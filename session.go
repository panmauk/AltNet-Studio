package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Session struct {
	Token     string `json:"token"`
	Email     string `json:"email"`
	IsAdmin   bool   `json:"is_admin"`
	ExpiresAt int64  `json:"expires_at"`
}

func (s *Session) Expired() bool {
	return s == nil || time.Now().Unix() >= s.ExpiresAt
}

// sessionPath returns the file we use to remember a logged-in user
// across app restarts. We keep it in the user's per-OS config dir so
// it travels with their profile.
func sessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "AltNet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

func loadSession() (*Session, error) {
	p, err := sessionPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSession(s *Session) error {
	p, err := sessionPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func clearSession() error {
	p, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
