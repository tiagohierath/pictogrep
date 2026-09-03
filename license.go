package main

// One unlock, bought two ways, proved by one signed file.
//
// A NavyLilyWorks year at navylily.tv (desktop and web) and the one-off mobile
// purchase both produce the same artifact: a short signed document saying who
// bought it, when, and under which tier. Pictogrep ships the public half of the
// signing key, checks the signature locally, writes down the answer, and is
// done. There is no per-plugin SKU and no per-plugin key: one valid license
// unlocks every paid plugin, including ones that do not exist yet.
//
// What this deliberately does not do, and must not grow:
//
//   - No expiry. The license carries an issue date because support cases need
//     one, not because anything compares it to a clock. Cancelling, expiring,
//     or a failed card never removes a plugin from a machine that already has
//     it. See docs/plugins.md, "Access is never taken away".
//   - No phone-home, no revocation list, no device binding, no activation
//     count, no obfuscation. A license is a file the buyer owns; copying it to
//     their second machine is intended and copying it to a stranger is
//     accepted. The signature proves the license was issued, not who holds it.
//   - No re-verification. verifyLicense runs exactly once, when a license is
//     imported. After that the stored boolean is the answer, forever, and no
//     code path recomputes it. An install that is unlocked today cannot become
//     locked tomorrow by a clock, a key rotation, or a corrupted license file.
//
// That last point is the whole product promise, so treat any future change
// that re-reads the token after import as a bug rather than as hardening.
// Anyone who wants to bypass all of this can recompile the open-source core,
// and that is fine.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// A license is one line of ASCII so the same bytes work as a file, as a paste
// into a text box, and as the payload of a QR code (see docs/plugins.md for the
// phone import, which is not built yet):
//
//	pictogrep-license-v1.<base64url(claims JSON)>.<base64url(Ed25519 signature)>
//
// The signature covers the claim bytes exactly as they were encoded, not a
// re-serialization of the parsed struct, so an issuer is free to order fields
// however it likes and a future field cannot break an old build's check.
//
// Issuing one, on whichever machine holds the private key, is:
//
//	claims := []byte(`{"buyer":"nlw-2f9c41","issued":"2026-09-03","tier":"navylilyworks"}`)
//	token := "pictogrep-license-v1." +
//		base64.RawURLEncoding.EncodeToString(claims) + "." +
//		base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, claims))
const licenseTokenPrefix = "pictogrep-license-v1."

// shippedLicensePublicKey is a PLACEHOLDER, not the production key.
//
// It was generated with crypto/ed25519 and its private half was thrown away
// without ever being written down, so no license exists that this build will
// accept. That is on purpose: a committed key whose private half also exists
// somewhere is a forging key waiting to leak, and a placeholder that cannot
// sign anything fails closed instead.
//
// To ship paid plugins, replace the string below with the public half of a key
// whose private half lives wherever licenses are issued (the navylily.tv side,
// never in this repository, never in the app bundle):
//
//	public, private, _ := ed25519.GenerateKey(rand.Reader)
//	// paste base64.RawURLEncoding.EncodeToString(public) here
//	// keep `private` offline and backed up; losing it means no new licenses,
//	// though every license already issued keeps working forever
//
// Rotating this key later is safe for people who already imported a license:
// their unlock is a stored boolean and is never checked against a key again.
// Only licenses imported after the swap are verified against the new key.
const shippedLicensePublicKey = "z5iHQIF1DhxLDEzCT8c14ECHRfFpFY2jZFvdI3ErzzI"

// licensePublicKey is a variable rather than a constant so tests can sign with
// a key of their own. Nothing at runtime writes it.
var licensePublicKey = decodeLicensePublicKey(shippedLicensePublicKey)

