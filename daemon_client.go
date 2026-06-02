package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DaemonClient talks to the local altnet daemon's HTTP APIs (registrar
// and metrics). All addresses are localhost by convention.
type DaemonClient struct {
	RegistrarBase string // e.g. http://127.0.0.1:9090
	MetricsBase   string // e.g. http://127.0.0.1:9999
	GatewayBase   string // e.g. http://127.0.0.1:9080  (informational only)
	Token         string
	HTTP          *http.Client
}

func NewDaemonClient(cfg *DaemonConfig) *DaemonClient {
	return &DaemonClient{
		RegistrarBase: "http://" + cfg.RegistrarAddr,
		MetricsBase:   "http://" + cfg.MetricsAddr,
		GatewayBase:   "http://" + cfg.GatewayAddr,
		Token:         cfg.RegistrarToken,
		// 5 minutes: publish can chunk + store a sizeable folder
		// synchronously, and 30s was tight when the user picks many
		// files. Reads on /metrics / /api/check are still effectively
		// instant — this timeout is a ceiling, not a normal duration.
		HTTP: &http.Client{Timeout: 5 * time.Minute},
	}
}

// MetricsSnapshot mirrors the daemon's /metrics output. Fields we don't
// care about in the UI are left out.
type MetricsSnapshot struct {
	PeerID              string   `json:"peer_id"`
	ShortID             string   `json:"short_id"`
	ListenAddress       string   `json:"listen_address"`
	AdvertisedAddresses []string `json:"advertised_addresses"`
	IsPublic            bool     `json:"is_public"`
	RelayRegistrations  []string `json:"relay_registrations"`
	ConnectedPeers      int      `json:"connected_peers"`
	UniqueConns         int      `json:"unique_conns"`
	RoutingTableSize    int      `json:"routing_table_size"`
	StoreEntries        int      `json:"store_entries"`
	StoreBytes          int64    `json:"store_bytes"`
	StoreBudget         int64    `json:"store_budget"`
	UptimeSec           int64    `json:"uptime_sec"`
	NumGoroutine        int      `json:"num_goroutine"`
}

func (c *DaemonClient) Metrics() (*MetricsSnapshot, error) {
	req, _ := http.NewRequest(http.MethodGet, c.MetricsBase+"/metrics", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("metrics returned %d", resp.StatusCode)
	}
	var s MetricsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// CheckResult mirrors apps/registrar.CheckResponse.
type CheckResult struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Owner     string `json:"owner,omitempty"`
	Root      string `json:"root,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (c *DaemonClient) Check(name string) (*CheckResult, error) {
	req, _ := http.NewRequest(http.MethodGet,
		c.RegistrarBase+"/api/check/"+url.PathEscape(name), nil)
	var out CheckResult
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DaemonPublishResult mirrors apps/registrar.PublishResponse.
type DaemonPublishResult struct {
	Root       string `json:"root"`
	EntryCount int    `json:"entry_count"`
	Error      string `json:"error,omitempty"`
}

func (c *DaemonClient) Publish(path string) (*DaemonPublishResult, error) {
	body, _ := json.Marshal(map[string]string{"path": path})
	req, _ := http.NewRequest(http.MethodPost,
		c.RegistrarBase+"/api/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	var out DaemonPublishResult
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return &out, errors.New(out.Error)
	}
	return &out, nil
}

// RegisterResult mirrors apps/registrar.RegisterResponse.
type RegisterResult struct {
	Name    string `json:"name"`
	Root    string `json:"root"`
	Version int64  `json:"version"`
	Error   string `json:"error,omitempty"`
}

func (c *DaemonClient) Register(name, root, owner string) (*RegisterResult, error) {
	body, _ := json.Marshal(map[string]string{
		"name": name, "root": root, "owner": owner,
	})
	req, _ := http.NewRequest(http.MethodPost,
		c.RegistrarBase+"/api/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	var out RegisterResult
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return &out, errors.New(out.Error)
	}
	return &out, nil
}

// SiteStats mirrors the registrar's StatsResponse. Kept separate so
// the desktop app's surface isn't coupled to the daemon's package
// layout.
type SiteStats struct {
	Name         string `json:"name"`
	Requests     int64  `json:"requests"`
	Bytes        int64  `json:"bytes"`
	UniqueIPs    int    `json:"unique_ips"`
	LastSeenUnix int64  `json:"last_seen_unix"`
}

func (c *DaemonClient) Stats(name string) (*SiteStats, error) {
	req, _ := http.NewRequest(http.MethodGet,
		c.RegistrarBase+"/api/stats/"+url.PathEscape(name), nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	var out SiteStats
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Unregister tears down a previously-registered name on this daemon.
// DHT records persist (TTL only), so visitors hitting another node may
// keep seeing the site briefly; visitors on the same daemon stop
// immediately.
func (c *DaemonClient) Unregister(name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, _ := http.NewRequest(http.MethodPost,
		c.RegistrarBase+"/api/unregister", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return c.do(req, nil)
}

// DaemonRevokeResult mirrors apps/registrar.RevokeResponse.
type DaemonRevokeResult struct {
	Name       string `json:"name"`
	ChunkCount int    `json:"chunk_count"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// Revoke asks the local daemon to broadcast a signed dht_revoke for a
// .alt name: resolve the name's content, collect chunk hashes,
// sign with the daemon's identity, and gossip the message out. Trusted
// remote nodes purge the chunks from their store and forward.
func (c *DaemonClient) Revoke(name string) (*DaemonRevokeResult, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, _ := http.NewRequest(http.MethodPost,
		c.RegistrarBase+"/api/revoke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	var out DaemonRevokeResult
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return &out, errors.New(out.Error)
	}
	return &out, nil
}

// Update points an already-registered name at a new root. Used by the
// app's "re-publish" path so the same name can be pointed at fresh
// content without the user noticing the difference.
func (c *DaemonClient) Update(name, root string) (*RegisterResult, error) {
	body, _ := json.Marshal(map[string]string{
		"name": name, "root": root,
	})
	req, _ := http.NewRequest(http.MethodPost,
		c.RegistrarBase+"/api/update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	var out RegisterResult
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return &out, errors.New(out.Error)
	}
	return &out, nil
}

func (c *DaemonClient) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// The daemon's registrar returns either {error: "..."} or a
		// typed response with .Error set; try to surface either.
		var asMap map[string]any
		if json.Unmarshal(body, &asMap) == nil {
			if msg, ok := asMap["error"].(string); ok && msg != "" {
				return fmt.Errorf("daemon: %s", msg)
			}
		}
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}
