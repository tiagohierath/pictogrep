package main

// The devices this one has agreed to talk to.
//
// A peer is remembered by its key and its certificate fingerprint, which are
// what it will be checked against on every later connection. The address is
// kept too, but only ever as a hint for where to look first: it is the one
// field here that is allowed to be wrong.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type peer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// SPKI DER, base64. What proves the peer is who paired.
	PublicKey string `json:"publicKey"`
	// SHA-256 of its TLS certificate, hex. Pinned: this is a network of two
	// machines that know each other, so there is no authority to ask.
	Fingerprint string `json:"fingerprint"`
	// Where it answered last. A hint, refreshed on every successful connection
	// and never trusted on its own.
	Address  string `json:"address,omitempty"`
	PairedAt int64  `json:"pairedAt"`
	LastSeen int64  `json:"lastSeen,omitempty"`
}

// peerStore is the paired devices, on disk as one JSON file.
//
// Small by nature: a person has a phone and a computer, maybe a laptop. There
// is no index and no database because there is nothing to index.
type peerStore struct {
	path string

	mu    sync.RWMutex
	peers map[string]peer
}

func openPeerStore(dir string) (*peerStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	store := &peerStore{path: filepath.Join(dir, "peers.json"), peers: map[string]peer{}}
	data, err := os.ReadFile(store.path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var stored []peer
	if err := json.Unmarshal(data, &stored); err != nil {
		// A corrupt file is not a reason to refuse to start, but it is a reason
		// not to overwrite it: pairing again is a QR code away, and the file
		// might be recoverable by hand.
		return nil, err
	}
	for _, one := range stored {
		store.peers[one.ID] = one
	}
	return store, nil
}

func (s *peerStore) all() []peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	found := make([]peer, 0, len(s.peers))
	for _, one := range s.peers {
		found = append(found, one)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].PairedAt < found[j].PairedAt })
	return found
}

func (s *peerStore) get(id string) (peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	one, found := s.peers[id]
	return one, found
}

// add records a newly paired device. Pairing the same device twice replaces the
// record rather than making a second one: a phone that was reinstalled has a
// new key and a new id, and a phone that paired twice with the same key is the
// same phone.
func (s *peerStore) add(one peer) error {
	if one.ID == "" || one.PublicKey == "" {
		return errors.New("a peer needs an id and a key")
	}
	if one.PairedAt == 0 {
		one.PairedAt = time.Now().Unix()
	}
	s.mu.Lock()
	s.peers[one.ID] = one
	s.mu.Unlock()
	return s.save()
}

func (s *peerStore) remove(id string) error {
	s.mu.Lock()
	delete(s.peers, id)
	s.mu.Unlock()
	return s.save()
}

// seen refreshes where a peer answered and when, which is what the interface
// shows as "last seen". Failing to write that down is not worth failing a sync
// over, so the error is returned and callers are free to drop it.
func (s *peerStore) seen(id, address string) error {
	s.mu.Lock()
	one, found := s.peers[id]
	if !found {
		s.mu.Unlock()
		return nil
	}
	one.LastSeen = time.Now().Unix()
	if address != "" {
		one.Address = address
	}
	s.peers[id] = one
	s.mu.Unlock()
	return s.save()
}

func (s *peerStore) save() error {
	s.mu.RLock()
	stored := make([]peer, 0, len(s.peers))
	for _, one := range s.peers {
		stored = append(stored, one)
	}
	s.mu.RUnlock()
	sort.Slice(stored, func(i, j int) bool { return stored[i].PairedAt < stored[j].PairedAt })
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(s.path, append(data, '\n'), 0o600)
}
