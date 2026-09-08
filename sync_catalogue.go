package main

// The other direction: a phone taking pictures FROM a computer.
//
// Sending is a push, because the phone is where new pictures appear and the
// desktop is where the disk is. Receiving cannot be the mirror image of that,
// and not for want of trying: reachability here is asymmetric on purpose. The
// phone dialled the desktop when it scanned the QR, so the phone holds an
// address that answers (peer.Listens) and the desktop holds the source port of
// a connection that is long closed. A desktop cannot open a socket to a phone
// at all, which is what sync_controls.go means by "a desktop cannot push to a
// phone".
//
// So this direction is a PULL. The phone asks what the desktop has, decides
// what it wants, and fetches it, over exactly the connection it already knows
// how to open. Nothing new is needed on the network, and the device with the
// small disk, the metered radio and the battery is the one deciding what
// arrives, which is where that decision belongs anyway.
//
// The protocol is the manifest exchange turned around, on the same
// authenticated listener:
//
//	POST /catalogue    what do you have          (this file, desktop side)
//	GET  /blobs/{hash} give me that one          (this file, desktop side)
//
// against the sending direction's existing:
//
//	POST /manifest     which of mine are you missing
//	POST /blobs/{hash} here it is

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// catalogueRequest asks what a device holds. An empty Folder means the whole
// library; naming one narrows it to that folder, which is how a phone takes a
// hundred pictures from a library of thousands.
type catalogueRequest struct {
	Folder string `json:"folder"`
}

// catalogueEntry is one picture, described by the only thing both sides agree
// on. The name is a suggestion for what to call the file locally; the hash is
// what identifies it.
type catalogueEntry struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
}

type catalogueResponse struct {
	// Folders and their counts, so the asking device can offer a choice before
	// it commits to a download. Always the whole list, whatever Folder said.
	Folders []catalogueFolder `json:"folders"`
	// The pictures themselves, in the folder that was asked for.
	Entries []catalogueEntry `json:"entries"`
}

type catalogueFolder struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// handleCatalogue answers what this library holds, by content hash.
//
// Hashes only, never paths. A path says where someone keeps their pictures and
// what they are called, which is more than the other end of a sync needs to
// know and more than it should be told; the base name goes because a file has
// to be called something once it lands.
func (s *syncServer) handleCatalogue(w http.ResponseWriter, r *http.Request) {
	var request catalogueRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&request); err != nil {
		sendError(w, http.StatusBadRequest, fmt.Errorf("malformed catalogue request"))
		return
	}

	// The whole library by hash, which the digest cache makes cheap after the
	// first pass. This is the same call the manifest exchange makes, so a
	// device that has just sent pictures has already paid for it.
	index := s.libraryIndex()

	// Which paths are in scope. A named folder narrows it; anything else is
	// everything. A folder that does not exist answers with no pictures rather
	// than an error: the folder may have been deleted between the moment the
	// phone was shown the list and the moment it asked.
	var wanted map[string]bool
	if request.Folder != "" {
		wanted = map[string]bool{}
		for _, path := range s.appServer.collectionImages(request.Folder) {
			wanted[path] = true
		}
	}

	entries := make([]catalogueEntry, 0, len(index))
	for digest, path := range index {
		if wanted != nil && !wanted[path] {
			continue
		}
		entries = append(entries, catalogueEntry{
			Hash: hex.EncodeToString(digest[:]),
			Name: filepath.Base(path),
		})
	}
	// Sorted so that two calls answer in the same order, which makes a resumed
	// download resume where it left off rather than somewhere arbitrary. Map
	// iteration would not.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Hash < entries[j].Hash })

	folders := []catalogueFolder{}
	for _, name := range s.appServer.collectionNames() {
		folders = append(folders, catalogueFolder{Name: name, Count: len(s.appServer.collectionImages(name))})
	}

	sendJSON(w, http.StatusOK, catalogueResponse{Folders: folders, Entries: entries})
}

// handleDownloadBlob serves one picture's bytes, named by the hash the
// catalogue already agreed on.
//
// Only a hash the library actually holds is servable, which is what stops this
// being a way to read arbitrary files off the computer: the hash is looked up
// in the index and the path comes from there, so nothing a caller sends is
// ever joined onto a directory.
func (s *syncServer) handleDownloadBlob(w http.ResponseWriter, r *http.Request) {
	raw, err := hex.DecodeString(r.PathValue("hash"))
	if err != nil || len(raw) != 32 {
		sendError(w, http.StatusBadRequest, fmt.Errorf("that is not a hash"))
		return
	}
	var digest [32]byte
	copy(digest[:], raw)

	path, held := s.libraryIndex()[digest]
	if !held {
		sendError(w, http.StatusNotFound, fmt.Errorf("no picture with that hash"))
		return
	}
	file, err := os.Open(path)
	if err != nil {
		sendError(w, http.StatusNotFound, fmt.Errorf("no picture with that hash"))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		sendError(w, http.StatusInternalServerError, fmt.Errorf("could not read that picture"))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	// ServeContent rather than io.Copy, for range requests: a phone that loses
	// wifi halfway through a 12 MB picture resumes rather than starting again.
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}
