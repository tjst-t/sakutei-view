package aggregate

import (
	"context"
	"sync"
	"time"

	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/source/sakutei"
)

// EventSource identifies which data source produced an Event.
type EventSource string

const (
	EventSourceSakutei EventSource = "sakutei"
	EventSourcePlugin  EventSource = "plugin"
)

// Event is one normalized event, merged from either Sakutei's /events or
// the plugin's /plugin/events. State carries the "firing"/"resolved"
// distinction both sources make (Sakutei's Event.State, the plugin's
// Event.Level); it is empty for one-shot events that have no such notion.
type Event struct {
	Source   EventSource `json:"source"`
	Time     time.Time   `json:"time"`
	Kind     string      `json:"kind"`
	Severity string      `json:"severity,omitempty"` // Sakutei only; always empty for plugin events
	State    string      `json:"state,omitempty"`    // "firing" | "resolved" | ""
	Resource string      `json:"resource,omitempty"`
	Message  string      `json:"message"`

	// SourceEventID is the originating source's own per-event id, when it
	// has one. Currently only the plugin sets this (the "id:" field of its
	// GET /plugin/events SSE stream, per docs/plugin-contract.md); Sakutei's
	// /events has no per-event id at all, so this is always empty for
	// EventSourceSakutei. Not sent to the browser — it exists only so
	// internal/timeline can de-duplicate and resume the plugin's stream
	// across a process restart, not just a mid-process reconnect.
	SourceEventID string `json:"-"`
}

func normalizeSakuteiEvent(ev sakutei.Event) Event {
	t, _ := time.Parse(time.RFC3339, ev.Time) // zero Time on parse failure; caller still gets the event
	return Event{
		Source:   EventSourceSakutei,
		Time:     t,
		Kind:     ev.Kind,
		Severity: ev.Severity,
		State:    ev.State,
		Resource: ev.Resource,
		Message:  ev.Message,
	}
}

func normalizePluginEvent(ev plugin.Event) Event {
	return Event{
		Source:   EventSourcePlugin,
		Time:     ev.At,
		Kind:     ev.Kind,
		State:    ev.Level,
		Resource: ev.ResourceID,
		Message:  ev.Message,
	}
}

// Events subscribes to whichever of Sakutei's /events and the plugin's
// /plugin/events are configured, and calls onEvent for each normalized
// Event as it arrives. Events from the two sources are delivered as they
// arrive (not sorted or buffered against each other); onEvent is never
// called concurrently.
//
// pluginResumeID, if non-empty, is sent as the very first Last-Event-ID to
// the plugin's /plugin/events — not just after a reconnect. A caller that
// persists plugin events across process restarts (internal/timeline) passes
// the last event id it previously recorded here, so a plugin that honors
// Last-Event-ID resume (required by docs/plugin-contract.md) does not
// redeliver events from before this process started. Pass "" to start from
// the plugin's live tail, as before this parameter existed.
//
// Each source's subscription is independent and self-healing: if its SSE
// connection drops or fails to establish, Events reconnects it after a
// backoff that starts at ReconnectInitial and doubles up to ReconnectMax,
// resetting once the stream has been up for a while. One source being
// permanently down does not stop the other from delivering events, and
// does not make Events return — only ctx cancellation or onEvent returning
// an error does that. Each attempt's outcome is recorded in the
// Aggregator's Health (see Snapshot.Health), so a caller polling Snapshot
// concurrently can tell "this source isn't being read right now" apart
// from "this source isn't configured".
//
// For the plugin's /plugin/events, Events resumes after a reconnect by
// sending the last event ID it received as Last-Event-ID, per
// docs/plugin-contract.md, so no events are replayed or skipped across a
// reconnect. Sakutei's /events has no such resume mechanism (see
// sakutei.Client.Events's doc comment): each reconnect simply re-receives
// whatever is currently firing.
//
// Events blocks until ctx is cancelled or onEvent returns an error. On
// onEvent's error, Events stops both subscriptions and returns that error.
func (a *Aggregator) Events(ctx context.Context, pluginResumeID string, onEvent func(Event) error) error {
	if a.Sakutei == nil && a.Plugin == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan Event)

	send := func(ev Event) error {
		select {
		case events <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	var wg sync.WaitGroup

	if a.Sakutei != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.runSakuteiEventLoop(ctx, send)
		}()
	}
	if a.Plugin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.runPluginEventLoop(ctx, pluginResumeID, send)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	for {
		select {
		case ev := <-events:
			if err := onEvent(ev); err != nil {
				cancel()
				<-done
				return err
			}
		case <-ctx.Done():
			<-done
			return ctx.Err()
		}
	}
}

