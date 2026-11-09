package smb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Usershare is one share file in the dumpstore-managed usershares directory.
type Usershare struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	PathMissing bool   `json:"path_missing"` // shared path no longer exists — Samba hides the share until it does
}

// ListUsershares reads the usershare definitions directly from dir. Unlike
// `net usershare info`, it also returns shares whose path is gone (#183):
// Samba skips those silently but re-activates them as soon as the path
// exists again. A missing dir yields no shares; other read errors are
// returned. The result is sorted by name and never nil.
func ListUsershares(dir string) ([]Usershare, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Usershare{}, nil
		}
		return nil, fmt.Errorf("read usershare dir: %w", err)
	}
	shares := []Usershare{}
	for _, e := range entries {
		// `net usershare add` writes "<dir>/:tmpXXXXXX" and renames it.
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ":") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // deleted between ReadDir and ReadFile
			}
			return nil, fmt.Errorf("read usershare %s: %w", e.Name(), err)
		}
		s := parseUsershare(e.Name(), data)
		if s.Path != "" {
			if _, err := os.Stat(s.Path); errors.Is(err, fs.ErrNotExist) {
				s.PathMissing = true
			}
		}
		shares = append(shares, s)
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].Name < shares[j].Name })
	return shares, nil
}

// parseUsershare parses a usershare file. Version 2 files carry the original
// share name in `sharename=`; version 1 files only have the (lower-cased)
// file name.
func parseUsershare(filename string, data []byte) Usershare {
	s := Usershare{Name: filename}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "path":
			s.Path = val
		case "sharename":
			if val != "" {
				s.Name = val
			}
		}
	}
	return s
}
