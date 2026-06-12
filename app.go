package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// defaultBackendURL is the production accounts/approvals backend, served
// over HTTPS by Caddy on the always-on VPS. Override with the
// ALTNET_BACKEND env var for local development against a backend on
// 127.0.0.1:8787.
const defaultBackendURL = "https://api.panmox.org"

// App is the Wails-bound singleton: every method on it becomes a
// promise-returning function in the JS frontend.
type App struct {
	ctx     context.Context
	backend *BackendClient

	mu      sync.Mutex
	session *Session

	// daemon is lazily created on first node-mode action; that way the
	// app starts cheap even for users who never become a node.
	daemonCfg    *DaemonConfig
	daemonClient *DaemonClient
	daemon       *Supervisor
	daemonMu     sync.Mutex
	appDataDir   string

	prefsMu sync.Mutex
	prefs   *AppPrefs
}

func NewApp() *App {
	url := os.Getenv("ALTNET_BACKEND")
	if url == "" {
		url = defaultBackendURL
	}
	url = strings.TrimRight(url, "/")
	return &App{backend: NewBackendClient(url)}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// On Linux the app must run as root (see rootcheck_linux.go). Pop a
	// blocking warning dialog immediately if it isn't — in a goroutine so
	// the window still finishes coming up behind it. No-op on other OSes.
	if notRunningAsRoot() {
		go wruntime.MessageDialog(a.ctx, wruntime.MessageDialogOptions{
			Type:    wruntime.WarningDialog,
			Title:   "Run AltNet Studio as root",
			Message: rootRequirementMessage,
		})
	}
	if s, err := loadSession(); err == nil && s != nil && !s.Expired() {
		a.mu.Lock()
		a.session = s
		a.mu.Unlock()
	}
	if dir, err := os.UserConfigDir(); err == nil {
		a.appDataDir = filepath.Join(dir, "AltNet")
	}
	a.prefsMu.Lock()
	a.prefs = loadPrefs()
	wantsNode := a.prefs.IsNode
	a.prefsMu.Unlock()

	// Always-on: every launch re-asserts the Run-key entry so the app
	// comes back at boot. Cheap, idempotent, no-op if already set.
	_ = EnableAutoStart()
	// One-time cleanup of the pre-rename "AltNet" Run-key entry. After
	// the rename to AltNet Studio, the old entry pointed at a binary
	// that no longer existed. Idempotent.
	CleanupLegacyAutoStart()

	// If the user previously chose to be a node, start the daemon
	// immediately. Best-effort — failures show up on the node screen.
	if wantsNode {
		if err := a.ensureDaemon(); err == nil {
			_ = a.daemon.Start()
		}
	}
}

// NeedsRoot reports whether the app is running without the privileges it
// needs (Linux, not root). The frontend renders a persistent warning
// banner when this is true. Always false on Windows/macOS.
func (a *App) NeedsRoot() bool { return notRunningAsRoot() }

// shutdown is wired to OnBeforeClose so we tear the daemon down before
// the window goes away (otherwise the child process is orphaned).
func (a *App) shutdown(_ context.Context) bool {
	a.daemonMu.Lock()
	d := a.daemon
	a.daemonMu.Unlock()
	if d != nil {
		_ = d.Stop(3_000_000_000) // 3s in nanoseconds, avoid time import shuffle
	}
	return false // don't prevent close
}

// --- Auth ---

type UserInfo struct {
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

func (a *App) currentSession() *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session
}

func (a *App) setSession(s *Session) {
	a.mu.Lock()
	a.session = s
	a.mu.Unlock()
	if s != nil {
		_ = saveSession(s)
	} else {
		_ = clearSession()
	}
}

func (a *App) requireToken() (string, error) {
	s := a.currentSession()
	if s == nil || s.Expired() {
		return "", errors.New("not signed in")
	}
	return s.Token, nil
}

func (a *App) CurrentUser() *UserInfo {
	s := a.currentSession()
	if s == nil || s.Expired() {
		return nil
	}
	return &UserInfo{Email: s.Email, IsAdmin: s.IsAdmin}
}

func (a *App) BackendURL() string { return a.backend.BaseURL }

func (a *App) Signup(email, password string) error {
	return a.translate(a.backend.Signup(email, password))
}

