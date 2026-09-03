package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Coverage for licensing (license.go) and for the one gate it feeds:
// whether a paid installed plugin will serve anything.
//
// The product promise being defended here is narrower than "the signature
// check works" and much more important: an install that is unlocked stays
// unlocked, whatever anyone later hands it. Several tests below exist only to
// fail if that ever stops being true.

// useTestLicenseKey swaps in a keypair generated for this test and returns the
// private half so the test can issue licenses. The shipped placeholder key has
// no private half by design (see license.go), so tests must bring their own.
func useTestLicenseKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previous := licensePublicKey
	licensePublicKey = public
	t.Cleanup(func() { licensePublicKey = previous })
	return private
}

// issueLicense signs claims exactly as an issuer would: over the raw claim
// bytes, not over a re-encoding of a parsed struct. Taking the JSON as text is
// what lets a test sign a payload no Go struct would produce.
func issueLicense(key ed25519.PrivateKey, claims string) string {
	return licenseTokenPrefix +
		base64.RawURLEncoding.EncodeToString([]byte(claims)) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(claims)))
}

func writeTestPlugin(t *testing.T, app *application, folder, id string, paid bool) {
	t.Helper()
	directory := filepath.Join(app.pluginsDir, folder)
	if err := os.MkdirAll(filepath.Join(directory, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"id": id, "name": folder, "version": "1.0.0", "apiVersion": "0",
		"entry": "ui/index.html", "permissions": []string{"images.list", "storage.kv", "ui.panel"},
	}
	if paid {
		manifest["paid"] = true
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ui", "index.html"), []byte(folder+" panel"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.reloadPlugins()
}

func postLicense(handler http.Handler, token string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"license": token})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/app/license", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestValidLicenseVerifiesAndSurvivesRestart(t *testing.T) {
	app := testApplication(t)
	key := useTestLicenseKey(t)
	token := issueLicense(key, `{"buyer":"nlw-2f9c41","issued":"2026-09-03","tier":"navylilyworks"}`)

	claims, err := verifyLicense(token, licensePublicKey)
	if err != nil {
		t.Fatalf("a license signed by the shipped key did not verify: %v", err)
	}
	if claims.Buyer != "nlw-2f9c41" || claims.Issued != "2026-09-03" || claims.Tier != "navylilyworks" {
		t.Fatalf("claims did not survive the round trip: %#v", claims)
	}
	if app.pluginsUnlocked() {
		t.Fatal("a fresh install started out unlocked")
	}
	if _, err := app.importLicense(token); err != nil {
		t.Fatal(err)
	}
	if !app.pluginsUnlocked() {
		t.Fatal("importing a valid license did not unlock plugins")
	}

	// The unlock is state on disk, not state in this process.
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.pluginsUnlocked() {
		t.Fatal("the unlock did not survive a restart")
	}
	stored := reloaded.installedPluginSettings()
	if stored.License == nil || stored.License.Tier != "navylilyworks" || stored.License.ImportedAt == 0 {
		t.Fatalf("the license record was not written down: %#v", stored.License)
	}
}

