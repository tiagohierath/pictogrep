package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsageTrackerCreatesStableAnonymousInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "usage.json")
	now := func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local) }
	first, err := newUsageTrackerWithOptions(path, "", "0.9.0", "linux/amd64", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	firstState := first.snapshot()
	first.close()
	second, err := newUsageTrackerWithOptions(path, "", "0.9.0", "linux/amd64", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	secondState := second.snapshot()
	if firstState.InstallationID == "" || firstState.InstallationID != secondState.InstallationID {
		t.Fatalf("installation ID was not stable: first=%q second=%q", firstState.InstallationID, secondState.InstallationID)
	}
	if firstState.InstallationCreated != "2026-08-23" {
		t.Fatalf("installation creation date = %q", firstState.InstallationCreated)
	}
	if firstState.LastActiveReport != "" || len(firstState.PendingActiveDates) != 0 {
		t.Fatalf("launch alone recorded activity: %+v", firstState)
	}
}

func TestUsageTrackerQueuesOfflineAndMarksOnlyAfterSuccess(t *testing.T) {
	var attempts atomic.Int32
	var succeed atomic.Bool
	received := make(chan activeDayEvent, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		var event activeDayEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("decode event: %v", err)
		}
		received <- event
		if !succeed.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "usage.json")
	now := func() time.Time { return time.Date(2026, 8, 23, 23, 0, 0, 0, time.Local) }
	tracker, err := newUsageTrackerWithOptions(path, server.URL, "0.9.0", "windows/amd64", server.Client(), now)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.close()
	tracker.markActive()

	select {
	case event := <-received:
		if event.Date != "2026-08-23" || event.AppVersion != "0.9.0" || event.Platform != "windows/amd64" || event.InstallationID == "" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("offline send was not attempted")
	}
	state := tracker.snapshot()
	if state.LastActiveReport != "" || !containsDate(state.PendingActiveDates, "2026-08-23") {
		t.Fatalf("failed report was marked successful: %+v", state)
	}

	succeed.Store(true)
	tracker.wakeFlush()
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("queued report was not retried")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		state = tracker.snapshot()
		if state.LastActiveReport == "2026-08-23" && len(state.PendingActiveDates) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("successful report was not committed: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := attempts.Load()
	tracker.markActive()
	time.Sleep(50 * time.Millisecond)
	if attempts.Load() != before {
		t.Fatal("same active day was reported twice")
	}
}
