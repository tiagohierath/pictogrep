package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultUsageEndpoint = "https://navylily.tv/api/pictogrep/active-day"

type usageState struct {
	InstallationID      string   `json:"installation_id"`
	InstallationCreated string   `json:"installation_created_date"`
	LastActiveReport    string   `json:"last_active_report,omitempty"`
	PendingActiveDates  []string `json:"pending_active_dates,omitempty"`
}

type activeDayEvent struct {
	InstallationID string `json:"installation_id"`
	Date           string `json:"date"`
	AppVersion     string `json:"app_version"`
	Platform       string `json:"platform"`
}

// usageTracker owns a tiny anonymous state file and sends at most one event per
// local calendar day. Meaningful actions call markActive through the local Go
// server; no remote request ever runs on the browser or request goroutine.
type usageTracker struct {
	path     string
	endpoint string
	version  string
	platform string
	client   *http.Client
	now      func() time.Time

	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	flushMu  sync.Mutex
	stateMu  sync.Mutex
	state    usageState
}

func newUsageTracker(path, appVersion string) (*usageTracker, error) {
	return newUsageTrackerWithOptions(
		path,
		defaultUsageEndpoint,
		appVersion,
		runtime.GOOS+"/"+runtime.GOARCH,
		&http.Client{Timeout: 5 * time.Second},
		time.Now,
	)
}

func newUsageTrackerWithOptions(path, endpoint, appVersion, platform string, client *http.Client, now func() time.Time) (*usageTracker, error) {
	state, err := loadOrCreateUsageState(path, now())
	if err != nil {
		return nil, err
	}
	tracker := &usageTracker{
		path: path, endpoint: endpoint, version: appVersion, platform: platform,
		client: client, now: now, state: state,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go tracker.run()
	return tracker, nil
}

func loadOrCreateUsageState(path string, now time.Time) (usageState, error) {
	var state usageState
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &state) == nil && state.InstallationID != "" && state.InstallationCreated != "" {
			state.PendingActiveDates = uniqueDates(state.PendingActiveDates)
			return state, nil
		}
	} else if !os.IsNotExist(err) {
		return usageState{}, err
	}
	id, err := randomUUID()
	if err != nil {
		return usageState{}, err
	}
	state = usageState{InstallationID: id, InstallationCreated: now.Format("2006-01-02")}
	if err := saveUsageState(path, state); err != nil {
		return usageState{}, err
	}
	return state, nil
}

func randomUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40 // UUID version 4
	raw[8] = raw[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func saveUsageState(path string, state usageState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func uniqueDates(dates []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(dates))
	for _, date := range dates {
		if len(date) == 10 && !seen[date] {
			seen[date] = true
			out = append(out, date)
		}
	}
	sort.Strings(out)
	return out
}

// markActive persists today's pending event before waking the sender. It only
// writes a tiny local JSON file; callers never wait for internet or analytics.
func (t *usageTracker) markActive() {
	today := t.now().Format("2006-01-02")
	t.stateMu.Lock()
	if t.state.LastActiveReport == today || containsDate(t.state.PendingActiveDates, today) {
		t.stateMu.Unlock()
		return
	}
	next := t.state
	next.PendingActiveDates = uniqueDates(append(append([]string{}, next.PendingActiveDates...), today))
	if err := saveUsageState(t.path, next); err == nil {
		t.state = next
	}
	t.stateMu.Unlock()
	t.wakeFlush()
}

func containsDate(dates []string, target string) bool {
	for _, date := range dates {
		if date == target {
			return true
		}
	}
	return false
}

func (t *usageTracker) wakeFlush() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *usageTracker) run() {
	defer close(t.done)
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	// Retry dates saved by an earlier offline run, but do not create today's
	// event merely because the application launched.
	t.flush()
	for {
		select {
		case <-t.wake:
			t.flush()
		case <-ticker.C:
			t.flush()
		case <-t.stop:
			return
		}
	}
}

func (t *usageTracker) flush() {
	if strings.TrimSpace(t.endpoint) == "" || t.client == nil {
		return
	}
	t.flushMu.Lock()
	defer t.flushMu.Unlock()

	t.stateMu.Lock()
	dates := append([]string{}, t.state.PendingActiveDates...)
	t.stateMu.Unlock()
	for _, date := range dates {
		if err := t.post(date); err != nil {
			return
		}
		t.stateMu.Lock()
		next := t.state
		next.PendingActiveDates = removeDate(next.PendingActiveDates, date)
		if next.LastActiveReport == "" || date > next.LastActiveReport {
			next.LastActiveReport = date
		}
		if err := saveUsageState(t.path, next); err == nil {
			t.state = next
		}
		t.stateMu.Unlock()
	}
}

func (t *usageTracker) post(date string) error {
	event := activeDayEvent{
		InstallationID: t.installationID(), Date: date,
		AppVersion: t.version, Platform: t.platform,
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pictogrep/"+t.version)
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("active day: status %d", resp.StatusCode)
	}
	return nil
}

func (t *usageTracker) installationID() string {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.state.InstallationID
}

func removeDate(dates []string, target string) []string {
	out := make([]string, 0, len(dates))
	for _, date := range dates {
		if date != target {
			out = append(out, date)
		}
	}
	return out
}

func (t *usageTracker) snapshot() usageState {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	state := t.state
	state.PendingActiveDates = append([]string{}, state.PendingActiveDates...)
	return state
}

func (t *usageTracker) close() {
	t.stopOnce.Do(func() { close(t.stop) })
	<-t.done
}
