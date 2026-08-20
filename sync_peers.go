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
	Address string `json:"address,omitempty"`
	// Whether Address is somewhere this device can actually connect to.
	//
	// True only for a peer this device dialled during pairing, because that is
	// the only case where the address is one the peer chose to listen on. The
	// device that was dialled saw the other end's source port, which nothing can
	// be sent to, so a desktop knows not to try pushing to a phone: the phone
	// pushes to it. Pairing is symmetric, reachability is not.
	Listens  bool  `json:"listens,omitempty"`
	PairedAt int64 `json:"pairedAt"`
	LastSeen int64 `json:"lastSeen,omitempty"`
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
	// Only for a peer whose address was never anywhere to send to anyway. A
	// peer that listens keeps the address it was paired on: what arrives here is
	// the source port of its outgoing connection, and writing that over a good
	// address would quietly turn a working peer into an unreachable one.
	if address != "" && !one.Listens {
		one.Address = address
	}
	s.peers[id] = one
	s.mu.Unlock()
	return s.save()
}

// rediscovered records where mDNS just found a peer answering right now,
// which is the one case allowed to overwrite the address of a peer that
// Listens.
//
// seen above refuses that on purpose: what arrives there is a source port
// nothing can be sent to, so writing it over a good address would break a
// working peer. mDNS is different in kind, not degree. The desktop
// advertises _pictogrep._tcp with its device id in the TXT record (see
// sync_mdns.go), so an answer naming this id is the desktop saying "here I
// am now", the same fact pairing's QR supplied once and DHCP is free to make
// stale at any later point: a new lease, a different network, sleep in one
// room and wake in another. Returns whether the id was a peer at all, so a
// caller can tell "updated" from "nothing here by that id".
func (s *peerStore) rediscovered(id, address string) bool {
	s.mu.Lock()
	one, found := s.peers[id]
	if !found {
		s.mu.Unlock()
		return false
	}
	one.Address = address
	one.LastSeen = time.Now().Unix()
	s.peers[id] = one
	s.mu.Unlock()
	_ = s.save()
	return true
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
