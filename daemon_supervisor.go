package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crypto/rand"
)

// defaultSeedPeers is the always-on bootstrap list baked into the app.
// Every install dials these on every daemon launch — that's how "you
// install AltNet, it auto-joins the network" actually works.
//
// Each entry needs to be a publicly-reachable `host:port` running an
// AltNet daemon. The project operator (you) is responsible for keeping
// at least one of these alive — via a small VPS, a Cloudflare Tunnel
// from a home PC, or port-forwarding. When none of these resolve, the
// dial fails silently and the install stays alone; flip on a single
// reachable seed and every install in the wild starts joining.
//
// To swap or extend, edit this slice and rebuild the app. User-added
// seeds (from the Node screen) are merged on top of these — defaults
// stay even if the user clears their own list.
var defaultSeedPeers = []string{
	// Hostname first so we can re-point the seed (or add more) via DNS
	// without shipping a new build. The raw IP is a fallback so installs
	// still join even before DNS propagates or if name resolution is
	// blocked — it's the Oracle Cloud seed VM's current ephemeral IP.
	"seed.panmox.org:9000",
	"130.162.215.155:9000",
}

// canonicalTrustedRevokers are the Ed25519 pubkeys (hex) whose signed
// dht_revoke messages every install honors network-wide. This is the
// AltNet seed/registrar identity running on the public VPS — the
// authority that publishes and can revoke panmox.org-gated .alt names.
// Baked in so a takedown (DMCA/DSA) propagates to all nodes with zero
// per-install configuration. User-added keys in trusted-revokers.txt are
// preserved; these are only ever added, never removed.
var canonicalTrustedRevokers = []string{
	// This is the raw Ed25519 PUBLIC KEY (hex) of the seed/registrar
	// identity — the value dht_revoke messages are signed with and
	// verified against. NOTE: this is NOT the node ID shown in metrics /
	// the "identity:" startup line (54c96e08…) — that's a different
	// encoding of the same key. Revoke verification uses the pubkey hex
	// below, so this must be the pubkey, or takedowns get rejected.
	"1a287188173a53d631d781bf21198012d8c18b7c345763c48a41446f155f2910",
}

// canonicalTrustedRegistrars are the Ed25519 PUBLIC KEYS (hex) whose
// signed name records every install will resolve. This is the PERMISSIONED-
// NAMING authority: a .alt name only resolves on the canonical network if
// the seed/registrar authority signed it (i.e. the admin approved it).
// Same key as the revoker (the VPS registrar identity). Baked in so the
// gate is enforced network-wide, not just inside AltNet Studio.
//
// NOTE: raw pubkey hex (the NameRecord "pk" field), NOT the node ID.
var canonicalTrustedRegistrars = []string{
	"1a287188173a53d631d781bf21198012d8c18b7c345763c48a41446f155f2910",
}