// decodeLicensePublicKey returns nil for anything that is not a usable key, and
// verifyLicense refuses to verify with a nil key. A build with a mistyped key
// therefore accepts no licenses at all, which is the safe direction: the other
// one would be a build that accepts every license.
func decodeLicensePublicKey(encoded string) ed25519.PublicKey {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(raw)
}

// licenseClaims is everything a license says. Unknown fields are ignored, so an
// issuer can add one without stranding older builds; in particular a field
// named "expires" would be read by nobody, because nothing here expires.
type licenseClaims struct {
	Buyer string `json:"buyer"`
	// Issued is kept as the issuer wrote it and never parsed into a time.
	// Parsing it would invite comparing it to something.
	Issued string `json:"issued"`
	// Tier records which way in was bought, for support and for curiosity. It
	// is deliberately not consulted when deciding what to unlock: there is one
	// unlock, and an unrecognised tier from a newer issuer must not lock a
	// paying customer out of what they bought.
	Tier string `json:"tier"`
}

// verifyLicense is the only place a signature is ever checked, and it runs only
// at import time.
func verifyLicense(token string, key ed25519.PublicKey) (licenseClaims, error) {
	if len(key) != ed25519.PublicKeySize {
		return licenseClaims{}, fmt.Errorf("this build ships no usable license key")
	}
	// A license arrives out of a file, a text box, or a camera, and all three
	// tend to bring a trailing newline or a stray space with them. Trim the
	// outside only: anything inside the token is signed material.
	body := strings.TrimSpace(token)
	if !strings.HasPrefix(body, licenseTokenPrefix) {
		return licenseClaims{}, fmt.Errorf("this does not look like a Pictogrep license")
	}
	encodedClaims, encodedSignature, found := strings.Cut(strings.TrimPrefix(body, licenseTokenPrefix), ".")
	if !found {
		return licenseClaims{}, fmt.Errorf("license is missing its signature")
	}
	claimBytes, err := base64.RawURLEncoding.DecodeString(encodedClaims)
	if err != nil {
		return licenseClaims{}, fmt.Errorf("license is not readable: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return licenseClaims{}, fmt.Errorf("license signature is not readable")
	}
	if !ed25519.Verify(key, claimBytes, signature) {
		return licenseClaims{}, fmt.Errorf("license signature does not match")
	}
	var claims licenseClaims
	if err := json.Unmarshal(claimBytes, &claims); err != nil {
		return licenseClaims{}, fmt.Errorf("license contents are not readable: %w", err)
	}
	// The one shape check worth making. A correctly signed but empty document
	// is an issuing mistake, and recording it as an unlock would hide that
	// mistake behind a working install.
	if claims.Tier == "" {
		return licenseClaims{}, fmt.Errorf("license names no tier")
	}
	return claims, nil
}

// licenseRecord is what gets written down: the claims, plus when they were
// imported. It exists to be shown back to a person ("unlocked by NavyLilyWorks,
// issued 2026-09-03"), never to be re-evaluated.
type licenseRecord struct {
	Buyer      string `json:"buyer"`
	Issued     string `json:"issued"`
	Tier       string `json:"tier"`
	ImportedAt int64  `json:"importedAt"`
}

// installedPluginSettings is the "installedPlugins" object in the config file,
// deliberately a different key from the "plugins" bool map that switches the
// eight compile-time features on and off. Those are a feature-flag system for
// code inside this binary; this is state about code that is not. Keeping them
// apart in the file is what stops one growing into the other by accident.
type installedPluginSettings struct {
	Unlocked bool           `json:"unlocked"`
	License  *licenseRecord `json:"license,omitempty"`
}

func (a *application) installedPluginSettings() installedPluginSettings {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return installedPluginSettings{}
	}
	var document struct {
		InstalledPlugins installedPluginSettings `json:"installedPlugins"`
	}
	if json.Unmarshal(data, &document) != nil {
		return installedPluginSettings{}
	}
	return document.InstalledPlugins
}

