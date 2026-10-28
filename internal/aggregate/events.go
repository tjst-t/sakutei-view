package aggregate

import (
	"context"
	"fmt"
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
	Source   EventSource
	Time     time.Time
	Kind     string
	Severity string // Sakutei only; always empty for plugin events
	State    string // "firing" | "resolved" | ""
	Resource string
	Message  string
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
// Events blocks until ctx is cancelled, every configured source's stream
// ends, onEvent returns an error, or a source returns a transport/decoding
// error. On error from any source or from onEvent, Events cancels the other
// source's subscription and returns that error (or onEvent's).
func (a *Aggregator) Events(ctx context.Context, onEvent func(Event) error) error {
	if a.Sakutei == nil && a.Plugin == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan Event)
	errs := make(chan error, 2)
	var wg sync.WaitGroup

	send := func(ev Event) error {
		select {
		case events <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if a.Sakutei != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := a.Sakutei.Events(ctx, func(ev sakutei.Event) error {
				return send(normalizeSakuteiEvent(ev))
			}, nil)
			if err != nil && ctx.Err() == nil {
				errs <- fmt.Errorf("sakutei events: %w", err)
			}
		}()
	}
	if a.Plugin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := a.Plugin.Events(ctx, "", func(_ string, ev plugin.Event) error {
				return send(normalizePluginEvent(ev))
			})
			if err != nil && ctx.Err() == nil {
				errs <- fmt.Errorf("plugin events: %w", err)
			}
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
		case err := <-errs:
			cancel()
			<-done
			return err
		case <-done:
			select {
			case err := <-errs:
				return err
			default:
				return ctx.Err()
			}
		case <-ctx.Done():
			<-done
			return ctx.Err()
		}
	}
}