func (a *App) Verify(email, code string) (*UserInfo, error) {
	resp, err := a.backend.Verify(email, code)
	if err != nil {
		return nil, a.translate(err)
	}
	a.setSession(&Session{Token: resp.Token, Email: resp.Email, IsAdmin: resp.IsAdmin, ExpiresAt: resp.ExpiresAt})
	return &UserInfo{Email: resp.Email, IsAdmin: resp.IsAdmin}, nil
}

func (a *App) Login(email, password string) (*UserInfo, error) {
	resp, err := a.backend.Login(email, password)
	if err != nil {
		return nil, a.translate(err)
	}
	a.setSession(&Session{Token: resp.Token, Email: resp.Email, IsAdmin: resp.IsAdmin, ExpiresAt: resp.ExpiresAt})
	return &UserInfo{Email: resp.Email, IsAdmin: resp.IsAdmin}, nil
}

func (a *App) Logout() error {
	a.setSession(nil)
	return nil
}

// RequestPasswordReset triggers an email with a 6-digit code. The
// backend always returns success (even for unknown emails) to avoid
// account-enumeration leaks; surface the actual SMTP error if any.
func (a *App) RequestPasswordReset(email string) error {
	return a.translate(a.backend.RequestPasswordReset(email))
}

// ConfirmPasswordReset finishes the flow. After success the backend
// invalidates *all* sessions for the user, so the desktop app drops
// its local session too — the user has to log in again with the new
// password.
func (a *App) ConfirmPasswordReset(email, code, newPassword string) error {
	if err := a.backend.ConfirmPasswordReset(email, code, newPassword); err != nil {
		return a.translate(err)
	}
	a.setSession(nil)
	return nil
}

// --- Node mode ---

// NodeStatus is the JSON the frontend polls for the node panel.
type NodeStatus struct {
	Running        bool     `json:"running"`
	Pid            int      `json:"pid,omitempty"`
	UptimeSec      int64    `json:"uptime_sec,omitempty"`
	Binary         string   `json:"binary,omitempty"`
	Note           string   `json:"note,omitempty"`
	Adopted        bool     `json:"adopted,omitempty"`
	PeerID         string   `json:"peer_id,omitempty"`
	ShortID        string   `json:"short_id,omitempty"`
	ConnectedPeers int      `json:"connected_peers"`
	StoreEntries   int      `json:"store_entries"`
	StoreBytes     int64    `json:"store_bytes"`
	GatewayURL     string   `json:"gateway_url,omitempty"`
	RecentLogs     []string `json:"recent_logs,omitempty"`
}

func (a *App) ensureDaemon() error {
	a.daemonMu.Lock()
	defer a.daemonMu.Unlock()

	// Drop a stale adopted supervisor: the external daemon died, but
	// our cached supervisor still claims it's running (adopted ones
	// don't spawn or kill, they just observe). Re-probe; if nothing
	// answers, we'll fall through and create a fresh spawn supervisor.
	if a.daemon != nil && a.daemon.Adopted() && !ProbeExistingDaemon() {
		a.daemon = nil
		a.daemonClient = nil
	}
	if a.daemon != nil {
		return nil
	}

	if a.appDataDir == "" {
		return errors.New("no user config dir")
	}
	cfg, err := DefaultDaemonConfig(a.appDataDir)
	if err != nil {
		return err
	}
	if bin, err := FindDaemonBinary(); err == nil {
		cfg.BinaryPath = bin
	}
	// Pipe seed peers into the daemon's -bootstrap flag. The list is
	// defaults-merged-with-user: baked-in addresses always dial,
	// user-added ones extend the set. Without any reachable seed a
	// fresh install stays alone; with one, the supervisor also
	// infers a -relay so NAT'd installs can be reached through it.
	cfg.BootstrapPeers = joinSeeds(a.effectiveSeedPeers())
	a.daemonCfg = cfg
	a.daemonClient = NewDaemonClient(cfg)
	// If a daemon is already running on the standard metrics port,
	// adopt it instead of trying to spawn a duplicate. This is the
	// common case when the user started the daemon manually for
	// testing — we use their token (read in DefaultDaemonConfig).
	if ProbeExistingDaemon() {
		a.daemon = NewAdoptedSupervisor(cfg)
	} else {
		a.daemon = NewSupervisor(cfg)
	}
	return nil
}