// The entitlement lives under its own config key, beside the unrelated bool map
// that switches compile-time features, and importing a license must not
// disturb anything else in the file.
func TestLicenseIsStoredUnderInstalledPluginsWithoutTouchingOtherSettings(t *testing.T) {
	app := testApplication(t)
	key := useTestLicenseKey(t)
	if err := app.setPluginEnabled("commandPalette", true); err != nil {
		t.Fatal(err)
	}
	if err := app.saveTheme("light"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.importLicense(issueLicense(key, `{"buyer":"b","issued":"2026-09-03","tier":"mobile"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Theme            string          `json:"theme"`
		Plugins          map[string]bool `json:"plugins"`
		InstalledPlugins struct {
			Unlocked bool `json:"unlocked"`
			License  struct {
				Tier string `json:"tier"`
			} `json:"license"`
		} `json:"installedPlugins"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if !document.InstalledPlugins.Unlocked || document.InstalledPlugins.License.Tier != "mobile" {
		t.Fatalf("license did not land under installedPlugins: %s", data)
	}
	if !document.Plugins["commandPalette"] || document.Theme != "light" {
		t.Fatalf("importing a license disturbed other settings: %s", data)
	}
}

func TestTamperedOrGarbageLicenseIsRejected(t *testing.T) {
	key := useTestLicenseKey(t)
	valid := issueLicense(key, `{"buyer":"b","issued":"2026-09-03","tier":"navylilyworks"}`)
	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Re-sign different claims with a different key: the signature is
	// internally consistent and still has to be refused.
	forged := issueLicense(otherKey, `{"buyer":"b","issued":"2026-09-03","tier":"navylilyworks"}`)
	// Same signature, edited claims: the payload says a different buyer than
	// the one that was signed.
	edited := licenseTokenPrefix +
		base64.RawURLEncoding.EncodeToString([]byte(`{"buyer":"someone-else","issued":"2026-09-03","tier":"navylilyworks"}`)) + "." +
		strings.SplitN(strings.TrimPrefix(valid, licenseTokenPrefix), ".", 2)[1]

	for name, token := range map[string]string{
		"empty":              "",
		"garbage":            "hello",
		"missing prefix":     strings.TrimPrefix(valid, licenseTokenPrefix),
		"missing signature":  licenseTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":"navylilyworks"}`)),
		"unreadable base64":  licenseTokenPrefix + "not base64!.also not base64!",
		"truncated payload":  valid[:len(valid)-4],
		"signed by another":  forged,
		"claims edited":      edited,
		"signed but no tier": issueLicense(key, `{"buyer":"b","issued":"2026-09-03"}`),
		"not json":           issueLicense(key, `this is not json`),
	} {
		if _, err := verifyLicense(token, licensePublicKey); err == nil {
			t.Fatalf("%s license was accepted", name)
		}
		app := testApplication(t)
		if _, err := app.importLicense(token); err == nil {
			t.Fatalf("%s license was imported", name)
		}
		if app.pluginsUnlocked() {
			t.Fatalf("%s license unlocked plugins", name)
		}
	}
}

// A build whose shipped key is missing or mistyped must verify nothing rather
// than everything, and must not panic on the way (crypto/ed25519 panics on a
// wrong-sized key, so this is a real hazard and not a hypothetical one).
func TestUnusableShippedKeyVerifiesNothing(t *testing.T) {
	if decodeLicensePublicKey("not a key") != nil {
		t.Fatal("a malformed key decoded to something usable")
	}
	if got := decodeLicensePublicKey(shippedLicensePublicKey); len(got) != ed25519.PublicKeySize {
		t.Fatalf("the shipped placeholder key is not a usable Ed25519 key: %d bytes", len(got))
	}
	key := useTestLicenseKey(t)
	token := issueLicense(key, `{"buyer":"b","issued":"2026-09-03","tier":"navylilyworks"}`)
	if _, err := verifyLicense(token, nil); err == nil {
		t.Fatal("a build with no key accepted a license")
	}
	if _, err := verifyLicense(token, ed25519.PublicKey("short")); err == nil {
		t.Fatal("a build with a truncated key accepted a license")
	}
}

func TestPaidPluginIsUnreachableUntilLicensed(t *testing.T) {
	app := testApplication(t)
	key := useTestLicenseKey(t)
	writeTestPlugin(t, app, "find-me", "dev.navylily.findme", true)
	writeTestPlugin(t, app, "room-view", "dev.navylily.roomview", false)
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()

	locked := pluginRequest(handler, "/plugin/dev.navylily.findme/ui/")
	if locked.Code != http.StatusPaymentRequired {
		t.Fatalf("an unlicensed install opened a paid plugin: status=%d body=%s", locked.Code, locked.Body.String())
	}
	storage := pluginRequest(handler, "/api/plugins/dev.navylily.findme/storage")
	if storage.Code != http.StatusPaymentRequired {
		t.Fatalf("an unlicensed install reached a paid plugin's storage: status=%d", storage.Code)
	}
	free := pluginRequest(handler, "/plugin/dev.navylily.roomview/ui/")
	if free.Code != http.StatusOK {
		t.Fatalf("a free plugin was caught by the paid gate: status=%d body=%s", free.Code, free.Body.String())
	}

	// Locked plugins stay listed, and say so, rather than disappearing.
	listed := installedPluginsByID(t, handler)
	if !listed["dev.navylily.findme"].Paid || !listed["dev.navylily.findme"].Locked {
		t.Fatalf("the paid plugin was not reported as locked: %#v", listed["dev.navylily.findme"])
	}
	if listed["dev.navylily.roomview"].Paid || listed["dev.navylily.roomview"].Locked {
		t.Fatalf("a free plugin was reported as paid or locked: %#v", listed["dev.navylily.roomview"])
	}

	token := issueLicense(key, `{"buyer":"nlw-2f9c41","issued":"2026-09-03","tier":"navylilyworks"}`)
	imported := postLicense(handler, token)
	if imported.Code != http.StatusOK {
		t.Fatalf("importing a valid license failed: status=%d body=%s", imported.Code, imported.Body.String())
	}

	// No restart, no reload: the same running server serves the plugin now.
	opened := pluginRequest(handler, "/plugin/dev.navylily.findme/ui/")
	if opened.Code != http.StatusOK {
		t.Fatalf("a licensed install could not open a paid plugin: status=%d body=%s", opened.Code, opened.Body.String())
	}
	if body := opened.Body.String(); !strings.Contains(body, "find-me panel") {
		t.Fatalf("the paid plugin served something unexpected: %q", body)
	}
	if storage := pluginRequest(handler, "/api/plugins/dev.navylily.findme/storage"); storage.Code != http.StatusOK {
		t.Fatalf("a licensed paid plugin could not reach its storage: status=%d", storage.Code)
	}
	if listed := installedPluginsByID(t, handler); listed["dev.navylily.findme"].Locked {
		t.Fatal("the paid plugin is still reported as locked after importing a license")
	}
}

// The gate reads the manifest and nothing else. A paid plugin nobody here
// wrote is treated exactly like ours, in both directions: refused without a
// license, served with one. If anyone ever adds an id allowlist, this is what
// notices.
func TestPaidGateDoesNotSpecialCaseFirstPartyPlugins(t *testing.T) {
	app := testApplication(t)
	key := useTestLicenseKey(t)
	writeTestPlugin(t, app, "find-me", "dev.navylily.findme", true)
	writeTestPlugin(t, app, "stranger", "com.example.stranger", true)
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	for _, id := range []string{"dev.navylily.findme", "com.example.stranger"} {
		if response := pluginRequest(handler, "/plugin/"+id+"/ui/"); response.Code != http.StatusPaymentRequired {
			t.Fatalf("%s was not gated without a license: status=%d", id, response.Code)
		}
	}
	if _, err := app.importLicense(issueLicense(key, `{"buyer":"b","issued":"2026-09-03","tier":"navylilyworks"}`)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dev.navylily.findme", "com.example.stranger"} {
		if response := pluginRequest(handler, "/plugin/"+id+"/ui/"); response.Code != http.StatusOK {
			t.Fatalf("%s stayed locked after a license was imported: status=%d", id, response.Code)
		}
	}
}

// The product promise, in test form. Once a machine is unlocked, nothing short
// of the user deleting their config may lock it again: not an old issue date,
// not a license that fails to verify, not a rotated signing key, not a build
// that ships a different key entirely. Each case below has broken a licensing
// system somewhere.
func TestUnlockedInstallStaysUnlockedForever(t *testing.T) {
	app := testApplication(t)
	key := useTestLicenseKey(t)
	writeTestPlugin(t, app, "find-me", "dev.navylily.findme", true)
	server, err := newServer(app)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	if imported := postLicense(handler, issueLicense(key, `{"buyer":"b","issued":"2026-09-03","tier":"navylilyworks"}`)); imported.Code != http.StatusOK {
		t.Fatalf("importing a valid license failed: status=%d body=%s", imported.Code, imported.Body.String())
	}

	stillOpen := func(t *testing.T, when string) {
		t.Helper()
		if !app.pluginsUnlocked() {
			t.Fatalf("the install lost its unlock %s", when)
		}
		if response := pluginRequest(handler, "/plugin/dev.navylily.findme/ui/"); response.Code != http.StatusOK {
			t.Fatalf("a paid plugin stopped opening %s: status=%d", when, response.Code)
		}
	}

	// A license issued years ago, carrying a field that looks exactly like an
	// expiry, and one dated in the past. No clock is consulted, so both are
	// ordinary licenses and both import fine.
	expiredLooking := issueLicense(key, `{"buyer":"b","issued":"2019-01-01","tier":"navylilyworks","expires":"2020-01-01"}`)
	if _, err := verifyLicense(expiredLooking, licensePublicKey); err != nil {
		t.Fatalf("a license with an old date and an expires field was refused: %v", err)
	}
	if imported := postLicense(handler, expiredLooking); imported.Code != http.StatusOK {
		t.Fatalf("importing an old license failed: status=%d body=%s", imported.Code, imported.Body.String())
	}
	stillOpen(t, "after importing an expired-looking license")

	// A garbage import is refused, and refusing it changes nothing.
	refused := postLicense(handler, "pictogrep-license-v1.not-a-license")
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("a garbage license was accepted: status=%d", refused.Code)
	}
	var refusal struct {
		Unlocked bool `json:"unlocked"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	if !refusal.Unlocked {
		t.Fatal("refusing a bad license reported the install as locked")
	}
	stillOpen(t, "after a garbage license was refused")

	// The signing key rotates, or this build simply ships a different one.
	// Licenses signed by the old key would no longer verify, which is exactly
	// why nothing re-verifies the one already imported.
	rotated, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	licensePublicKey = rotated
	stillOpen(t, "after the signing key rotated")

	// Even a build that ships no usable key at all keeps serving what was
	// already paid for.
	licensePublicKey = nil
	stillOpen(t, "in a build with no usable license key")

	// And it survives a restart in that state, because the answer on disk is a
	// boolean and not a license to re-check.
	reloaded, err := newApplication()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.pluginsUnlocked() {
		t.Fatal("the unlock did not survive a restart with no usable license key")
	}
}

// The phone gate over the compile-time features asks the same entitlement as
// the installed plugins do. Only the phone build locks anything, and the two
// free features stay free either way; a desktop is never gated by this at all.
func TestPhoneFeatureGateFollowsTheSameUnlock(t *testing.T) {
	app := testApplication(t)
	key := useTestLicenseKey(t)
	if !runsOnPhone {
		if app.lockedOnPhone("canvas") {
			t.Fatal("a desktop build locked a built-in feature")
		}
		return
	}
	if !app.lockedOnPhone("canvas") || app.lockedOnPhone("web") || app.lockedOnPhone("calendar") {
		t.Fatal("the phone gate is not locking exactly the non-free features")
	}
	if _, err := app.importLicense(issueLicense(key, `{"buyer":"b","issued":"2026-09-03","tier":"mobile"}`)); err != nil {
		t.Fatal(err)
	}
	if app.lockedOnPhone("canvas") {
		t.Fatal("a licensed phone is still locked out of a built-in feature")
	}
}

type installedPluginRow struct {
	ID     string `json:"id"`
	Paid   bool   `json:"paid"`
	Locked bool   `json:"locked"`
}

func installedPluginsByID(t *testing.T, handler http.Handler) map[string]installedPluginRow {
	t.Helper()
	response := pluginRequest(handler, "/api/app/plugins/installed")
	if response.Code != http.StatusOK {
		t.Fatalf("installed plugins status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Plugins []installedPluginRow `json:"plugins"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	rows := map[string]installedPluginRow{}
	for _, row := range payload.Plugins {
		rows[row.ID] = row
	}
	return rows
}
