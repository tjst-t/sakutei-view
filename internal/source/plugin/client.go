package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client calls one device plugin's HTTP endpoints, per
// docs/plugin-contract.md (v0.1).
type Client struct {
	// BaseURL is the plugin's address, e.g. "http://127.0.0.1:8081" or
	// "https://plugin.example:8443". No trailing slash.
	BaseURL string

	// HTTP is used to make requests. Build it with internal/httpclient.New
	// for TLS/CA/bearer-token handling; a plain &http.Client{} works for
	// local, unauthenticated plugins such as examples/example-plugin.
	HTTP *http.Client
}

// NewClient returns a Client. httpClient may be nil, in which case
// http.DefaultClient is used (no TLS verification bypass is ever offered
// here; configure httpClient via internal/httpclient for TLS/auth).
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: httpClient}
}

// apiError is returned when the plugin responds with an unexpected status
// code and a body that does not decode as the expected shape.
type apiError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("plugin: %s %s: unexpected status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// StatusCodeOf returns the HTTP status code the plugin responded with, if
// err originated from an unexpected-status response to one of this
// client's calls (e.g. a 404 from GET /plugin/runs/{run_id} for an unknown
// run id), and true. Returns (0, false) for any other error (a transport
// failure, a cancelled context, a JSON-decoding error, ...), for which
// there is no meaningful plugin-side status code to forward.
func StatusCodeOf(err error) (int, bool) {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.StatusCode, true
	}
	return 0, false
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("plugin: building request %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin: %s %s: %w", method, path, err)
	}
	return resp, nil
}

func decodeJSON[T any](resp *http.Response, method, path string, okStatuses ...int) (T, error) {
	defer resp.Body.Close()
	var zero T

	ok := false
	for _, s := range okStatuses {
		if resp.StatusCode == s {
			ok = true
			break
		}
	}
	if !ok {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return zero, &apiError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(b)}
	}

	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return zero, fmt.Errorf("plugin: %s %s: decoding response: %w", method, path, err)
	}
	return v, nil
}

// Info calls GET /plugin.
func (c *Client) Info(ctx context.Context) (Info, error) {
	resp, err := c.do(ctx, http.MethodGet, "/plugin", nil)
	if err != nil {
		return Info{}, err
	}
	return decodeJSON[Info](resp, http.MethodGet, "/plugin", http.StatusOK)
}

// Topology calls GET /plugin/topology.
func (c *Client) Topology(ctx context.Context) (Topology, error) {
	resp, err := c.do(ctx, http.MethodGet, "/plugin/topology", nil)
	if err != nil {
		return Topology{}, err
	}
	return decodeJSON[Topology](resp, http.MethodGet, "/plugin/topology", http.StatusOK)
}

// Status calls GET /plugin/status.
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/plugin/status", nil)
	if err != nil {
		return StatusResponse{}, err
	}
	return decodeJSON[StatusResponse](resp, http.MethodGet, "/plugin/status", http.StatusOK)
}

// Scripts calls GET /plugin/scripts.
func (c *Client) Scripts(ctx context.Context) (ScriptsResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/plugin/scripts", nil)
	if err != nil {
		return ScriptsResponse{}, err
	}
	return decodeJSON[ScriptsResponse](resp, http.MethodGet, "/plugin/scripts", http.StatusOK)
}

// RunAction calls POST /plugin/scripts/{scriptID}/actions/{action}. Both the
// 202 Accepted (a new run started) and 409 Conflict (a run was already in
// progress) cases decode into ActionResult; callers should check
// ActionResult.Accepted rather than treating 409 as a Go error.
func (c *Client) RunAction(ctx context.Context, scriptID, action string) (ActionResult, error) {
	path := fmt.Sprintf("/plugin/scripts/%s/actions/%s", scriptID, action)
	resp, err := c.do(ctx, http.MethodPost, path, strings.NewReader("{}"))
	if err != nil {
		return ActionResult{}, err
	}
	return decodeJSON[ActionResult](resp, http.MethodPost, path, http.StatusAccepted, http.StatusConflict)
}

// Run calls GET /plugin/runs/{runID}.
func (c *Client) Run(ctx context.Context, runID string) (Run, error) {
	path := "/plugin/runs/" + runID
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Run{}, err
	}
	return decodeJSON[Run](resp, http.MethodGet, path, http.StatusOK)
}