// EnableNodeMode is the "I want to be a node" entry point. It:
//   1. Marks the user as a node in prefs
//   2. Registers Windows autostart so the daemon comes back after reboot
//   3. Starts the daemon now (if it isn't already)
//   4. Installs the Windows NRPT rule routing every *.alt DNS query to
//      the daemon's resolver, so any .alt name resolves in any browser
//      without per-site hosts-file entries.
// Idempotent — calling it twice is fine.
func (a *App) EnableNodeMode() error {
	// On Linux this needs root (port 80, resolved config, CA). Fail loudly
	// rather than half-start a daemon that can't serve .alt.
	if notRunningAsRoot() {
		return errors.New("AltNet Studio must be run as root on Linux to be a node. " +
			"Quit and relaunch with: sudo -E ./AltNetStudio")
	}
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	// Start first so we surface a clear error before persisting state.
	st := a.daemon.Status()
	if !st.Running {
		if err := a.daemon.Start(); err != nil {
			return err
		}
	}
	a.prefsMu.Lock()
	if a.prefs == nil {
		a.prefs = &AppPrefs{}
	}
	a.prefs.IsNode = true
	a.prefs.AutoStartWithOS = true
	p := *a.prefs
	a.prefsMu.Unlock()
	_ = savePrefs(&p)
	_ = EnableAutoStart() // best-effort
	// Best-effort: install the NRPT rule so every *.alt query routes
	// to the daemon's DNS resolver on 127.0.0.53. UAC prompts once
	// the first time; no-op on subsequent calls. After this, any
	// .alt name the user types into their browser resolves through
	// us, without us having to maintain a per-site hosts list.
	_ = installNRPTRuleImpl()
	return nil
}

// HostingTermsAccepted reports whether the user has already clicked
// through the be-a-node legal disclaimer. The frontend gates the
// EnableNodeMode call on this; if false, it shows the disclaimer
// first and only enables on Accept.
func (a *App) HostingTermsAccepted() bool {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	return a.prefs != nil && a.prefs.HostingTermsAcceptedAt != 0
}

// AcceptHostingTerms stamps the current time into prefs as the moment
// the user accepted the hosting disclaimer. Idempotent — a second
// call doesn't overwrite the original acceptance time, so we keep
// the original audit trail.
func (a *App) AcceptHostingTerms() error {
	a.prefsMu.Lock()
	if a.prefs == nil {
		a.prefs = &AppPrefs{}
	}
	if a.prefs.HostingTermsAcceptedAt == 0 {
		a.prefs.HostingTermsAcceptedAt = time.Now().Unix()
	}
	p := *a.prefs
	a.prefsMu.Unlock()
	return savePrefs(&p)
}

// DisableNodeMode is the "stop being a node" opt-out. Stops the
// daemon, clears the prefs flag, removes Windows autostart.
func (a *App) DisableNodeMode() error {
	a.daemonMu.Lock()
	d := a.daemon
	a.daemonMu.Unlock()
	if d != nil {
		_ = d.Stop(5_000_000_000)
	}
	a.prefsMu.Lock()
	if a.prefs == nil {
		a.prefs = &AppPrefs{}
	}
	a.prefs.IsNode = false
	// AutoStartWithOS stays true — the app itself is always-on regardless
	// of node mode. Only the daemon stops; the window still comes back
	// at boot (hidden, in the background).
	p := *a.prefs
	a.prefsMu.Unlock()
	_ = savePrefs(&p)
	return nil
}

// IsNodeMode is what the frontend checks on launch to decide whether
// to land directly on the node screen.
func (a *App) IsNodeMode() bool {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	return a.prefs != nil && a.prefs.IsNode
}

// --- Seed peers (multi-node bootstrap) ---

