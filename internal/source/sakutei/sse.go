package sakutei

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Events subscribes to GET /events and calls onEvent for each Event
// received, in order. The daemon sends every currently-firing level event
// first (reconnecting therefore "catches up" on drift/halts that started
// before the connection), then streams new events as they happen.
//
// If the daemon reports it dropped events for this connection (its
// per-subscriber buffer overflowed because the reader was too slow), onDropped
// is called with the cumulative dropped count for the connection; onDropped
// may be nil if the caller doesn't care. Sakutei does not resend dropped
// events — per docs/read-api.md, callers should re-fetch /status and /plan
// to resynchronize after a drop.
//
// Events has no resume/Last-Event-ID mechanism: Sakutei's /events keeps no
// history beyond currently-firing events (unlike the device plugin
// contract's /plugin/events). Events blocks until ctx is cancelled, the
// stream ends, onEvent returns an error, or a transport/decoding error
// occurs.
func (c *Client) Events(ctx context.Context, onEvent func(Event) error, onDropped func(count int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/events", nil)
	if err != nil {
		return fmt.Errorf("sakutei: building events request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("sakutei: GET /events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &apiError{Path: "/events", StatusCode: resp.StatusCode, Message: string(b)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventName string
	var dataLines []string

	dispatch := func() error {
		defer func() {
			eventName = ""
			dataLines = nil
		}()
		if len(dataLines) == 0 {
			return nil // comment-only / keepalive frame
		}
		data := strings.Join(dataLines, "\n")

		if eventName == "dropped" {
			var body struct {
				Dropped int64 `json:"dropped"`
			}
			if err := json.Unmarshal([]byte(data), &body); err != nil {
				return fmt.Errorf("sakutei: decoding dropped-count event: %w", err)
			}
			if onDropped != nil {
				onDropped(body.Dropped)
			}
			return nil
		}

		var ev Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("sakutei: decoding event: %w", err)
		}
		return onEvent(ev)
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
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, ":"):
			// Comment / keepalive line (": sakutei events", ": keepalive").
		default:
			// Ignore other SSE fields (id:, retry:, ...) — not used here.
		}
	}
	if err := dispatch(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sakutei: reading events stream: %w", err)
	}
	return nil
}
