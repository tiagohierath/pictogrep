package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessTokenGuardsTheAPI(t *testing.T) {
	original := accessToken
	accessToken = "secret-of-this-launch"
	t.Cleanup(func() { accessToken = original })

	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name   string
		cookie string
		header string
		status int
	}{
		{name: "neighbouring app", status: http.StatusForbidden},
		{name: "wrong secret", cookie: "guessed", status: http.StatusForbidden},
		{name: "the app's own window", cookie: "secret-of-this-launch", status: http.StatusNoContent},
		{name: "the native share handler", header: "secret-of-this-launch", status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/app/state", nil)
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: accessTokenCookie, Value: test.cookie})
			}
			if test.header != "" {
				request.Header.Set("X-Pictogrep-Token", test.header)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
	}
}

// Every desktop launch runs without a token, and nothing may start asking for one.
func TestNoTokenMeansNoGuard(t *testing.T) {
	original := accessToken
	accessToken = ""
	t.Cleanup(func() { accessToken = original })

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/app/state", nil)
	if !hasAccessToken(request) {
		t.Fatal("a plain desktop request was rejected")
	}
}