// currentSeedPeers returns the persisted seed list, normalised.
// Returned slice is a fresh copy — safe to hand to anything.
func (a *App) currentSeedPeers() []string {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	if a.prefs == nil {
		return nil
	}
	out := make([]string, 0, len(a.prefs.SeedPeers))
	for _, s := range a.prefs.SeedPeers {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// GetSeedPeers is the read side bound to the UI: just the user-added
// list (defaults are exposed separately so the UI can show them as
// read-only built-ins).
func (a *App) GetSeedPeers() []string { return a.currentSeedPeers() }

// GetDefaultSeedPeers returns the always-on baked-in seeds. The UI
// shows these as "Auto-connected" and doesn't let the user remove
// them — to change them you edit the constant in
// daemon_supervisor.go and rebuild.
func (a *App) GetDefaultSeedPeers() []string {
	out := make([]string, len(defaultSeedPeers))
	copy(out, defaultSeedPeers)
	return out
}

// effectiveSeedPeers merges baked-in defaults with the user-added
// list, de-duped while preserving order. This is what the supervisor
// actually hands to the daemon's -bootstrap flag.
func (a *App) effectiveSeedPeers() []string {
	out := make([]string, 0, len(defaultSeedPeers)+4)
	seen := map[string]bool{}
	for _, s := range defaultSeedPeers {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	for _, s := range a.currentSeedPeers() {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}

// SetSeedPeers replaces the seed list, normalises entries, persists,
// and restarts the daemon so the new -bootstrap flag takes effect.
// Returns the cleaned list the UI should render going forward.
func (a *App) SetSeedPeers(peers []string) ([]string, error) {
	clean := make([]string, 0, len(peers))
	seen := map[string]bool{}
	for _, p := range peers {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		// Accept "host" (assume :9000) or "host:port". Stash the
		// canonical "host:port" form so the supervisor doesn't have
		// to guess later.
		if _, _, err := net.SplitHostPort(p); err != nil {
			p = p + ":9000"
		}
		clean = append(clean, p)
		seen[p] = true
	}
	a.prefsMu.Lock()
	if a.prefs == nil {
		a.prefs = &AppPrefs{}
	}
	a.prefs.SeedPeers = clean
	snap := *a.prefs
	a.prefsMu.Unlock()
	if err := savePrefs(&snap); err != nil {
		return clean, err
	}
	// Restart the daemon to pick up the new -bootstrap. If the user
	// isn't currently a node, ensureDaemon will spin up fresh next
	// time they need it.
	a.daemonMu.Lock()
	d := a.daemon
	a.daemonMu.Unlock()
	if d != nil && !d.Adopted() {
		if d.Status().Running {
			_ = d.Stop(5 * time.Second)
		}
		a.daemonMu.Lock()
		a.daemon = nil
		a.daemonClient = nil
		a.daemonMu.Unlock()
		if err := a.ensureDaemon(); err == nil {
			a.daemonMu.Lock()
			nd := a.daemon
			a.daemonMu.Unlock()
			if nd != nil {
				_ = nd.Start()
			}
		}
	}
	return clean, nil
}

// joinSeeds concatenates seed addresses for the daemon's -bootstrap flag.
func joinSeeds(peers []string) string { return strings.Join(peers, ",") }

func (a *App) GetNodeStatus() *NodeStatus {
	a.daemonMu.Lock()
	d := a.daemon
	dc := a.daemonClient
	cfg := a.daemonCfg
	a.daemonMu.Unlock()
	if d == nil {
		return &NodeStatus{Note: "node has not been started yet"}
	}
	st := d.Status()
	out := &NodeStatus{
		Running:    st.Running,
		Pid:        st.Pid,
		UptimeSec:  st.UptimeSec,
		Binary:     st.Binary,
		Note:       st.Note,
		Adopted:    d.Adopted(),
		RecentLogs: d.RecentLogs(20),
	}
	if cfg != nil {
		out.GatewayURL = "http://" + cfg.GatewayAddr
	}
	if st.Running && dc != nil {
		if m, err := dc.Metrics(); err == nil {
			out.PeerID = m.PeerID
			out.ShortID = m.ShortID
			out.ConnectedPeers = m.ConnectedPeers
			out.StoreEntries = m.StoreEntries
			out.StoreBytes = m.StoreBytes
		}
	}
	return out
}

// --- Domain registration ---

func (a *App) RequestDomain(name, description, root string) (*BackendDomainRow, error) {
	tok, err := a.requireToken()
	if err != nil {
		return nil, err
	}
	row, err := a.backend.RequestDomain(tok, name, description, root)
	if err != nil {
		return nil, a.translate(err)
	}
	return toBackendDomainRow(row), nil
}

// DeleteAccount permanently removes the signed-in user. Local session
// is cleared so the next render lands on the login screen.
func (a *App) DeleteAccount() error {
	tok, err := a.requireToken()
	if err != nil {
		return err
	}
	if err := a.backend.DeleteAccount(tok); err != nil {
		return a.translate(err)
	}
	a.setSession(nil)
	return nil
}

func (a *App) MyDomains() ([]*BackendDomainRow, error) {
	tok, err := a.requireToken()
	if err != nil {
		return nil, err
	}
	rows, err := a.backend.MyDomains(tok)
	if err != nil {
		return nil, a.translate(err)
	}
	out := make([]*BackendDomainRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBackendDomainRow(&r))
	}
	return out, nil
}

func (a *App) AdminPending() ([]*BackendDomainRow, error) {
	tok, err := a.requireToken()
	if err != nil {
		return nil, err
	}
	rows, err := a.backend.AdminPending(tok)
	if err != nil {
		return nil, a.translate(err)
	}
	out := make([]*BackendDomainRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBackendDomainRow(&r))
	}
	return out, nil
}

func (a *App) AdminDecide(id int64, decision string) error {
	tok, err := a.requireToken()
	if err != nil {
		return err
	}
	return a.translate(a.backend.AdminDecide(tok, id, decision))
}

// --- Abuse reports ---

// ReportSite flags a .alt name as hosting illegal/abusive content.
// Open to any signed-in user. Returns the created report row (id +
// status) so the UI can confirm it landed.
func (a *App) ReportSite(name, reason string) (*AbuseReportRow, error) {
	tok, err := a.requireToken()
	if err != nil {
		return nil, err
	}
	row, err := a.backend.SubmitReport(tok, name, reason)
	if err != nil {
		return nil, a.translate(err)
	}
	return row, nil
}

// AdminReports returns the pending abuse-report queue. Admin-only on
// the backend; the frontend gates the menu item on state.user.is_admin.
func (a *App) AdminReports() ([]AbuseReportRow, error) {
	tok, err := a.requireToken()
	if err != nil {
		return nil, err
	}
	rows, err := a.backend.AdminReports(tok)
	if err != nil {
		return nil, a.translate(err)
	}
	return rows, nil
}

// AdminDecideReport stamps the admin's decision on a report. On
// "revoke" we also ask the local daemon to broadcast a signed
// dht_revoke so other nodes purge the chunks. To do that we need the
// reported name, which the caller passes alongside the report id.
//
// Failure of the broadcast doesn't roll back the backend decision —
// the admin already said "revoke," and partial propagation is the
// default state of any gossip protocol. We report the broadcast
// outcome back to the UI as a toast so the admin knows.
func (a *App) AdminDecideReport(id int64, decision, note, name string) error {
	tok, err := a.requireToken()
	if err != nil {
		return err
	}
	// The backend now performs the authority-signed revoke itself (it's
	// co-located with the authority registrar). We no longer revoke via
	// the admin's local daemon — that signed with the wrong key and the
	// network ignored it. The 'name' arg is kept for signature stability.
	_ = name
	return a.translate(a.backend.AdminDecideReport(tok, id, decision, note))
}

// AdminTakedown takes a .alt name down network-wide. This is the admin
// "search a domain, click take down" action in Studio: it routes through
// the backend's authority-registrar bridge, so the revoke is signed by the
// trusted authority and honored by every node.
func (a *App) AdminTakedown(name string) error {
	tok, err := a.requireToken()
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("enter a .alt name to take down")
	}
	return a.translate(a.backend.AdminRevoke(tok, name))
}

// OpenURL launches the user's default OS browser at the given URL.
// Used by the "View site" button on the Registered Sites screen so the
// user lands on the gateway's path-based browse route in a real
// browser (the Wails webview can navigate too, but that loses the
// app's chrome).
func (a *App) OpenURL(u string) error {
	if a.ctx == nil {
		return errors.New("app not ready")
	}
	wruntime.BrowserOpenURL(a.ctx, u)
	return nil
}

// --- Per-site analytics + take-down ---

// SiteAnalytics is what the dashboard polls. Mirrors DaemonClient.SiteStats
// but exposed under a stable JSON shape so the frontend types stay
// independent of the daemon's wire format.
type SiteAnalytics struct {
	Name         string `json:"name"`
	Requests     int64  `json:"requests"`
	Bytes        int64  `json:"bytes"`
	UniqueIPs    int    `json:"unique_ips"`
	LastSeenUnix int64  `json:"last_seen_unix"`
	GatewayAddr  string `json:"gateway_addr"`
}

// SiteAnalyticsBatch grabs stats for a list of sites in one shot so
// the dashboard's poll loop is N calls per refresh, not N×M.
func (a *App) SiteAnalyticsBatch(names []string) ([]*SiteAnalytics, error) {
	if err := a.ensureDaemon(); err != nil {
		return nil, err
	}
	a.daemonMu.Lock()
	dc := a.daemonClient
	cfg := a.daemonCfg
	a.daemonMu.Unlock()
	out := make([]*SiteAnalytics, 0, len(names))
	for _, n := range names {
		st, err := dc.Stats(n)
		if err != nil {
			// One bad name shouldn't kill the whole dashboard refresh.
			out = append(out, &SiteAnalytics{Name: n, GatewayAddr: cfg.GatewayAddr})
			continue
		}
		out = append(out, &SiteAnalytics{
			Name:         st.Name,
			Requests:     st.Requests,
			Bytes:        st.Bytes,
			UniqueIPs:    st.UniqueIPs,
			LastSeenUnix: st.LastSeenUnix,
			GatewayAddr:  cfg.GatewayAddr,
		})
	}
	return out, nil
}

// TakeDownSite lets a signed-in publisher remove their own approved site
// network-wide. It goes through the backend authority bridge so the revoke is
// signed by the canonical registrar, not by this machine's local daemon.
func (a *App) TakeDownSite(name string) error {
	tok, err := a.requireToken()
	if err != nil {
		return err
	}
	if err := a.backend.UserTakedown(tok, name); err != nil {
		return a.translate(err)
	}
	return nil
}

// GatewayAddr lets the frontend know whether the daemon got port 80
// (clean URL) or fell back to 9080. The dashboard uses this to render
// the right link without hardcoding port assumptions in JS.
func (a *App) GatewayAddr() string {
	if err := a.ensureDaemon(); err != nil {
		return "127.0.0.1:9080"
	}
	a.daemonMu.Lock()
	cfg := a.daemonCfg
	a.daemonMu.Unlock()
	if cfg == nil {
		return "127.0.0.1:9080"
	}
	return cfg.GatewayAddr
}

// GatewayTLSAddr returns the HTTPS listen address (e.g. 127.0.0.1:443).
// Empty means the daemon hasn't been told to serve TLS yet.
func (a *App) GatewayTLSAddr() string {
	if err := a.ensureDaemon(); err != nil {
		return ""
	}
	a.daemonMu.Lock()
	cfg := a.daemonCfg
	a.daemonMu.Unlock()
	if cfg == nil {
		return ""
	}
	return cfg.GatewayTLSAddr
}

// --- HTTPS / CA trust integration ---
//
// The dashboard surfaces a single toggle: "Trust the AltNet CA so
// browsers stop warning on https://name.alt/". On install, we drop the
// CA's public cert into the user's CurrentUser Root store (no admin).
// On uninstall, we remove the matching entries. Status is queried so
// the UI knows which way to render the button.

// HTTPSReady reports whether the local CA is installed in the user's
// trust store, AND the daemon is running with HTTPS enabled. Both
// have to be true for https:// URLs to actually work.
func (a *App) HTTPSReady() bool {
	tlsAddr := a.GatewayTLSAddr()
	if tlsAddr == "" {
		return false
	}
	return caTrustedImpl()
}

// CATrusted is just the trust-store half — useful for the UI to show
// "CA installed but daemon HTTPS port not bound" vs "CA missing".
func (a *App) CATrusted() bool { return caTrustedImpl() }

// EnableHTTPS imports the daemon's CA cert into the user's
// CurrentUser Root store. Pops the OS confirmation (Windows shows a
// security dialog the first time you add a cert to Root). No UAC.
func (a *App) EnableHTTPS() error {
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	a.daemonMu.Lock()
	cfg := a.daemonCfg
	a.daemonMu.Unlock()
	if cfg == nil || cfg.CADir == "" {
		return errors.New("ca dir not configured on the daemon")
	}
	certPath := filepath.Join(cfg.CADir, "altnet-ca.crt")
	// The cert may not exist yet if the daemon hasn't started its
	// HTTPS listener (which is what creates it on first launch). Give
	// it a beat to come up.
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(certPath); err == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("ca cert not found at %s yet (daemon may still be starting)", certPath)
	}
	return installCATrustImpl(certPath)
}

