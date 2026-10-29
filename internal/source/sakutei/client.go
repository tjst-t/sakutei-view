package sakutei

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// Client calls one Sakutei daemon's read-only API (--read-listen), per
// docs/read-api.md.
type Client struct {
	// BaseURL is the daemon's read-only address, e.g.
	// "http://127.0.0.1:9471" or "https://sakutei.example:9471".
	BaseURL string

	// HTTP is used to make requests. Build it with internal/httpclient.New
	// for TLS/CA/bearer-token handling.
	HTTP *http.Client
}

// NewClient returns a Client. httpClient may be nil, in which case
// http.DefaultClient is used.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: httpClient}
}

// apiError is returned for unexpected status codes. The read-only API
// reports errors as {"error": "..."}; NotYet reports the well-known
// "no cycle has observed reality yet" 503 case so callers can distinguish
// "daemon just started" from a real failure.
type apiError struct {
	Path       string
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("sakutei: GET %s: %d %s", e.Path, e.StatusCode, e.Message)
}

// NotYet reports whether err is the daemon's "no cycle has observed reality
// yet" response (HTTP 503, sent right after the daemon starts, before its
// first cycle finishes).
func NotYet(err error) bool {
	var ae *apiError
	return asAPIError(err, &ae) && ae.StatusCode == http.StatusServiceUnavailable
}

func asAPIError(err error, target **apiError) bool {
	ae, ok := err.(*apiError)
	if ok {
		*target = ae
	}
	return ok
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("sakutei: building request GET %s: %w", path, err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sakutei: GET %s: %w", path, err)
	}
	return resp, nil
}

func decodeJSON[T any](resp *http.Response, path string) (T, error) {
	defer resp.Body.Close()
	var zero T

	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = json.Unmarshal(b, &body)
		msg := body.Error
		if msg == "" {
			msg = string(b)
		}
		return zero, &apiError{Path: path, StatusCode: resp.StatusCode, Message: msg}
	}

	var v T
	err := json.NewDecoder(resp.Body).Decode(&v)
	if err == nil {
		return v, nil
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		// One field's JSON value had a shape sakutei-view doesn't recognize
		// for its Go type (e.g. a Sakutei version change). encoding/json
		// skips that field and decodes every other field as usual, so v is
		// still usable -- do not throw the whole response away over one
		// unrecognized field.
		log.Printf("sakutei: GET %s: field %q has an unexpected shape (want %s), ignoring it: %v",
			path, typeErr.Field, typeErr.Type, typeErr)
		return v, nil
	}
	return zero, fmt.Errorf("sakutei: GET %s: decoding response: %w", path, err)
}

// Status calls GET /status.
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	resp, err := c.get(ctx, "/status")
	if err != nil {
		return StatusResponse{}, err
	}
	return decodeJSON[StatusResponse](resp, "/status")
}

// Plan calls GET /plan.
func (c *Client) Plan(ctx context.Context) (PlanResponse, error) {
	resp, err := c.get(ctx, "/plan")
	if err != nil {
		return PlanResponse{}, err
	}
	return decodeJSON[PlanResponse](resp, "/plan")
}

// Graph calls GET /graph.
func (c *Client) Graph(ctx context.Context) (GraphResponse, error) {
	resp, err := c.get(ctx, "/graph")
	if err != nil {
		return GraphResponse{}, err
	}
	return decodeJSON[GraphResponse](resp, "/graph")
}
