package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// On a desktop, "only accepts local requests" means "only this machine", and
// the machine is the user's. On Android it means "only this phone", and a phone
// is full of other apps: any one of them holding INTERNET can open the loopback
// port and read the whole library through the API. Loopback is a boundary
// between computers, never between apps.
//
// PICTOGREP_TOKEN closes that. The shell invents a fresh secret per launch and
// hands it to the WebView, so the library is readable by the one page that was
// given the secret and by nothing else on the device. Unset, which is every
// desktop launch, none of this is in the way.
var accessToken = strings.TrimSpace(os.Getenv("PICTOGREP_TOKEN"))

const accessTokenCookie = "pictogrep_token"

// A cookie rather than a header because most requests are not fetch() calls:
// every thumbnail is an <img src>, and a subresource load cannot be given a
// header. The header form stays for the native share handler, which posts
// uploads without a WebView anywhere in the picture.
func hasAccessToken(r *http.Request) bool {
	if accessToken == "" {
		return true
	}
	if cookie, err := r.Cookie(accessTokenCookie); err == nil && matchesAccessToken(cookie.Value) {
		return true
	}
	return matchesAccessToken(r.Header.Get("X-Pictogrep-Token"))
}

func matchesAccessToken(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(accessToken)) == 1
}

func rejectUntrustedCaller(w http.ResponseWriter) {
	sendError(w, http.StatusForbidden, fmt.Errorf("Pictogrep only accepts requests from its own window"))
}