// DisableHTTPS removes the CA cert from the trust store. The daemon
// keeps serving TLS (with an untrusted cert) but the user gets the
// browser warning back, same as no install.
func (a *App) DisableHTTPS() error {
	return removeCATrustImpl()
}

// --- Hosts file integration ---
//
// The point: turn the working-but-ugly URL
//   http://127.0.0.1:9080/n/panmox.alt/
// into the natural one
//   http://panmox.alt:9080
// for any browser on THIS machine. We do this by appending
// `127.0.0.1 panmox.alt` to the system hosts file. Real DNS for `.alt`
// would do this too, but requires the AltNet daemon to bind UDP/53 +
// system DNS reconfiguration — both of which we defer until we ship a
// proper "configure system DNS" mode.
//
// Writing the hosts file needs admin on Windows: see
// hosts_windows.go for the elevated-PowerShell trick. Each toggle
// triggers one UAC prompt; subsequent visits in any browser are
// instant.

// HostsEntryStatus returns a name -> installed map. Batched so the UI
// can render the whole list in one round trip instead of N calls.
func (a *App) HostsEntryStatus(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = hostsEntryExistsImpl(n)
	}
	return out
}

func (a *App) InstallHostsEntry(name string) error {
	return installHostsEntryImpl(name)
}

func (a *App) RemoveHostsEntry(name string) error {
	return removeHostsEntryImpl(name)
}

