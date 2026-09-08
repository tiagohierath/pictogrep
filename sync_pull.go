package main

// Taking pictures from a paired computer, on this device's own initiative.
//
// The counterpart to sync_catalogue.go, which is the answering half. Read that
// file first for why this direction is a pull rather than a push.
//
// Deliberately not the outbox. The outbox is a background loop with a timer, a
// record of what every peer has confirmed, and a promise to keep trying: right
// for sending, because a picture shared into the app must reach the computer
// eventually without anyone thinking about it. Receiving is the opposite kind
// of act. Someone asks for a folder, watches it arrive, and is done. Giving it
// a timer would mean a phone that fills itself up in the background over
// someone's mobile data, which is a bug however well it works.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// pullState is what the interface shows while a pull runs, and what it finds
// afterwards. One pull at a time, so this is a single value rather than a set.
type pullState struct {
	Running bool `json:"running"`
	// Peer is the name of the computer being taken from, for a line that reads
	// "Getting pictures from Tiago's desktop" rather than naming nothing.
	Peer string `json:"peer,omitempty"`
	// Total is how many pictures this pull decided it needed, fixed at the
	// start. Done counts up to it.
	Total int `json:"total"`
	Done  int `json:"done"`
	// Skipped is pictures the catalogue offered that this device already had.
	// Counted separately from Done because "20 arrived, 400 were already here"
	// is the answer to "why was that so fast", and reporting them together
	// invites the opposite question.
	Skipped int `json:"skipped"`
	// Failed is pictures that would not download. A pull does not abandon
	// everything because one file is unreadable on the other end.
	Failed    int    `json:"failed"`
	LastError string `json:"lastError,omitempty"`
	// Finished marks a completed pass whose numbers are still worth showing.
	Finished bool `json:"finished"`
}

type puller struct {
	server *syncServer

	mu     sync.Mutex
	state  pullState
	cancel context.CancelFunc
}

func newPuller(server *syncServer) *puller { return &puller{server: server} }

func (p *puller) snapshot() pullState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// stop asks a running pull to give up. What already landed stays: every
// picture is imported as it arrives, so a cancelled pull is a shorter pull
// rather than an undone one.
func (p *puller) stopPull() {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// reachablePeer is the computer this device can actually open a connection to.
// Same test the outbox makes, and for the same reason: only a peer this device
// dialled during pairing has an address worth trying.
func (p *puller) reachablePeer(id string) (peer, bool) {
	for _, candidate := range p.server.peers.all() {
		if !candidate.Listens || candidate.Address == "" {
			continue
		}
		if id == "" || candidate.ID == id {
			return candidate, true
		}
	}
	return peer{}, false
}

// catalogueOf asks a computer what it holds. Used both to draw the folder
// picker, with no folder named, and to start a pull.
func (p *puller) catalogueOf(ctx context.Context, target peer, folder string) (catalogueResponse, error) {
	body, err := json.Marshal(catalogueRequest{Folder: folder})
	if err != nil {
		return catalogueResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+target.Address+"/catalogue", bytes.NewReader(body))
	if err != nil {
		return catalogueResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.server.peerClient(target).Do(request)
	if err != nil {
		return catalogueResponse{}, fmt.Errorf("could not reach %s", target.Name)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return catalogueResponse{}, fmt.Errorf("%s refused to list its pictures", target.Name)
	}
	var catalogue catalogueResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&catalogue); err != nil {
		return catalogueResponse{}, fmt.Errorf("could not read what %s sent", target.Name)
	}
	return catalogue, nil
}

// start begins a pull, in the background, and returns as soon as it is under
// way. The interface polls snapshot() rather than holding a request open for
// what can be several minutes of downloading.
func (p *puller) start(target peer, folder, into string) error {
	p.mu.Lock()
	if p.state.Running {
		p.mu.Unlock()
		return fmt.Errorf("already getting pictures from %s", p.state.Peer)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	p.state = pullState{Running: true, Peer: target.Name}
	p.cancel = cancel
	p.mu.Unlock()

	go p.run(ctx, target, folder, into)
	return nil
}

func (p *puller) run(ctx context.Context, target peer, folder, into string) {
	defer func() {
		p.mu.Lock()
		p.state.Running = false
		p.state.Finished = true
		p.cancel = nil
		p.mu.Unlock()
	}()

	catalogue, err := p.catalogueOf(ctx, target, folder)
	if err != nil {
		p.fail(err)
		return
	}

	// What this device already holds, so a picture that came from here in the
	// first place is not downloaded back. The same content hash the sending
	// direction dedupes by, which is why a phone that pushed a picture up will
	// not pull it down again on the next pass.
	have := p.server.libraryIndex()

	var wanted []catalogueEntry
	skipped := 0
	for _, entry := range catalogue.Entries {
		raw, err := hex.DecodeString(entry.Hash)
		if err != nil || len(raw) != 32 {
			continue
		}
		var digest [32]byte
		copy(digest[:], raw)
		if _, held := have[digest]; held {
			skipped++
			continue
		}
		wanted = append(wanted, entry)
	}

	p.mu.Lock()
	p.state.Total, p.state.Skipped = len(wanted), skipped
	p.mu.Unlock()

	client := p.server.peerClient(target)
	for _, entry := range wanted {
		if ctx.Err() != nil {
			return
		}
		if err := p.fetchOne(ctx, client, target, entry, into); err != nil {
			p.mu.Lock()
			p.state.Failed++
			p.state.LastError = err.Error()
			p.mu.Unlock()
			// One picture that will not come across is not a failed pull. The
			// alternative is losing the other nine hundred to it.
			continue
		}
		p.mu.Lock()
		p.state.Done++
		p.mu.Unlock()
	}
}

// fetchOne downloads a picture and files it, through the same import path a
// share or a folder drop uses: hash checked before anything is kept, deduped
// again in case it arrived by another route while this pull was running.
func (p *puller) fetchOne(ctx context.Context, client *http.Client, target peer, entry catalogueEntry, into string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+target.Address+"/blobs/"+entry.Hash, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%s: %v", entry.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", entry.Name, response.Status)
	}

	// Spooled to disk rather than held in memory, for the same reason the
	// upload side spools: a picture can be tens of megabytes and there is no
	// reason for the peak to be the file size.
	spool, err := os.CreateTemp(p.server.app.dataDir, "sync-pull-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()

	if _, err := io.Copy(spool, io.LimitReader(response.Body, maxUploadBytes+1)); err != nil {
		return fmt.Errorf("%s: %v", entry.Name, err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return err
	}

	name := entry.Name
	if name == "" {
		name = entry.Hash + ".jpg"
	}
	// The hash is not re-checked here. saveImportedImageWithOptions hashes what
	// it is given and dedupes on it, so a corrupted download lands as its own
	// picture rather than silently replacing the real one, and the next pull
	// asks for it again because this device still does not have the hash the
	// catalogue named.
	if _, _, err := p.server.appServer.saveImportedImageWithOptions(spool, name, into, "", true, true, nil); err != nil {
		return fmt.Errorf("%s: %v", name, err)
	}
	return nil
}

func (p *puller) fail(err error) {
	p.mu.Lock()
	p.state.LastError = err.Error()
	p.mu.Unlock()
}

// pullTimeout bounds a whole pass. Generous, because a folder of a thousand
// pictures over a phone's wifi is genuinely slow, and bounded, because a
// computer that goes to sleep mid-pull must not leave the interface saying
// "getting pictures" until the app is restarted.
const pullTimeout = 2 * time.Hour
