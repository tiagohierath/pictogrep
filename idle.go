package main

import (
	"net/http"
	"sync"
	"time"
)

const (
	idleShutdownAfter = time.Hour
	idleCheckEvery    = time.Minute
)

// Pictogrep is a local server behind a browser tab, so closing the tab leaves
// the process running and holding the library lock. An open tab sends a
// heartbeat, which means a server nobody has touched for an hour has no tab
// left to break: it can close itself and let the next launch have the library.
type idleWatch struct {
	mu       sync.Mutex
	last     time.Time
	inFlight int
}

func newIdleWatch() *idleWatch {
	return &idleWatch{last: time.Now()}
}

func (watch *idleWatch) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		watch.touch(1)
		defer watch.touch(-1)
		next.ServeHTTP(w, r)
	})
}

func (watch *idleWatch) touch(delta int) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	watch.inFlight += delta
	watch.last = time.Now()
}

// A long import or a slow page holds the connection open without sending
// anything new, so work in flight always counts as activity.
func (watch *idleWatch) idleFor() time.Duration {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.inFlight > 0 {
		return 0
	}
	return time.Since(watch.last)
}

func (s *server) heartbeat(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
