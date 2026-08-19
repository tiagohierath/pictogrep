package main

// Content hashes, remembered between runs.
//
// Sync asks the same question on every pass, on both sides of the wire: what is
// the SHA-256 of every picture in this library. The desktop needs it to answer a
// manifest, the phone needs it to decide what is still worth sending, and the
// honest way to compute it is to read every file. On a library of any size that
// is minutes of disk, and on a phone it is minutes of radio-idle CPU and a
// visibly warmer handset, for an answer that only changes when a file does.
//
// So a hash is kept alongside the size and modification time it was computed
// from, and recomputed only when one of those has moved. Losing this file costs
// exactly one slow pass, which is why a corrupt or missing one starts empty
// rather than failing: it is a cache, and it is the only file in the data
// directory that is safe to delete.

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
)

type digestRecord struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Digest  string `json:"digest"`
}

type digestCache struct {
	path string

	mu      sync.Mutex
	records map[string]digestRecord
	dirty   bool
}

func openDigestCache(path string) *digestCache {
	cache := &digestCache{path: path, records: map[string]digestRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	var stored map[string]digestRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return cache
	}
	cache.records = stored
	return cache
}

// of returns a file's content hash, reading the file only if what is on disk
// no longer matches what was hashed.
func (c *digestCache) of(path string) ([32]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return [32]byte{}, err
	}
	size, modTime := info.Size(), info.ModTime().UnixNano()

	c.mu.Lock()
	record, found := c.records[path]
	c.mu.Unlock()
	if found && record.Size == size && record.ModTime == modTime {
		if raw, err := hex.DecodeString(record.Digest); err == nil && len(raw) == 32 {
			var digest [32]byte
			copy(digest[:], raw)
			return digest, nil
		}
	}

	// Hashed outside the lock: this is the slow part, and holding the mutex
	// across it would serialize every reader behind one large file.
	digest, err := fileDigest(path)
	if err != nil {
		return [32]byte{}, err
	}
	c.mu.Lock()
	c.records[path] = digestRecord{Size: size, ModTime: modTime, Digest: hex.EncodeToString(digest[:])}
	c.dirty = true
	c.mu.Unlock()
	return digest, nil
}

// index hashes a set of paths and returns what maps to what, dropping
// remembered files that are no longer among them. Unreadable files are skipped
// rather than failing the whole pass: one picture the user deleted mid-scan is
// not a reason to refuse to sync the rest.
func (c *digestCache) index(paths []string) map[[32]byte]string {
	found := make(map[[32]byte]string, len(paths))
	live := make(map[string]bool, len(paths))
	for _, path := range paths {
		live[path] = true
		digest, err := c.of(path)
		if err != nil {
			continue
		}
		// First path wins, so that two copies of one picture give a stable
		// answer rather than one that depends on map ordering.
		if _, taken := found[digest]; !taken {
			found[digest] = path
		}
	}

	c.mu.Lock()
	for path := range c.records {
		if !live[path] {
			delete(c.records, path)
			c.dirty = true
		}
	}
	c.mu.Unlock()
	c.save()
	return found
}

// save writes the cache out if anything changed. Failures are dropped: the cost
// of not persisting a cache is recomputing it, and there is nothing useful to
// tell the user about that.
func (c *digestCache) save() {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	data, err := json.Marshal(c.records)
	c.dirty = false
	c.mu.Unlock()
	if err != nil {
		return
	}
	_ = writeFileAtomically(c.path, data, 0o600)
}
