// Package fsutil holds the file and naming helpers the stores share.
package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a name into a lowercase, file-safe id part of at most 40 characters.
func Slug(name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "item"
	}
	return s
}

// NewID returns the slug of name plus a random 6 hex digit suffix.
func NewID(name string) string {
	b := make([]byte, 3)
	rand.Read(b)
	return Slug(name) + "-" + hex.EncodeToString(b)
}

// WriteFile writes data to path through a temporary file, so readers never see a partial file.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteJSON marshals v (indented) and writes it atomically.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, append(b, '\n'))
}

// ReadJSON decodes the JSON file at path into v.
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
