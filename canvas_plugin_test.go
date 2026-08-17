package main

import (
	"net/http"
	"strings"
	"testing"
)

// The folder canvas is an extra way of looking at a folder, not something every
// library needs, so it stays off until it is asked for.
func TestCanvasPluginIsOffUntilEnabled(t *testing.T) {
	app, server := testHTTPServer(t)
	if app.pluginEnabled("canvas") {
		t.Fatal("the folder canvas should be off by default")
	}
	response, err := http.Get(server.URL + "/api/app/state")
	if err != nil {
		t.Fatal(err)
	}
	value := responseJSON(t, response)
	plugins, _ := value["plugins"].(map[string]any)
	canvas, _ := plugins["canvas"].(map[string]any)
	if canvas == nil || canvas["enabled"] != false {
		t.Fatalf("state should report the canvas as off: %#v", plugins["canvas"])
	}

	enable, err := http.Post(server.URL+"/api/app/plugins", "application/json",
		strings.NewReader(`{"name":"canvas","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if enable.StatusCode != http.StatusOK {
		t.Fatalf("enabling the canvas failed: %d", enable.StatusCode)
	}
	if !app.pluginEnabled("canvas") {
		t.Fatal("the canvas did not stay enabled")
	}
}