// ensureTrustedKeys merges the given keys into <dataDir>/<file>, never
// clobbering keys the user (or a prior version) added.
func ensureTrustedKeys(dataDir, file string, keys []string) error {
	path := filepath.Join(dataDir, file)
	present := map[string]bool{}
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			key := strings.ToLower(strings.TrimSpace(ln))
			if key == "" || strings.HasPrefix(key, "#") {
				lines = append(lines, ln)
				continue
			}
			present[key] = true
			lines = append(lines, ln)
		}
	}
	changed := false
	for _, k := range keys {
		if !present[strings.ToLower(k)] {
			lines = append(lines, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o600)
}

// ensureTrustedRevokers makes sure every key in canonicalTrustedRevokers
// is present in <dataDir>/trusted-revokers.txt, which the daemon loads on
// startup. It merges (never clobbers) so user-added revokers survive.
func ensureTrustedRevokers(dataDir string) error {
	path := filepath.Join(dataDir, "trusted-revokers.txt")
	present := map[string]bool{}
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			key := strings.ToLower(strings.TrimSpace(ln))
			if key == "" || strings.HasPrefix(key, "#") {
				lines = append(lines, ln)
				continue
			}
			present[key] = true
			lines = append(lines, ln)
		}
	}
	changed := false
	for _, k := range canonicalTrustedRevokers {
		if !present[strings.ToLower(k)] {
			lines = append(lines, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o600)
}

// DaemonConfig is everything the supervisor needs to spawn one altnet
// daemon instance with sensible localhost defaults. The user normally
// doesn't touch any of this -- the desktop app picks ports + paths.
type DaemonConfig struct {
	BinaryPath     string
	DataDir        string
	KeyDir         string
	CADir          string // where the AltNet local CA cert+key live
	ListenAddr     string // peer listener
	GatewayAddr    string // HTTP browse
	GatewayTLSAddr string // HTTPS browse (empty = HTTPS disabled)
	DNSAddr        string // local DNS resolver
	RegistrarAddr  string // HTTP registrar API
	MetricsAddr    string // HTTP metrics
	RegistrarToken string
	BootstrapPeers string // comma-separated host:port
	ExtraArgs      []string
}

// DefaultDaemonConfig returns a config that puts everything in the
// user's per-OS app data dir and uses non-privileged ports.
//
// Token resolution order: (1) the daemon's own `data/registrar-token.txt`
// next to the daemon binary, so a manually-launched daemon and the app
// share a token; (2) the per-user app config file; (3) freshly generated
// and persisted to the app config file.
func DefaultDaemonConfig(appDataDir string) (*DaemonConfig, error) {
	dataDir := filepath.Join(appDataDir, "daemon", "data")
	keyDir := filepath.Join(appDataDir, "daemon", "keys")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, err
	}
	// Make sure the baked-in seed/registrar revoker key is trusted by
	// this install's daemon. Best-effort: a write failure here shouldn't
	// block the node from starting.
	_ = ensureTrustedRevokers(dataDir)
	// Same for the permissioned-naming authority: only admin-approved
	// (authority-signed) names will resolve on this install.
	_ = ensureTrustedKeys(dataDir, "trusted-registrars.txt", canonicalTrustedRegistrars)

	daemonDir := ""
	if bin, err := FindDaemonBinary(); err == nil {
		daemonDir = filepath.Dir(bin)
	}
	tok, err := loadOrCreateToken(
		filepath.Join(appDataDir, "daemon", "registrar-token"),
		daemonDir,
	)
	if err != nil {
		return nil, err
	}

	// Prefer port 80 for the gateway so the browser URL is the natural
	// `http://name.alt/` instead of `http://name.alt:9080/`. Windows
	// lets a normal user bind port 80 as long as nothing else is using
	// it (IIS, World Wide Web service, Skype-of-old, etc.). If 80 is
	// taken we drop back to 9080 transparently — the desktop app reads
	// cfg.GatewayAddr later so the right URL appears in the dashboard.
	gatewayAddr := "127.0.0.1:9080"
	if tcpPortFree("127.0.0.1:80") {
		gatewayAddr = "127.0.0.1:80"
	}
	// Same logic for HTTPS: 443 is the natural port, fall back to 9443.
	gatewayTLSAddr := "127.0.0.1:9443"
	if tcpPortFree("127.0.0.1:443") {
		gatewayTLSAddr = "127.0.0.1:443"
	}

	return &DaemonConfig{
		DataDir: dataDir,
		KeyDir:  keyDir,
		CADir:   filepath.Join(appDataDir, "daemon", "ca"),
		// 0.0.0.0 so other PCs on the LAN can dial us as a seed peer.
		// For locked-down installs the user can override this to
		// 127.0.0.1 via prefs once that wiring exists.
		ListenAddr:     "0.0.0.0:9000",
		GatewayAddr:    gatewayAddr,
		GatewayTLSAddr: gatewayTLSAddr,
		// Local DNS resolver address is OS-specific (see daemonDNSAddr in
		// the per-platform nrpt_*.go files):
		//   Windows: 127.0.0.53:53  — captured by an NRPT rule.
		//   Linux:   127.0.0.1:5354 — 127.0.0.53 is taken by
		//            systemd-resolved, so we bind a free port and add a
		//            resolved drop-in routing *.alt here.
		//   other:   "" — resolver disabled until that OS is wired up.
		// The routing install happens client-side, elevated, when the
		// user enables node mode — see installNRPTRuleImpl per platform.
		DNSAddr:        daemonDNSAddr,
		RegistrarAddr:  "127.0.0.1:9090",
		MetricsAddr:    "127.0.0.1:9999",
		RegistrarToken: tok,
	}, nil
}

// relayAddrsFromBootstrap derives "host:9100" relay entries from each
// "host:9000" bootstrap entry. The convention is the daemon listens
// for peer connections on the listen port and for relay registrations
// on listen+100. Returns the comma-joined relay list, or "" if the
// input is empty/unparseable.
func relayAddrsFromBootstrap(bootstrap string) string {
	var out []string
	for _, p := range strings.Split(bootstrap, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		host, portStr, err := net.SplitHostPort(p)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		out = append(out, net.JoinHostPort(host, strconv.Itoa(port+100)))
	}
	return strings.Join(out, ",")
}

// tcpPortFree probes whether anything is currently listening on addr.
// Used to decide between port 80 and 9080 for the gateway. Quick: 250ms
// dial. A free port == nothing answers == port is available.
func tcpPortFree(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
	if err == nil {
		conn.Close()
		return false
	}
	return true
}