// pluginsUnlocked reads one stored boolean. It does not verify anything, does
// not look at a date, and does not touch the network. Reading the file each
// time is how every other setting in this app behaves, and it is what makes a
// freshly imported license take effect without a restart; the important part is
// that the value read is the answer that was written at import time and is
// never recomputed from the license itself.
func (a *application) pluginsUnlocked() bool {
	return a.installedPluginSettings().Unlocked
}

// pluginLocked is the single gate for installed plugins, and it asks the
// manifest, never the id. There is no allowlist of first-party plugins here or
// anywhere else: a paid plugin written by a stranger is gated by exactly this
// line, and so is ours. See docs/plugins.md, "The rule".
func (a *application) pluginLocked(manifest pluginManifest) bool {
	return manifest.Paid && !a.pluginsUnlocked()
}

// freeOnPhone is the set of built-in features that need no unlock on a phone.
// Import from web and the calendar are the two that make an empty library
// useful on the day it is installed. This is about the compile-time feature
// flags in pluginEnabled, not about installed plugins, which are gated by
// pluginLocked above.
//
// The desktop is unaffected: it ships with these features and does not take a
// working one away to sell it back.
var freeOnPhone = map[string]bool{
	"web":      true,
	"calendar": true,
}

// lockedOnPhone answers whether a built-in feature is behind the unlock right
// now. Called by pluginEnabled, so a locked feature is off everywhere at once:
// the panel it draws, the routes it serves, and the background job it runs.
//
// This used to consult a phone-only "premium.unlocked" boolean that any caller
// could set. It now asks the same entitlement the installed plugins ask, which
// is what "two ways in, one unlock" means in practice.
func (a *application) lockedOnPhone(name string) bool {
	if !runsOnPhone || freeOnPhone[name] {
		return false
	}
	return !a.pluginsUnlocked()
}

// storeLicense records the unlock. It only ever writes true: there is no code
// path in this program that turns an unlock off, because there is no event that
// is allowed to. Sibling keys under "installedPlugins" are merged rather than
// replaced so that later per-plugin state can live beside this without a
// license import wiping it.
func (a *application) storeLicense(claims licenseClaims) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	installed, ok := document["installedPlugins"].(map[string]any)
	if !ok {
		installed = map[string]any{}
	}
	installed["unlocked"] = true
	installed["license"] = licenseRecord{
		Buyer:      claims.Buyer,
		Issued:     claims.Issued,
		Tier:       claims.Tier,
		ImportedAt: time.Now().Unix(),
	}
	document["installedPlugins"] = installed
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(a.configPath, append(data, '\n'), 0o644)
}

// importLicense verifies once and writes the answer down. A license that fails
// to verify changes nothing, which matters most on an install that is already
// unlocked: a stranger's broken paste, a truncated file, or a license signed by
// a key this build no longer ships must never take away plugins that are
// already on the machine.
func (a *application) importLicense(token string) (licenseClaims, error) {
	claims, err := verifyLicense(token, licensePublicKey)
	if err != nil {
		return licenseClaims{}, err
	}
	if err := a.storeLicense(claims); err != nil {
		return licenseClaims{}, err
	}
	return claims, nil
}

// POST /api/app/license: the one way in.
//
// This replaces POST /api/app/premium, which flipped a boolean and stood in for
// a Play Billing callback. Both ways of paying now end at the same place: the
// seller issues a signed license, the buyer brings it here, and the unlock is
// permanent from that moment. Nothing in this app can undo it.
func (s *server) importLicense(w http.ResponseWriter, r *http.Request) {
	var request struct {
		License string `json:"license"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	claims, err := s.app.importLicense(request.License)
	if err != nil {
		// Report whether this install is unlocked as well as why this
		// particular license was refused, so an interface can say "that file
		// is not a license" without implying anything was lost.
		sendJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": err.Error(), "unlocked": s.app.pluginsUnlocked(),
		})
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "unlocked": true, "license": claims})
}
