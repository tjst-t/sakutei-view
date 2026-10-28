package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Events subscribes to GET /plugin/events and calls onEvent for each event
// received, in order, passing the event's SSE id (for later resumption) and
// the decoded Event. If lastEventID is non-empty, it is sent as the
// Last-Event-ID header so the plugin can resume from that point without
// repeating or skipping events (per docs/plugin-contract.md).
//
// Events blocks until ctx is cancelled, the stream ends, onEvent returns an
// error, or a transport/decoding error occurs. A clean cancellation via ctx
// returns ctx.Err().
func (c *Client) Events(ctx context.Context, lastEventID string, onEvent func(id string, ev Event) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/plugin/events", nil)
	if err != nil {
		return fmt.Errorf("plugin: building events request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("plugin: GET /plugin/events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &apiError{Method: http.MethodGet, Path: "/plugin/events", StatusCode: resp.StatusCode, Body: string(b)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var curID string
	var dataLines []string

	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		// Per the SSE spec, the last event ID persists across events that
		// don't carry their own "id:" line; it is only replaced by a later
		// "id:" line, never reset just because an event was dispatched. This
		// matters for callers that resume with Last-Event-ID after a drop.
		id := curID

		var ev Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("plugin: decoding event id=%q: %w", id, err)
		}
		return onEvent(id, ev)
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		switch {
		case line == "":
			if err := dispatch(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "id:"):
			curID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, ":"):
			// SSE comment / keep-alive line; ignore.
		default:
			// Ignore other SSE fields (event:, retry:, ...) — not used in
			// the v0.1 contract.
		}
	}
	// A final event with no trailing blank line is still delivered.
	if err := dispatch(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("plugin: reading events stream: %w", err)
	}
	return nil
}
