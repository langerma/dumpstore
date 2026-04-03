package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Duration is a time.Duration that marshals/unmarshals as a human-readable string
// (e.g. "24h", "30m") in JSON.
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = v
	return nil
}

// Config holds all authentication configuration persisted to disk.
type Config struct {
	Username          string   `json:"username"`
	PasswordHash      string   `json:"password_hash"`
	SessionTTL        Duration `json:"session_ttl"`
	TrustedProxies    []string `json:"trusted_proxies"`
	UnprotectedPaths  []string `json:"unprotected_paths"`
}

// defaults fills zero-value fields with sensible defaults.
func (c *Config) defaults() {
	if c.Username == "" {
		c.Username = "admin"
	}
	if c.SessionTTL.Duration == 0 {
		c.SessionTTL = Duration{24 * time.Hour}
	}
	if len(c.UnprotectedPaths) == 0 {
		c.UnprotectedPaths = []string{"/metrics"}
	}
}

// LoadConfig reads the config file at path. If the file does not exist, a
// default Config is returned (no error). Other I/O errors are returned.
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := &Config{}
		cfg.defaults()
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.defaults()
	return &cfg, nil
}

// SaveConfig writes cfg to path atomically (temp file + rename).
// The directory is created with mode 0700 if it does not exist.
// The file is written with mode 0600.
func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode config: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}
