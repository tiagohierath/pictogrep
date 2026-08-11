package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type canvasPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type storedCanvasLayout struct {
	Scope     string                 `json:"scope"`
	Positions map[string]canvasPoint `json:"positions"`
	UpdatedAt int64                  `json:"updatedAt"`
}

func (a *application) canvasLayoutPath(scope string) string {
	key := sha256.Sum256([]byte(scope))
	return filepath.Join(a.canvasDir, "canvas-"+hashHex(key[:])+".json")
}

func hashHex(value []byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, item := range value {
		encoded[index*2] = digits[item>>4]
		encoded[index*2+1] = digits[item&15]
	}
	return string(encoded)
}

func (a *application) loadCanvasLayout(scope string) (map[string]canvasPoint, error) {
	a.canvasMu.Lock()
	defer a.canvasMu.Unlock()
	data, err := os.ReadFile(a.canvasLayoutPath(scope))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]canvasPoint{}, nil
	}
	if err != nil {
		return nil, err
	}
	var stored storedCanvasLayout
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	if stored.Scope != scope || stored.Positions == nil {
		return map[string]canvasPoint{}, nil
	}
	return stored.Positions, nil
}

func (a *application) saveCanvasLayout(scope string, positions map[string]canvasPoint) error {
	a.canvasMu.Lock()
	defer a.canvasMu.Unlock()
	stored := storedCanvasLayout{Scope: scope, Positions: positions, UpdatedAt: time.Now().Unix()}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	target := a.canvasLayoutPath(scope)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, target)
}