// loadOrCreateToken returns the registrar token to share with the daemon.
//   1. If `<daemonDir>/data/registrar-token.txt` exists, use it.
//      That's the file a manually-launched daemon writes its own token
//      to, so adopting it Just Works.
//   2. Else read `appConfigPath` if it exists.
//   3. Else generate a new 32-byte token and persist it at appConfigPath.
func loadOrCreateToken(appConfigPath, daemonDir string) (string, error) {
	if daemonDir != "" {
		candidate := filepath.Join(daemonDir, "data", "registrar-token.txt")
		if b, err := os.ReadFile(candidate); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b)), nil
		}
	}
	if b, err := os.ReadFile(appConfigPath); err == nil && len(b) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	if err := os.MkdirAll(filepath.Dir(appConfigPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(appConfigPath, []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// ProbeExistingDaemon checks whether a daemon is already listening on
// the standard metrics port. If so, returns true and the desktop app
// adopts it instead of trying to spawn a duplicate (which would crash
// on port conflict). Fast probe — 700ms timeout.
func ProbeExistingDaemon() bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:9999/metrics")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// FindDaemonBinary picks an altnet daemon binary, preferring an env
// override, then the directory next to the desktop executable, then a
// dev-time fallback that walks up to the project root.
//
// CRITICAL: Windows filesystems are case-insensitive, so a naive lookup
// for `altnet.exe` in the same directory as the GUI binary `AltNet.exe`
// resolves to the GUI binary itself. Spawning that as the "daemon" then
// triggers the GUI's own auto-start logic, which spawns another GUI,
// which... yeah. We explicitly skip any candidate that resolves to the
// running executable.
func FindDaemonBinary() (string, error) {
	self, _ := os.Executable()

	if p := os.Getenv("ALTNET_DAEMON"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("ALTNET_DAEMON=%q does not exist", p)
		}
		if isSamePath(p, self) {
			return "", fmt.Errorf("ALTNET_DAEMON points at the GUI binary itself; set it to the headless daemon executable")
		}
		return p, nil
	}

	if self == "" {
		return "", errors.New("could not find altnet daemon binary; set ALTNET_DAEMON to point at it")
	}
	dir := filepath.Dir(self)
	// 1. Same directory as the desktop app (production layout).
	if p := pickDaemon(dir, self); p != "" {
		return p, nil
	}
	// 2. Walk up looking for the dev-tree project root.
	cur := dir
	for i := 0; i < 6; i++ {
		cur = filepath.Dir(cur)
		if p := pickDaemon(cur, self); p != "" {
			return p, nil
		}
	}
	return "", errors.New("could not find altnet daemon binary; set ALTNET_DAEMON to point at it")
}

// pickDaemon returns the first existing altnet binary in dir that isn't
// the running executable, or "" if none.
func pickDaemon(dir, self string) string {
	for _, name := range []string{"altnet.exe", "altnet"} {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if isSamePath(candidate, self) {
			continue
		}
		return candidate
	}
	return ""
}

// isSamePath compares two paths case-insensitively (correct for
// Windows; harmless on case-sensitive filesystems where collisions
// don't happen anyway).
func isSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return strings.EqualFold(aa, bb)
}

// Supervisor manages one child daemon process. All methods are safe to
// call concurrently.
//
// When `adopted` is true, the supervisor doesn't own the daemon process
// — someone else (the user, a service) started it. Start() and Stop()
// become no-ops in that mode; Status() reports `Running: true` based on
// the original probe instead of a tracked Pid.
type Supervisor struct {
	cfg     *DaemonConfig
	adopted bool

	mu        sync.Mutex
	cmd       *exec.Cmd
	startedAt time.Time
	logBuf    *RingBuffer
	exited    chan struct{}
	exitErr   error
}

func NewSupervisor(cfg *DaemonConfig) *Supervisor {
	return &Supervisor{cfg: cfg, logBuf: NewRingBuffer(200)}
}

// NewAdoptedSupervisor wraps a daemon that's already running outside
// of this process — we just observe it, never spawn or kill.
func NewAdoptedSupervisor(cfg *DaemonConfig) *Supervisor {
	return &Supervisor{cfg: cfg, adopted: true, logBuf: NewRingBuffer(200), startedAt: time.Now()}
}

func (s *Supervisor) Adopted() bool { return s.adopted }

func (s *Supervisor) Start() error {
	if s.adopted {
		// The user's daemon is already running; nothing to do.
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return errors.New("daemon is already running")
	}
	if s.cfg.BinaryPath == "" {
		bin, err := FindDaemonBinary()
		if err != nil {
			return err
		}
		s.cfg.BinaryPath = bin
	}

	args := []string{
		"-listen", s.cfg.ListenAddr,
		"-data", s.cfg.DataDir,
		"-keydir", s.cfg.KeyDir,
		"-gateway", s.cfg.GatewayAddr,
		"-registrar", s.cfg.RegistrarAddr,
		"-registrar-token", s.cfg.RegistrarToken,
		"-metrics", s.cfg.MetricsAddr,
		"-log-format", "json",
	}
	// Only start the daemon's DNS resolver where we have a routing story
	// for it (Windows/Linux). On OSes still stubbed, DNSAddr is empty and
	// we omit -dns so the daemon doesn't try to bind a resolver nobody
	// routes to.
	if s.cfg.DNSAddr != "" {
		args = append(args, "-dns", s.cfg.DNSAddr)
	}
	if s.cfg.GatewayTLSAddr != "" {
		args = append(args, "-gateway-tls", s.cfg.GatewayTLSAddr)
		if s.cfg.CADir != "" {
			args = append(args, "-ca-dir", s.cfg.CADir)
		}
	}
	if s.cfg.BootstrapPeers != "" {
		args = append(args, "-bootstrap", s.cfg.BootstrapPeers)
		// Each bootstrap peer's relay port (default +100) gets passed
		// as a relay so NAT'd installs can also receive inbound. The
		// daemon ignores the flag if it can dial the bootstrap peer
		// directly, so it's harmless when not needed.
		relays := relayAddrsFromBootstrap(s.cfg.BootstrapPeers)
		if relays != "" {
			args = append(args, "-relay", relays)
		}
	}
	args = append(args, s.cfg.ExtraArgs...)

	cmd := exec.Command(s.cfg.BinaryPath, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	// The daemon's CLI reads from stdin for its interactive prompt and
	// exits on EOF. We hand it a stdin pipe and never close it -- the
	// process stays alive until Stop kills it.
	if _, err := cmd.StdinPipe(); err != nil {
		return fmt.Errorf("daemon stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	s.cmd = cmd
	s.startedAt = time.Now()
	s.exited = make(chan struct{})
	s.exitErr = nil

	// Tee both pipes into the ring buffer so the UI can show the last
	// 200 lines of daemon output.
	go s.drain(stdout)
	go s.drain(stderr)

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.exitErr = err
		s.cmd = nil
		close(s.exited)
		s.mu.Unlock()
	}()

	return nil
}

func (s *Supervisor) drain(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1024*1024)
	for sc.Scan() {
		s.logBuf.Add(sc.Text())
	}
}

func (s *Supervisor) Stop(timeout time.Duration) error {
	if s.adopted {
		// Don't kill a daemon someone else owns.
		return nil
	}
	s.mu.Lock()
	cmd := s.cmd
	exited := s.exited
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Windows doesn't support SIGINT to GUI processes; Kill is the
	// portable way to stop.
	_ = cmd.Process.Kill()
	select {
	case <-exited:
	case <-time.After(timeout):
		return errors.New("daemon did not exit within timeout")
	}
	return nil
}

// Status describes whether the daemon is up and for how long.
type Status struct {
	Running   bool   `json:"running"`
	Pid       int    `json:"pid,omitempty"`
	UptimeSec int64  `json:"uptime_sec,omitempty"`
	Binary    string `json:"binary,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (s *Supervisor) Status() Status {
	if s.adopted {
		// We trust the probe; if the user kills the daemon, this lies
		// for one poll cycle, then the metrics call will fail and the
		// UI will surface the actual state via its empty-stats path.
		return Status{
			Running:   true,
			Binary:    "(external daemon, adopted)",
			Note:      "adopted an already-running daemon",
			UptimeSec: int64(time.Since(s.startedAt).Seconds()),
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		st := Status{Binary: s.cfg.BinaryPath}
		if s.exitErr != nil {
			st.Note = "last exit: " + s.exitErr.Error()
		}
		return st
	}
	return Status{
		Running:   true,
		Pid:       s.cmd.Process.Pid,
		UptimeSec: int64(time.Since(s.startedAt).Seconds()),
		Binary:    s.cfg.BinaryPath,
	}
}

func (s *Supervisor) RecentLogs(n int) []string {
	return s.logBuf.Tail(n)
}

// RingBuffer is a tiny goroutine-safe last-N strings buffer. We use it
// to keep the most recent daemon log lines around for the UI without
// bounding memory.
type RingBuffer struct {
	mu  sync.Mutex
	buf []string
	cap int
}

func NewRingBuffer(cap int) *RingBuffer { return &RingBuffer{cap: cap} }

func (r *RingBuffer) Add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.cap {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, s)
}

func (r *RingBuffer) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.buf) {
		n = len(r.buf)
	}
	out := make([]string, n)
	copy(out, r.buf[len(r.buf)-n:])
	return out
}