// PickDirectory opens a native folder picker and returns the chosen
// path (empty string if the user cancels).
func (a *App) PickDirectory() (string, error) {
	if a.ctx == nil {
		return "", errors.New("app not ready")
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Pick the folder to publish",
	})
}

// PickFiles opens a native multi-file picker. Use when the user has a
// loose handful of files (e.g. a single index.html) instead of a
// well-formed site folder.
func (a *App) PickFiles() ([]string, error) {
	if a.ctx == nil {
		return nil, errors.New("app not ready")
	}
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Pick files for the site",
	})
}

// PublishSite is the unified entry point: hand it the .alt name plus a
// list of paths (one folder OR one-or-more files), and it stages a
// publishable directory, gets the root hash from the daemon, and
// registers the name with the signed-in user as owner.
//
// Auto-spawns the daemon if it isn't running yet — publishing requires
// a node, so making the user click "Be a node" first is just friction.
// The daemon stays up until they explicitly stop it.
func (a *App) PublishSite(name string, paths []string) (*PublishResult, error) {
	if err := a.ensureDaemon(); err != nil {
		return nil, err
	}
	a.daemonMu.Lock()
	dc := a.daemonClient
	d := a.daemon
	cfg := a.daemonCfg
	a.daemonMu.Unlock()
	if !d.Status().Running {
		if err := d.Start(); err != nil {
			return nil, fmt.Errorf("starting daemon for publish: %w", err)
		}
	}
	// Even if Status said "running" we wait — fixed sleeps lie when the
	// daemon's still booting, and we just got bitten by an adopted-but-
	// dead supervisor returning fake "running". A real TCP probe is the
	// authoritative answer.
	if err := waitForTCP(cfg.RegistrarAddr, 6*time.Second); err != nil {
		return nil, fmt.Errorf("daemon registrar at %s never came up: %w", cfg.RegistrarAddr, err)
	}
	if a.CurrentUser() == nil {
		return nil, errors.New("not signed in")
	}
	publishDir, cleanup, err := stagePublishDir(paths)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	pub, err := dc.Publish(publishDir)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	// IMPORTANT: we do NOT register the name on the local daemon here.
	// Under permissioned naming only the seed authority (reached via the
	// backend) may bind a name -> root; a local-signed record is rejected
	// network-wide AND its republish loop would clobber the authority's
	// record. PublishSite only chunks the content into the DHT and returns
	// the root. The caller binds it: the request flow on admin approval,
	// or PublishToOwnedDomain for a domain you already own.
	return &PublishResult{
		Root:       pub.Root,
		EntryCount: pub.EntryCount,
		Name:       name,
		GatewayURL: cfg.GatewayAddr,
	}, nil
}

