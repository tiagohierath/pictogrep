package main

import (
	"embed"
)

//go:embed web/*
var embeddedFiles embed.FS

func storyboardHTML() ([]byte, error) {
	return embeddedFiles.ReadFile("web/practice.html")
}
