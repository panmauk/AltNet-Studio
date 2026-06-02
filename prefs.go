package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// AppPrefs is the small chunk of state we persist between launches so
// the app remembers "I'm a node" and "I want to launch with Windows."
type AppPrefs struct {
	IsNode          bool `json:"is_node"`
	AutoStartWithOS bool `json:"auto_start_with_os"`

	// SeedPeers is the user-curated list of always-on AltNet nodes
	// to dial on every daemon launch (host:port). Without at least
	// one entry, a fresh install has nothing to bootstrap from and
	// stays alone forever. Friends sharing the same list end up on
	// the same DHT.
	SeedPeers []string `json:"seed_peers,omitempty"`

	// HostingTermsAcceptedAt is the unix timestamp at which the user
	// clicked "I understand" on the be-a-node legal disclaimer. Zero
	// means they have not accepted yet. We re-show the disclaimer
	// whenever this is zero AND they try to enable node mode, then
	// stamp the timestamp on acceptance. We keep it as a unix int (not
	// a bool) so we have an audit trail — useful for any later legal
	// inquiry about who accepted what, when.
	HostingTermsAcceptedAt int64 `json:"hosting_terms_accepted_at,omitempty"`
}

func prefsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "AltNet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "prefs.json"), nil
}

func loadPrefs() *AppPrefs {
	p, err := prefsPath()
	if err != nil {
		return &AppPrefs{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &AppPrefs{}
		}
		return &AppPrefs{}
	}
	var out AppPrefs
	if err := json.Unmarshal(b, &out); err != nil {
		return &AppPrefs{}
	}
	return &out
}

func savePrefs(p *AppPrefs) error {
	path, err := prefsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
