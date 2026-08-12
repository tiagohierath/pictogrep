package main

import (
	"embed"
)

//go:embed web/* assets/pictogrep.png LICENSE third_party/*/*
var embeddedFiles embed.FS

func storyboardHTML() ([]byte, error) {
	return embeddedFiles.ReadFile("web/practice.html")
}
