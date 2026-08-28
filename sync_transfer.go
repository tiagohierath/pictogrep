package main

// The manifest exchange and the blob upload: the two requests that move a
// picture from a phone's outbox into the library, silently.
//
// "Silently" is the design goal, not a nice-to-have. A shared picture already
// exists in the phone's own library the moment it is saved, so nothing about
// sync is allowed to make that feel provisional: no spinner blocking the UI, no
// "sync failed" for a laptop that is simply asleep, and no image (2).jpg for a
// picture the desktop already has. The manifest step is what makes the last one
// possible without transferring a byte: the phone asks what is missing before
// it sends anything, using the same content hash saveImportedImageWithOptions
// already dedupes by, so a picture the desktop has under any name is
// recognized before its bytes ever cross the network.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// manifestRequest lists what the phone is offering, by content hash.
type manifestRequest struct {
	Hashes []string `json:"hashes"`
}

// manifestResponse says which of those the desktop does not already have.
// Everything not listed needs no transfer: it is already in the library, under
// whatever name it arrived with the first time.
type manifestResponse struct {
	Missing []string `json:"missing"`
}

func (s *syncServer) handleManifest(w http.ResponseWriter, r *http.Request) {
	var req manifestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, fmt.Errorf("malformed manifest"))
		return
	}
	have := s.libraryIndex()
	missing := make([]string, 0, len(req.Hashes))
	for _, hash := range req.Hashes {
		raw, err := hex.DecodeString(hash)
		if err != nil || len(raw) != 32 {
			continue
		}
		var digest [32]byte
		copy(digest[:], raw)
		if _, held := have[digest]; !held {
			missing = append(missing, hash)
		}
	}
	sendJSON(w, http.StatusOK, manifestResponse{Missing: missing})
}

// handleUploadBlob accepts one picture's bytes, named by the hash the manifest
// already agreed on, and hands it to the same import path a folder drop or a
// share-target save uses: verified, deduped again in case two devices raced
// to send the same picture, and filed into the folder the request named. The
// hash is checked before anything is kept, so a connection that drops mid
// upload leaves nothing behind for the phone to wrongly believe arrived.
func (s *syncServer) handleUploadBlob(w http.ResponseWriter, r *http.Request) {
	wanted := r.PathValue("hash")
	folder := r.URL.Query().Get("folder")
	name := r.URL.Query().Get("name")
	if name == "" {
		name = wanted + ".jpg"
	}

	// Spooled to a temp file rather than read fully into memory: a phone can
	// batch-upload many pictures in a row, and buffering each one whole would
	// stack up peak memory with every concurrent transfer.
	spool, err := os.CreateTemp(s.app.dataDir, "sync-upload-*.tmp")
	if err != nil {
		sendError(w, http.StatusInternalServerError, fmt.Errorf("could not spool upload"))
		return
	}
	defer os.Remove(spool.Name())
	defer spool.Close()

	body := io.LimitReader(r.Body, maxUploadBytes+1)
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(spool, hasher), body); err != nil {
		sendError(w, http.StatusBadRequest, fmt.Errorf("could not read upload"))
		return
	}
	if hex.EncodeToString(hasher.Sum(nil)) != wanted {
		// Whatever arrived does not match what was promised. Refused outright,
		// rather than kept and reconciled: a mismatch here is either corruption
		// in transit or two different pictures racing on the same hash, and
		// either way the library must not accept it as the thing the phone
		// thinks it just synced.
		sendError(w, http.StatusUnprocessableEntity, fmt.Errorf("upload did not match its declared hash"))
		return
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		sendError(w, http.StatusInternalServerError, fmt.Errorf("could not read upload"))
		return
	}

	// No source directory: a picture arriving from a phone belongs in the
	// library itself, exactly like one dropped on the window or saved from the
	// share sheet. `source` in importDestination names one of the library's own
	// indexed folders on this disk, so anything descriptive passed here (the
	// sending device, say) is read as a path, fails to stat, and turns every
	// upload into "unknown destination folder".
	result, status, err := s.appServer.saveImportedImageWithOptions(
		spool, name, folder, "", true, true, nil,
	)
	if err != nil {
		sendError(w, status, err)
		return
	}
	// Counted even for a duplicate the library already had: the desktop that
	// polls this is deciding whether to bother checking at all, and a check
	// that finds nothing new is a cheap no-op, where a picture that never
	// bumped this because it happened to arrive twice is a real one missed.
	s.arrivals.Add(1)
	sendJSON(w, status, result)
}
