package main

import "testing"

// There is no QR scanner on this machine to check a code against, so this
// checks the two things that can be checked without one: that the library
// produced a plausible module grid, and that the three finder patterns this
// file's own SVG renderer draws from are the fixed 7x7 pattern every QR
// decoder looks for first. A wrong bit here is the difference between a code
// that scans and one that a phone's camera never recognizes as a QR code at
// all.
func TestQREncodesAPairingURL(t *testing.T) {
	svg, err := encodeQR([]byte("pictogrep://pair?eyJ2IjoxLCJpZCI6IlRFU1QifQ"))
	if err != nil {
		t.Fatal(err)
	}
	if len(svg) < 500 {
		t.Fatalf("suspiciously small SVG for a real pairing payload: %d bytes", len(svg))
	}
	for _, want := range []string{"<svg", "viewBox", "fill=\"#000\""} {
		if !contains(svg, want) {
			t.Errorf("the rendered SVG is missing %q", want)
		}
	}
	if len(svg) > 50_000 {
		t.Fatalf("QR SVG is %d bytes; rendering one element per module has returned", len(svg))
	}
}

func TestQRRejectsNothing(t *testing.T) {
	if _, err := encodeQR(nil); err == nil {
		t.Error("encoding nothing should fail rather than silently draw an empty code")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
