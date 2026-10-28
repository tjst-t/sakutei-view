package containerlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ClabAPIClient is a Source backed directly by clab-api-server's HTTP API
// (GET /api/v1/labs/{labName} and GET /api/v1/labs/{labName}/links). It
// expects an already-issued bearer token to be configured on HTTP (see
// internal/httpclient); it does not perform clab-api-server's own
// POST /login username/password exchange itself.
type ClabAPIClient struct {
	// BaseURL is clab-api-server's address, no trailing slash.
	BaseURL string
	// LabName is the lab to query.
	LabName string
	// HTTP is used to make requests. Build it with internal/httpclient.New
	// for TLS/CA/bearer-token handling.
	HTTP *http.Client
}

// NewClabAPIClient returns a ClabAPIClient. httpClient may be nil, in which
// case http.DefaultClient is used.
func NewClabAPIClient(baseURL, labName string, httpClient *http.Client) *ClabAPIClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ClabAPIClient{BaseURL: strings.TrimRight(baseURL, "/"), LabName: labName, HTTP: httpClient}
}

// Topology fetches the lab's nodes and links from clab-api-server.
func (c *ClabAPIClient) Topology(ctx context.Context) (Topology, error) {
	var rawNodes []rawContainerInfo
	if err := c.get(ctx, "/api/v1/labs/"+c.LabName, &rawNodes); err != nil {
		return Topology{}, fmt.Errorf("fetching lab nodes: %w", err)
	}
	var rawLinks []rawLink
	if err := c.get(ctx, "/api/v1/labs/"+c.LabName+"/links", &rawLinks); err != nil {
		return Topology{}, fmt.Errorf("fetching lab links: %w", err)
	}

	nodes := make([]Node, 0, len(rawNodes))
	for _, r := range rawNodes {
		nodes = append(nodes, r.toNode())
	}
	links := make([]Link, 0, len(rawLinks))
	for _, r := range rawLinks {
		links = append(links, r.toLink())
	}
	return Topology{Nodes: nodes, Links: links}, nil
}

// apiError is returned when clab-api-server responds with a non-200 status.
type apiError struct {
	Path       string
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("clab-api-server %s: %s (status %d)", e.Path, e.Message, e.StatusCode)
	}
	return fmt.Sprintf("clab-api-server %s: unexpected status %d", e.Path, e.StatusCode)
}

func (c *ClabAPIClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// clab-api-server's error body shape is models.ErrorResponse: {"error": "..."}.
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return &apiError{Path: path, StatusCode: resp.StatusCode, Message: body.Error}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
