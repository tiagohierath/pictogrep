package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdleWatchCountsWorkInFlightAsActivity(t *testing.T) {
	watch := newIdleWatch()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := watch.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/app/state", nil))
	}()
	<-entered

	watch.mu.Lock()
	watch.last = time.Now().Add(-2 * idleShutdownAfter)
	watch.mu.Unlock()
	if idle := watch.idleFor(); idle != 0 {
		t.Fatalf("a long import looked idle after %s and would be shut down mid-run", idle)
	}

	close(release)
	<-done
	if idle := watch.idleFor(); idle > time.Second {
		t.Fatalf("finishing a request did not count as activity: idle for %s", idle)
	}
}

func TestHeartbeatFromAnOpenWindowResetsTheIdleTimer(t *testing.T) {
	app := testApplication(t)
	handler, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	watch := newIdleWatch()
	routes := watch.wrap(handler.routes())
	watch.mu.Lock()
	watch.last = time.Now().Add(-2 * idleShutdownAfter)
	watch.mu.Unlock()
	if watch.idleFor() < idleShutdownAfter {
		t.Fatal("idle watch did not age")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/app/heartbeat", nil)
	request.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("heartbeat returned %d", response.Code)
	}
	if idle := watch.idleFor(); idle > time.Second {
		t.Fatalf("an open window would still be closed: idle for %s", idle)
	}
}

func TestEveryOpenWindowSendsAHeartbeat(t *testing.T) {
	for _, name := range []string{"web/app.js", "web/practice.html"} {
		page, err := embeddedFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(page, []byte("/api/app/heartbeat")) {
			t.Errorf("%s never reports that its window is open, so a long session would be closed", name)
		}
	}
}
