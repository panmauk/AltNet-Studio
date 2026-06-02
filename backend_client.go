package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BackendClient is a thin HTTP client for the AltNet auth/registry
// service. The desktop app talks to it from Go (not from JS) so we
// don't need CORS and we keep the bearer token off the renderer.
type BackendClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewBackendClient(baseURL string) *BackendClient {
	return &BackendClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

type backendError struct {
	Status int
	Msg    string
}

func (e *backendError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("backend returned status %d", e.Status)
}

// IsBackendUnreachable lets the UI distinguish "wrong password" from
// "the server is down."
func IsBackendUnreachable(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*backendError)
	return !ok // any non-backendError came from net/http (dial, timeout, etc.)
}

func (c *BackendClient) post(path string, body, out any, token string) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readResponse(resp, out)
}

func (c *BackendClient) get(path string, out any, token string) error {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readResponse(resp, out)
}

func readResponse(resp *http.Response, out any) error {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var er struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &er)
		return &backendError{Status: resp.StatusCode, Msg: er.Error}
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

// --- API surface ---

type SessionResp struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Email     string `json:"email"`
	IsAdmin   bool   `json:"is_admin"`
}

func (c *BackendClient) Signup(email, password string) error {
	return c.post("/v1/signup", map[string]string{"email": email, "password": password}, nil, "")
}

func (c *BackendClient) Verify(email, code string) (*SessionResp, error) {
	var s SessionResp
	if err := c.post("/v1/verify", map[string]string{"email": email, "code": code}, &s, ""); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *BackendClient) Login(email, password string) (*SessionResp, error) {
	var s SessionResp
	if err := c.post("/v1/login", map[string]string{"email": email, "password": password}, &s, ""); err != nil {
		return nil, err
	}
	return &s, nil
}

type MeResp struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
	IsAdmin  bool   `json:"is_admin"`
	Created  int64  `json:"created"`
}

func (c *BackendClient) Me(token string) (*MeResp, error) {
	var m MeResp
	if err := c.get("/v1/me", &m, token); err != nil {
		return nil, err
	}
	return &m, nil
}

// DomainRow mirrors the JSON the backend returns for one domain
// request -- whether it's the user's own list or admin's pending queue.
type DomainRow struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"` // pending | approved | declined
	UserEmail   string `json:"user_email,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	DecidedAt   int64  `json:"decided_at,omitempty"`
}

func (c *BackendClient) RequestDomain(token, name, description, root string) (*DomainRow, error) {
	var out DomainRow
	if err := c.post("/v1/domains/request",
		map[string]string{"name": name, "description": description, "root": root}, &out, token); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAccount permanently removes the signed-in user.
func (c *BackendClient) DeleteAccount(token string) error {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+"/v1/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readResponse(resp, nil)
}

func (c *BackendClient) MyDomains(token string) ([]DomainRow, error) {
	var out struct {
		Domains []DomainRow `json:"domains"`
	}
	if err := c.get("/v1/domains/mine", &out, token); err != nil {
		return nil, err
	}
	return out.Domains, nil
}

func (c *BackendClient) AdminPending(token string) ([]DomainRow, error) {
	var out struct {
		Pending []DomainRow `json:"pending"`
	}
	if err := c.get("/v1/admin/pending", &out, token); err != nil {
		return nil, err
	}
	return out.Pending, nil
}

func (c *BackendClient) AdminDecide(token string, id int64, decision string) error {
	return c.post("/v1/admin/decisions",
		map[string]any{"id": id, "decision": decision}, nil, token)
}

func (c *BackendClient) RequestPasswordReset(email string) error {
	return c.post("/v1/password/request-reset",
		map[string]string{"email": email}, nil, "")
}

func (c *BackendClient) ConfirmPasswordReset(email, code, newPassword string) error {
	return c.post("/v1/password/confirm-reset", map[string]string{
		"email": email, "code": code, "new_password": newPassword,
	}, nil, "")
}

// AbuseReportRow mirrors the backend's abuse_reports row, JSON-friendly.
type AbuseReportRow struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	ReporterUserID *int64 `json:"reporter_user_id,omitempty"`
	ReporterEmail  string `json:"reporter_email,omitempty"`
	Reason         string `json:"reason"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at"`
	DecidedAt      *int64 `json:"decided_at,omitempty"`
	DecidedBy      *int64 `json:"decided_by,omitempty"`
	DecisionNote   string `json:"decision_note,omitempty"`
}

// SubmitReport flags a .alt name as hosting bad content.
func (c *BackendClient) SubmitReport(token, name, reason string) (*AbuseReportRow, error) {
	var out AbuseReportRow
	if err := c.post("/v1/reports",
		map[string]string{"name": name, "reason": reason}, &out, token); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminReports returns the pending abuse-report queue.
func (c *BackendClient) AdminReports(token string) ([]AbuseReportRow, error) {
	var out struct {
		Reports []AbuseReportRow `json:"reports"`
	}
	if err := c.get("/v1/admin/reports", &out, token); err != nil {
		return nil, err
	}
	return out.Reports, nil
}

// AdminDecideReport stamps a revoke/dismiss decision on a pending
// report. On "revoke" the caller is responsible for separately
// asking the local daemon to broadcast the dht_revoke message.
func (c *BackendClient) AdminDecideReport(token string, id int64, decision, note string) error {
	return c.post("/v1/admin/reports/decide",
		map[string]any{"id": id, "decision": decision, "note": note}, nil, token)
}

// AdminRevoke takes a .alt name down network-wide via the backend's
// authority-registrar bridge. Used by the admin "search a domain, take it
// down" flow in Studio.
func (c *BackendClient) AdminRevoke(token, name string) error {
	return c.post("/v1/admin/revoke", map[string]any{"name": name}, nil, token)
}