// PublishToOwnedDomain publishes content to a domain the signed-in user
// already owns. It chunks the folder into the DHT (PublishSite), then asks
// the backend to bind name -> root via the seed authority — authority-
// signed, so it resolves network-wide. No admin re-review: you own the
// name. (The initial request flow carries content through admin approval
// instead.)
func (a *App) PublishToOwnedDomain(name string, paths []string) (*PublishResult, error) {
	tok, err := a.requireToken()
	if err != nil {
		return nil, err
	}
	res, err := a.PublishSite(name, paths)
	if err != nil {
		return nil, err
	}
	if err := a.backend.UserPublish(tok, name, res.Root); err != nil {
		return nil, a.translate(err)
	}
	return res, nil
}

// stagePublishDir resolves a list of user-picked paths into a single
// directory the daemon can publish.
//
//   - One folder, alone: published as-is, no copy. (Fast path.)
//   - Mix of folders and/or files: a fresh temp dir is built. Each
//     folder lands as a subdirectory keeping its base name; each file
//     lands at the root. So picking `index.html` + folder `assets/`
//     produces a site with `/index.html` and `/assets/...`.
//
// Returned cleanup removes any temp dir we created (no-op for the fast
// path).
func stagePublishDir(paths []string) (string, func(), error) {
	noop := func() {}
	if len(paths) == 0 {
		return "", noop, errors.New("no files or folder chosen")
	}
	if len(paths) == 1 {
		info, err := os.Stat(paths[0])
		if err != nil {
			return "", noop, err
		}
		if info.IsDir() {
			return paths[0], noop, nil
		}
	}
	tmp, err := os.MkdirTemp("", "altnet-publish-*")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			cleanup()
			return "", noop, err
		}
		dst := filepath.Join(tmp, filepath.Base(p))
		if _, err := os.Stat(dst); err == nil {
			cleanup()
			return "", noop, fmt.Errorf("two picked items share a name: %s", filepath.Base(p))
		}
		if info.IsDir() {
			if err := copyDir(p, dst); err != nil {
				cleanup()
				return "", noop, err
			}
		} else {
			if err := copyFile(p, dst); err != nil {
				cleanup()
				return "", noop, err
			}
		}
	}
	return tmp, cleanup, nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, s)
	return err
}