// runSakuteiEventLoop subscribes to Sakutei's /events and keeps
// reconnecting (with backoff) until ctx is done. It never returns an error
// itself; failures are recorded via recordError and the loop just retries.
func (a *Aggregator) runSakuteiEventLoop(ctx context.Context, send func(Event) error) {
	backoff := a.reconnectInitial()
	for ctx.Err() == nil {
		start := time.Now()
		var sawEvent bool
		err := a.Sakutei.Events(ctx, func(ev sakutei.Event) error {
			sawEvent = true
			a.recordSuccess(SourceSakutei)
			return send(normalizeSakuteiEvent(ev))
		}, nil)
		if ctx.Err() != nil {
			return
		}

		switch {
		case err == nil:
			// The stream ended cleanly (server closed it) without ever
			// erroring; treat the source as reachable.
			a.recordSuccess(SourceSakutei)
		default:
			a.recordError(SourceSakutei, err)
		}
		// A connection that stayed up long enough to be worth resetting
		// backoff for (whether or not it ultimately errored) — avoids a
		// single late failure after a long healthy stream being treated
		// like a tight failure loop.
		if sawEvent || time.Since(start) >= a.reconnectInitial() {
			backoff = a.reconnectInitial()
		}

		if !sleepBackoff(ctx, &backoff, a.reconnectMax()) {
			return
		}
	}
}

// runPluginEventLoop subscribes to the plugin's /plugin/events and keeps
// reconnecting (with backoff) until ctx is done, resuming from the last
// event ID it saw via Last-Event-ID (starting from resumeID, if non-empty,
// on the very first connection). It never returns an error itself;
// failures are recorded via recordError and the loop just retries.
func (a *Aggregator) runPluginEventLoop(ctx context.Context, resumeID string, send func(Event) error) {
	backoff := a.reconnectInitial()
	lastEventID := resumeID
	for ctx.Err() == nil {
		start := time.Now()
		var sawEvent bool
		err := a.Plugin.Events(ctx, lastEventID, func(id string, ev plugin.Event) error {
			sawEvent = true
			if id != "" {
				lastEventID = id
			}
			a.recordSuccess(SourcePlugin)
			ev2 := normalizePluginEvent(ev)
			ev2.SourceEventID = id
			return send(ev2)
		})
		if ctx.Err() != nil {
			return
		}

		switch {
		case err == nil:
			a.recordSuccess(SourcePlugin)
		default:
			a.recordError(SourcePlugin, err)
		}
		if sawEvent || time.Since(start) >= a.reconnectInitial() {
			backoff = a.reconnectInitial()
		}

		if !sleepBackoff(ctx, &backoff, a.reconnectMax()) {
			return
		}
	}
}

// sleepBackoff waits for *backoff (or until ctx is done, whichever comes
// first), then doubles *backoff up to max for the next call. It returns
// false if ctx was done instead of the wait completing, meaning the caller
// should stop retrying.
func sleepBackoff(ctx context.Context, backoff *time.Duration, max time.Duration) bool {
	timer := time.NewTimer(*backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return false
	}
	next := *backoff * 2
	if next > max {
		next = max
	}
	*backoff = next
	return true
}
