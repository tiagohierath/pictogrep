package main

// The sending half: what actually moves a picture off this device.
//
// Everything else in sync is a way of making this part safe or cheap. Pairing
// decides who is allowed to receive; the manifest decides what is worth
// sending; the digest cache decides how much of the library has to be read to
// work that out. This file is the loop that uses them.
//
// Three properties it is built around, in the order they matter:
//
//   - Silence. A picture is already saved on this device the moment the share
//     sheet accepts it, so nothing here is allowed to make that feel
//     provisional. A laptop that is asleep is not an error, it is Tuesday. The
//     interface shows a count of what is waiting and never a failure the user
//     is expected to act on.
//   - Delivered once. What a peer has confirmed is written down, so a picture
//     the user later deletes on the desktop stays deleted instead of being
//     helpfully restored on the next pass. This is why the record is of what
//     was delivered rather than of what is pending: the pending set is derived,
//     and cannot drift out of step with the library.
//   - No progress lost to a dropped connection. Each picture is confirmed on
//     its own, so a transfer interrupted after nine of ten resumes at the tenth
//     rather than at the first.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// How often the outbox looks for something to send while the app is running.
// Long, because the moments that actually matter (a picture just arrived, the
// user asked) all trigger a pass directly, and this only catches what those
// missed: a peer that was asleep, a transfer that failed halfway.
const outboxInterval = 5 * time.Minute

// outboxState is what the interface polls. Counts rather than a log, because
// the only question a person has about sync is whether it is finished.
type outboxState struct {
	// Waiting is how many pictures no paired device has confirmed yet.
	Waiting int `json:"waiting"`
	// Sending is true while a pass is in flight, which is what turns the
	// interface's "waiting" line into "sending" without inventing progress
	// percentages nobody can act on.
	Sending  bool  `json:"sending"`
	LastRunAt int64 `json:"lastRunAt,omitempty"`
	// LastError is shown only in the diagnostics view, never as an alert: a peer
	// being unreachable is the normal state of a laptop that is closed.
	LastError string `json:"lastError,omitempty"`
}

type outbox struct {
	server *syncServer
	path   string

	mu sync.Mutex
	// delivered[peerID] is the set of content hashes that peer has confirmed,
	// hex encoded because this is written to disk as JSON and read by a human
	// when something has gone wrong.
	delivered map[string]map[string]bool
	state     outboxState

	trigger chan struct{}
	stop    chan struct{}
	once    sync.Once
}

func newOutbox(server *syncServer, dir string) *outbox {
	box := &outbox{
		server:    server,
		path:      filepath.Join(dir, "outbox.json"),
		delivered: map[string]map[string]bool{},
		trigger:   make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
	if data, err := os.ReadFile(box.path); err == nil {
		var stored map[string][]string
		if err := json.Unmarshal(data, &stored); err == nil {
			for id, hashes := range stored {
				set := make(map[string]bool, len(hashes))
				for _, hash := range hashes {
					set[hash] = true
				}
				box.delivered[id] = set
			}
		}
	}
	return box
}

// start runs the loop until stop. Safe to call more than once; only the first
// call starts anything, because the server it belongs to can be restarted and
// a second loop would double every upload.
func (b *outbox) start() {
	b.once.Do(func() { go b.run() })
}

func (b *outbox) run() {
	ticker := time.NewTicker(outboxInterval)
	defer ticker.Stop()
	// A first pass on launch, which is what makes a phone that was offline all
	// day catch up the moment it is opened at home.
	b.flush()
	for {
		select {
		case <-b.stop:
			return
		case <-b.trigger:
			b.flush()
		case <-ticker.C:
			b.flush()
		}
	}
}

// nudge asks for a pass soon without waiting for one. Never blocks: a full
// channel already means a pass is queued, and a second request adds nothing.
func (b *outbox) nudge() {
	select {
	case b.trigger <- struct{}{}:
	default:
	}
}

func (b *outbox) shutdown() {
	close(b.stop)
}

func (b *outbox) snapshot() outboxState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// flush is one pass over every peer that can receive.
//
// Only one runs at a time. A pass that arrives while another is working is
// dropped rather than queued, because it would compute the same answer from the
// same library and race the one already uploading.
func (b *outbox) flush() {
	b.mu.Lock()
	if b.state.Sending {
		b.mu.Unlock()
		return
	}
	b.state.Sending = true
	b.mu.Unlock()

	failure := b.deliver()

	b.mu.Lock()
	b.state.Sending = false
	b.state.LastRunAt = time.Now().Unix()
	b.state.LastError = failure
	b.mu.Unlock()
	b.recount()
}

func (b *outbox) deliver() string {
	// Checked here rather than by not scheduling the loop, so that turning it
	// back on needs nothing restarted: the pass still runs on its timer and
	// simply finds itself switched off. recount below still counts what is
	// waiting, which is what lets the interface say how much would go.
	if !b.server.app.sendsAutomatically() {
		return ""
	}
	// Peers that listen: the device that dialled out during pairing knows where
	// to find the other one. The device that was dialled saw only a source port,
	// which is nowhere to send anything, so a desktop does not try to push to a
	// phone. See peer.Listens.
	var reachable []peer
	for _, candidate := range b.server.peers.all() {
		if candidate.Listens && candidate.Address != "" {
			reachable = append(reachable, candidate)
		}
	}
	if len(reachable) == 0 {
		return ""
	}

	index := b.server.libraryIndex()
	if len(index) == 0 {
		return ""
	}

	var lastError string
	for _, target := range reachable {
		if err := b.deliverTo(target, index); err != nil {
			lastError = fmt.Sprintf("%s: %v", target.Name, err)
			// The next peer still gets its turn: one unreachable laptop must not
			// stop a second one from receiving.
			continue
		}
	}
	return lastError
}

func (b *outbox) deliverTo(target peer, index map[[32]byte]string) error {
	// What this peer has not confirmed. Computed fresh from the library every
	// pass, so a picture deleted here simply stops being offered.
	pending := map[string]string{} // hash -> path
	b.mu.Lock()
	done := b.delivered[target.ID]
	for digest, path := range index {
		hash := hex.EncodeToString(digest[:])
		if !done[hash] {
			pending[hash] = path
		}
	}
	b.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}

	client := b.server.peerClient(target)
	base := "https://" + target.Address

	hashes := make([]string, 0, len(pending))
	for hash := range pending {
		hashes = append(hashes, hash)
	}
	missing, err := askPeerManifest(client, base, hashes)
	if err != nil {
		return err
	}

	// Everything offered and not reported missing is already there, under
	// whatever name it arrived with. Recorded as delivered without sending a
	// byte, which is the whole point of asking first.
	wanted := make(map[string]bool, len(missing))
	for _, hash := range missing {
		wanted[hash] = true
	}
	for hash := range pending {
		if !wanted[hash] {
			b.markDelivered(target.ID, hash)
		}
	}

	for _, hash := range missing {
		path, found := pending[hash]
		if !found {
			// The peer asked for something that was not offered. Nothing sensible
			// to send, and not worth failing the pass over.
			continue
		}
		if err := uploadTo(client, base, hash, path); err != nil {
			return err
		}
		// Written down one picture at a time, so an interrupted pass keeps what
		// it managed to deliver.
		b.markDelivered(target.ID, hash)
	}
	return nil
}

