package main

// Pairing: the one moment a stranger is allowed to talk to this device at all.
//
// Everything before a peer is trusted has to assume the network is hostile: a
// coffee shop, a shared office, a router someone else configured. So the
// pairing endpoint is the only one an unpaired client can reach, the secret it
// needs is random, short-lived and spent on first use, and the two sides
// additionally confirm each other's TLS certificate against the fingerprint
// carried in the QR. A stranger on the same wifi can see the pairing request
// happen and still cannot complete it without that fingerprint.
//
// Once paired, nothing here runs again until the user removes the device and
// pairs it over: see sync_peers.go for what "paired" means afterwards.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// How long a QR is good for. Long enough to walk across a room with a phone,
// short enough that a photo of an old screen is useless.
const pairingSecretTTL = 3 * time.Minute

// pairingSession is the offer a QR encodes: everything a phone needs to find
// this device, prove it is really it, and be let in.
type pairingSession struct {
	ProtocolVersion int      `json:"v"`
	DeviceID        string   `json:"id"`
	Name            string   `json:"name"`
	Addresses       []string `json:"addrs"`
	Port            int      `json:"port"`
	// SHA-256 of the certificate, hex. What the phone pins before it sends
	// anything else, so nothing on the wire before this point can be trusted.
	Fingerprint string `json:"fp"`
	Secret      string `json:"secret"`
}

// pairingManager issues and redeems one secret at a time. A second "Connect
// phone" click replaces the pending one rather than stacking a queue nobody
// asked for: only the QR on screen right now should work.
type pairingManager struct {
	mu      sync.Mutex
	secret  string
	expires time.Time
}

func newPairingManager() *pairingManager { return &pairingManager{} }

// begin mints a new secret and invalidates whatever QR was on screen before.
func (m *pairingManager) begin() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	m.mu.Lock()
	m.secret, m.expires = secret, time.Now().Add(pairingSecretTTL)
	m.mu.Unlock()
	return secret, nil
}

// redeem spends the secret if it matches and has not expired. It can only ever
// succeed once: the point of a pairing secret is that a phone that captured the
// wire traffic and replays it gets nothing.
func (m *pairingManager) redeem(secret string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.secret == "" || time.Now().After(m.expires) {
		return false
	}
	// Constant time: this is the one comparison on the whole pairing path that
	// stands between a stranger and being trusted forever.
	if subtle.ConstantTimeCompare([]byte(secret), []byte(m.secret)) != 1 {
		return false
	}
	m.secret = ""
	return true
}

// certFingerprint hashes a certificate the way both ends need to agree on: the
// same DER bytes the TLS handshake itself will present.
func certFingerprint(cert *tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}

// pairRequest is what a phone sends once it has verified the pinned
// fingerprint and is ready to spend the secret.
type pairRequest struct {
	Secret    string `json:"secret"`
	DeviceID  string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"` // SPKI DER, base64
}

// pairAccepted is the desktop's half of the introduction: enough for the phone
// to recognize it again without ever needing this endpoint a second time.
type pairAccepted struct {
	DeviceID  string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

func (s *syncServer) handlePair(w http.ResponseWriter, r *http.Request) {
	// Checked before the secret is spent: a client that shows up with no
	// certificate at all cannot be turned into a peer record that will ever
	// authenticate again (see authenticated() in sync_server.go, which requires
	// one), so it is refused without burning the one QR the user has on
	// screen. tls.Config.ClientAuth is RequestClientCert, not RequireAnyClientCert,
	// because /pair is the one endpoint a client with no certificate yet is
	// allowed to reach at all; every other route demands one.
	if len(r.TLS.PeerCertificates) == 0 {
		sendError(w, http.StatusBadRequest, fmt.Errorf("no device certificate presented"))
		return
	}
	var req pairRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, fmt.Errorf("malformed pairing request"))
		return
	}
	if req.DeviceID == "" || req.PublicKey == "" {
		sendError(w, http.StatusBadRequest, fmt.Errorf("malformed pairing request"))
		return
	}
	if !s.pairing.redeem(req.Secret) {
		// Deliberately unspecific. "wrong secret" and "already used" and
		// "expired" all look the same to whoever is asking, which is the point:
		// an unpaired client learns nothing from a failed attempt.
		sendError(w, http.StatusForbidden, fmt.Errorf("pairing failed"))
		return
	}
	sum := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	fingerprint := hex.EncodeToString(sum[:])
	newPeer := peer{
		ID: req.DeviceID, Name: req.Name, PublicKey: req.PublicKey,
		Fingerprint: fingerprint, Address: r.RemoteAddr,
	}
	if err := s.peers.add(newPeer); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	publicKey, err := publicKeyDER(s.identity)
	if err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, pairAccepted{DeviceID: s.identity.id, Name: s.identity.name, PublicKey: publicKey})
}
