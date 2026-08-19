package main

// The other half of pairing: this device as the one holding the phone,
// scanning a QR a desktop is showing, and connecting out to it.
//
// It reuses exactly what the desktop side built rather than inventing a
// parallel path: the same identity, the same self-signed certificate
// machinery in loadOrCreateCertificate (a throwaway transport key unrelated
// to the device identity, same as the desktop's own listener uses), and the
// same peer store. Pairing is symmetric once it succeeds — both sides end up
// with a peer record for the other — so a desktop that someday wants to
// initiate its own pairing has nothing new to build either.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// parsePairingURL extracts the session a QR encodes from the
// pictogrep://pair?<base64> link a desktop shows.
func parsePairingURL(raw string) (pairingSession, error) {
	const prefix = "pictogrep://pair?"
	if !strings.HasPrefix(raw, prefix) {
		return pairingSession{}, fmt.Errorf("that code is not a Pictogrep pairing code")
	}
	encoded := strings.TrimPrefix(raw, prefix)
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return pairingSession{}, fmt.Errorf("that code could not be read")
	}
	var session pairingSession
	if err := json.Unmarshal(data, &session); err != nil {
		return pairingSession{}, fmt.Errorf("that code could not be read")
	}
	if session.DeviceID == "" || session.Fingerprint == "" || session.Secret == "" || session.Port == 0 {
		return pairingSession{}, fmt.Errorf("that code is missing something Pictogrep needs")
	}
	return session, nil
}

// pairAsClient dials the desktop a QR named, pinning its certificate to the
// fingerprint the QR carried before sending anything else, presents this
// device's own certificate so the desktop can recognize it again, and spends
// the pairing secret. On success both ends have a peer record for the other.
func (s *syncServer) pairAsClient(session pairingSession) error {
	config := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		// The certificate authority model this network does not have: nothing
		// signs these certificates but the device presenting them, so the
		// standard chain check is replaced with the one thing that actually
		// proves identity here, a byte-for-byte match against the fingerprint
		// the QR carried in person.
		InsecureSkipVerify: true, //nolint:gosec // verified in VerifyPeerCertificate below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return fmt.Errorf("the computer presented no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != session.Fingerprint {
				return fmt.Errorf("the computer's certificate does not match the code that was scanned")
			}
			return nil
		},
	}

	var lastErr error
	// Every address the QR carried, in order, because the LAN address a
	// desktop had when it drew the code is a hint, not a guarantee: the
	// machine could be on a second interface, or the one listed first could
	// simply not be reachable from wherever the phone's radio landed.
	for _, address := range session.Addresses {
		target := fmt.Sprintf("%s:%d", address, session.Port)
		if err := s.attemptPair(target, config, session); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no address in the code was reachable")
	}
	return lastErr
}

func (s *syncServer) attemptPair(target string, tlsConfig *tls.Config, session pairingSession) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialTLSContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			return tls.DialWithDialer(dialer, network, addr, tlsConfig)
		},
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport}

	publicKey, err := publicKeyDER(s.identity)
	if err != nil {
		return err
	}
	body, err := json.Marshal(pairRequest{
		Secret: session.Secret, DeviceID: s.identity.id, Name: s.identity.name, PublicKey: publicKey,
	})
	if err != nil {
		return err
	}
	response, err := client.Post("https://"+target+"/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", target, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("pairing was refused")
	}
	var accepted pairAccepted
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		return fmt.Errorf("the computer's reply could not be read")
	}

	// The desktop's certificate, already verified against the QR's fingerprint
	// by the handshake that just completed, is what this device pins from now
	// on. Recorded here rather than trusted from the QR's fingerprint field
	// forever, because the QR itself is single-use and this is the record that
	// has to outlive it.
	return s.peers.add(peer{
		ID: accepted.DeviceID, Name: accepted.Name, PublicKey: accepted.PublicKey,
		Fingerprint: session.Fingerprint, Address: target,
	})
}

// peerClient dials a device this one has already paired with.
//
// The same pinning as pairing, against a different source of truth: pairing
// pins the fingerprint the QR carried, and everything afterwards pins the one
// written into the peer record when that pairing succeeded. A peer whose
// certificate has changed is refused rather than re-trusted, because on this
// network a changed certificate and an impostor are the same event.
func (s *syncServer) peerClient(target peer) *http.Client {
	config := &tls.Config{
		Certificates:       []tls.Certificate{s.cert},
		InsecureSkipVerify: true, //nolint:gosec // pinned in VerifyPeerCertificate below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return fmt.Errorf("%s presented no certificate", target.Name)
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != target.Fingerprint {
				return fmt.Errorf("%s is not the device that was paired", target.Name)
			}
			return nil
		},
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		// Generous next to pairing's ten seconds: this one carries a picture over
		// a phone's wifi, and a timeout that fires mid-upload is a transfer that
		// gets retried from the beginning rather than one that fails usefully.
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialTLSContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return tls.DialWithDialer(dialer, network, addr, config)
			},
		},
	}
}

// pairWithScannedRequest is what the phone's own page posts to its own local
// server right after the camera reads a QR: the raw pictogrep://pair? text,
// unparsed, because parsing it is this file's job and not JavaScript's.
type pairWithScannedRequest struct {
	Data string `json:"data"`
}

// POST /api/app/sync/pair-with: scan result in, paired peer out. Runs on
// every platform that has sync at all, not only the phone build: nothing
// about connecting outward is phone-specific, and a desktop pairing to
// another desktop over the same QR is the same code path.
func (s *server) pairWithScanned(w http.ResponseWriter, r *http.Request) {
	sync, ok := s.requireSync(w)
	if !ok {
		return
	}
	var req pairWithScannedRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, fmt.Errorf("malformed request"))
		return
	}
	session, err := parsePairingURL(req.Data)
	if err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := sync.pairAsClient(session); err != nil {
		sendError(w, http.StatusBadGateway, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "name": session.Name})
}
