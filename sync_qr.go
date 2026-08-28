package main

// QR rendering for the pairing screen.
//
// Encoding is handed to github.com/skip2/go-qrcode rather than hand-rolled:
// this file used to carry a from-scratch encoder, and Reed-Solomon block
// splitting and the exact zigzag placement rules are precisely the kind of
// thing worth not getting subtly wrong with no scanner on hand to check
// against. Only the bitmap this library produces is used; rendering it as an
// SVG here keeps output consistent with the rest of the app, which never
// ships a PNG.

import (
	"bytes"
	"encoding/base64"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// urlSafeBase64 is how a pairing session's JSON rides inside the
// pictogrep://pair? URL a QR carries, which keeps the code openable by a
// phone's ordinary camera app as a link, and not only by Pictogrep's own
// scanner.
func urlSafeBase64(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// encodeQR renders data as a self-contained square SVG QR code.
func encodeQR(data []byte) (string, error) {
	code, err := qrcode.New(string(data), qrcode.Medium)
	if err != nil {
		return "", err
	}
	bitmap := code.Bitmap()
	return qrSVG(bitmap), nil
}

// qrSVG draws a QR bitmap (true = dark module) as SVG plus the quiet zone most
// scanners assume rather than discover. Adjacent dark modules are one path
// segment instead of one <rect> each: a real pairing code has thousands of
// modules, and handing the browser a 160 KB DOM tree made opening Connect phone
// visibly lag on slower machines.
func qrSVG(bitmap [][]bool) string {
	size := len(bitmap)
	const quiet = 4
	total := size + quiet*2

	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, total, total)
	out.WriteString(`<rect width="100%" height="100%" fill="#fff"/>`)
	out.WriteString(`<path fill="#000" d="`)
	for r, row := range bitmap {
		for start := 0; start < len(row); {
			for start < len(row) && !row[start] {
				start++
			}
			end := start
			for end < len(row) && row[end] {
				end++
			}
			if end > start {
				fmt.Fprintf(&out, "M%d %dh%dv1H%dz", start+quiet, r+quiet, end-start, start+quiet)
			}
			start = end
		}
	}
	out.WriteString(`"/></svg>`)
	return out.String()
}
