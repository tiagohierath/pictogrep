package main

// The escape hatch. Sync runs in the background and stays out of the way by
// default, and every one of these exists so a person can reach in and change
// what it is doing without reading a log file first.
//
// Nothing under here blocks the caller on a slow network. "Sync now" starts a
// background attempt and returns immediately; the outbox counters are what the
// interface polls to show progress, the same way the library's own indexing
// job is already polled.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
)

// localAddresses lists this machine's own LAN addresses, so a QR can carry
// somewhere for the phone to try before it has mDNS working. Loopback and
// link-local are skipped: nothing outside this machine can reach either.
func localAddresses() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var found []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			found = append(found, ip4.String())
		}
	}
	return found
}

// deviceDisplayName is what a peer shows for this machine in its list of
// paired devices: "Tiago's Laptop", not a device id nobody chose.
func deviceDisplayName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "This computer"
}

func (s *server) requireSync(w http.ResponseWriter) (*syncServer, bool) {
	if s.sync == nil {
		sendError(w, http.StatusServiceUnavailable, fmt.Errorf("sync is not available on this build"))
		return nil, false
	}
	return s.sync, true
}

// GET /api/app/sync: the paired devices, whether the port is open, and this
// device's own pairing QR data if a pairing is in progress. What the settings
// panel and the "Connect phone" screen are both built from.
func (s *server) syncState(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{
		"deviceId":   sync.identity.id,
		"deviceName": sync.identity.name,
		"listening":  sync.listener != nil,
		"peers":      sync.peers.all(),
		"outbox":     sync.outbox.snapshot(),
	})
}

// POST /api/app/sync/listen: "Sync over this network." Opens the port even
// though no phone has paired yet, which is what showing a first QR needs: the
// address and fingerprint it carries only exist once the listener is up.
func (s *server) startSyncListening(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	if err := sync.start(); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/app/sync/pause: closes the port. Paired devices are kept, so
// resuming is a click rather than a re-pair; nothing reaches this machine over
// the network while it is paused, which is the point of offering it at all.
func (s *server) pauseSync(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	if err := sync.stop(); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/app/sync/peers/{id}: "Forget device." The peer's key stops being
// accepted immediately; if it is the last one, the port is also closed, since a
// server with nothing paired has no reason to keep listening.
func (s *server) forgetSyncPeer(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := sync.peers.remove(id); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	// What that device had already received is forgotten with it, so pairing it
	// again fills an empty library rather than skipping everything it once had.
	sync.outbox.forget(id)
	if len(sync.peers.all()) == 0 {
		_ = sync.stop()
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/app/sync/pairing: "Connect phone." Opens the port if it was
// closed and mints a fresh QR payload, replacing whichever one was on screen.
func (s *server) beginSyncPairing(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	if err := sync.start(); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	secret, err := sync.pairing.begin()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	addresses := localAddresses()
	session := pairingSession{
		ProtocolVersion: 1,
		DeviceID:        sync.identity.id,
		Name:            sync.identity.name,
		Addresses:       addresses,
		// The port that is open, not the one the constant asks for: start() has
		// just returned, so these are the same number in every real install, and
		// the QR stays truthful if that ever stops being so.
		Port: sync.listeningPort(),
		Fingerprint:     sync.fingerprint(),
		Secret:          secret,
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	qr, err := encodeQR([]byte("pictogrep://pair?" + urlSafeBase64(encoded)))
	if err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{
		"session":   session,
		"qrSVG":     qr,
		"expiresIn": int(pairingSecretTTL.Seconds()),
	})
}

// POST /api/app/sync/now: nudges outboxes into flushing sooner than their own
// schedule would. On the desktop this asks every reachable paired phone to
// push what it is holding; the endpoint itself does not wait for that to
// finish, because "sync now" is a request to hurry, not a request to block.
func (s *server) syncNow(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	sync.outbox.nudge()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/app/sync/rediscovered: "mDNS just answered for a peer I know."
//
// Nothing in this process runs NsdManager; that lives in the Android shell,
// which calls this the moment a resolve names a device id already in the
// peer store (see PeerDiscovery.kt). Landing it here rather than teaching Go
// to speak mDNS on Android keeps one already-working implementation
// (hashicorp/mdns, desktop side) instead of two, and NsdManager is what the
// platform actually wants asked on a phone: it is backed by the system
// resolver, works through Doze, and does not need a multicast lock held for
// as long as the listener runs.
func (s *server) peerRediscovered(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	var req struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil || req.ID == "" || req.Address == "" {
		sendError(w, http.StatusBadRequest, fmt.Errorf("a rediscovery needs an id and an address"))
		return
	}
	if !sync.peers.rediscovered(req.ID, req.Address) {
		// Not an error: NsdManager answers for every _pictogrep._tcp on the
		// LAN, including a device this one has never paired with.
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "known": false})
		return
	}
	// The stale address was very possibly the reason the last few passes had
	// nothing to report; worth trying again now rather than on the next tick.
	sync.outbox.nudge()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "known": true})
}