func (b *outbox) markDelivered(peerID, hash string) {
	b.mu.Lock()
	if b.delivered[peerID] == nil {
		b.delivered[peerID] = map[string]bool{}
	}
	b.delivered[peerID][hash] = true
	b.mu.Unlock()
	b.save()
}

// forget drops what a device had confirmed, so that pairing it again starts
// from an empty desktop rather than from a record of a library that is no
// longer there.
func (b *outbox) forget(peerID string) {
	b.mu.Lock()
	delete(b.delivered, peerID)
	b.mu.Unlock()
	b.save()
	b.recount()
}

// recount works out how many pictures no peer has yet, which is the one number
// the interface shows.
func (b *outbox) recount() {
	var reachable []peer
	for _, candidate := range b.server.peers.all() {
		if candidate.Listens && candidate.Address != "" {
			reachable = append(reachable, candidate)
		}
	}
	waiting := 0
	if len(reachable) > 0 {
		index := b.server.libraryIndex()
		b.mu.Lock()
		for digest := range index {
			hash := hex.EncodeToString(digest[:])
			for _, target := range reachable {
				if !b.delivered[target.ID][hash] {
					waiting++
					break
				}
			}
		}
		b.mu.Unlock()
	}
	b.mu.Lock()
	b.state.Waiting = waiting
	b.mu.Unlock()
}

func (b *outbox) save() {
	b.mu.Lock()
	stored := make(map[string][]string, len(b.delivered))
	for id, set := range b.delivered {
		hashes := make([]string, 0, len(set))
		for hash := range set {
			hashes = append(hashes, hash)
		}
		stored[id] = hashes
	}
	b.mu.Unlock()
	data, err := json.Marshal(stored)
	if err != nil {
		return
	}
	if err := writeFileAtomically(b.path, data, 0o600); err != nil {
		log.Printf("sync: could not record what was delivered: %v", err)
	}
}

func askPeerManifest(client *http.Client, base string, hashes []string) ([]string, error) {
	body, err := json.Marshal(manifestRequest{Hashes: hashes})
	if err != nil {
		return nil, err
	}
	response, err := client.Post(base+"/manifest", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asked what was missing and got %s", response.Status)
	}
	var answer manifestResponse
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return nil, err
	}
	return answer.Missing, nil
}

func uploadTo(client *http.Client, base, hash, path string) error {
	file, err := os.Open(path)
	if err != nil {
		// Gone between the index and the upload. Not an error worth stopping for.
		return nil
	}
	defer file.Close()
	target := fmt.Sprintf("%s/blobs/%s?name=%s", base, hash, url.QueryEscape(filepath.Base(path)))
	response, err := client.Post(target, "application/octet-stream", file)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusConflict:
		// The other side already has it. Counts as delivered: that is what the
		// user means by synced.
		return nil
	default:
		return fmt.Errorf("sending %s answered %s", filepath.Base(path), response.Status)
	}
}
