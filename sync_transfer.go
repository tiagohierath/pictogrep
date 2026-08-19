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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	have := s.app.digestSet()
	missing := make([]string, 0, len(req.Hashes))
	for _, hash := range req.Hashes {
		raw, err := hex.DecodeString(hash)
		if err != nil || len(raw) != 32 {
			continue
		}
		var digest [32]byte
		copy(digest[:], raw)
		if !have[digest] {
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

	body := io.LimitReader(r.Body, maxUploadBytes+1)
	hasher := sha256.New()
	data, err := io.ReadAll(io.TeeReader(body, hasher))
	if err != nil {
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

	peer, _ := peerFromContext(r.Context())
	source := "sync:" + peer.Name
	result, status, err := s.appServer.saveImportedImageWithOptions(
		bytes.NewReader(data), name, folder, source, true, true, nil,
	)
	if err != nil {
		sendError(w, status, err)
		return
	}
	sendJSON(w, status, result)
}