// copyDir does a vanilla recursive copy, preserving the relative
// structure of src under dst. Skips non-regular files (symlinks,
// devices, sockets) to match what files.PublishDir does anyway.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

// waitForTCP blocks until something accepts a TCP connection on addr,
// or returns an error after timeout. Used to gate publish/register
// calls until the daemon's registrar is actually listening — fixed
// sleeps were lying when the boot was slow or the daemon never came up.
func waitForTCP(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not reachable within %s", timeout)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// PublishResult is what the frontend gets back after a successful
// publish + register. The GatewayURL is the local address it can hit
// to verify the site is live.
type PublishResult struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	EntryCount int    `json:"entry_count"`
	Version    int64  `json:"version"`
	GatewayURL string `json:"gateway_url"`
}

// BackendDomainRow is the JSON-friendly version of DomainRow we hand
// to the frontend. (Kept distinct so we can extend the frontend shape
// without touching the wire format.)
type BackendDomainRow struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	UserEmail   string `json:"user_email,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	DecidedAt   int64  `json:"decided_at,omitempty"`
}

func toBackendDomainRow(r *DomainRow) *BackendDomainRow {
	return &BackendDomainRow{
		ID: r.ID, Name: r.Name, Description: r.Description, Status: r.Status,
		UserEmail: r.UserEmail, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt,
	}
}

// translate turns network errors into a UI-friendly message instead of
// leaking Go internals.
func (a *App) translate(err error) error {
	if err == nil {
		return nil
	}
	if IsBackendUnreachable(err) {
		return errors.New("can't reach the AltNet server (" + a.backend.BaseURL + "). is the backend running?")
	}
	return err
}
