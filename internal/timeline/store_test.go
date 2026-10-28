package timeline

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
)

func sakuteiEvent(resource, message string, at time.Time) aggregate.Event {
	return aggregate.Event{
		Source:   aggregate.EventSourceSakutei,
		Time:     at,
		Kind:     "drift",
		Severity: "warning",
		State:    "firing",
		Resource: resource,
		Message:  message,
	}
}

func pluginEvent(id, resource, message string, at time.Time) aggregate.Event {
	return aggregate.Event{
		Source:        aggregate.EventSourcePlugin,
		Time:          at,
		Kind:          "restart",
		State:         "firing",
		Resource:      resource,
		Message:       message,
		SourceEventID: id,
	}
}

func TestStore_AppendAndSince(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{Path: filepath.Join(dir, "events.jsonl")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	e1, stored1, err := s.Append(sakuteiEvent("leaf1", "one", now))
	if err != nil || !stored1 {
		t.Fatalf("Append 1: entry=%+v stored=%v err=%v", e1, stored1, err)
	}
	e2, stored2, err := s.Append(sakuteiEvent("leaf2", "two", now.Add(time.Second)))
	if err != nil || !stored2 {
		t.Fatalf("Append 2: entry=%+v stored=%v err=%v", e2, stored2, err)
	}

	if e1.Seq == 0 || e2.Seq != e1.Seq+1 {
		t.Fatalf("sequence numbers not increasing: e1.Seq=%d e2.Seq=%d", e1.Seq, e2.Seq)
	}

	all := s.Since(0)
	if len(all) != 2 || all[0].Event.Message != "one" || all[1].Event.Message != "two" {
		t.Fatalf("Since(0) = %+v, want [one, two]", all)
	}

	tail := s.Since(e1.Seq)
	if len(tail) != 1 || tail[0].Event.Message != "two" {
		t.Fatalf("Since(e1.Seq) = %+v, want [two]", tail)
	}

	if len(s.Since(e2.Seq)) != 0 {
		t.Fatalf("Since(e2.Seq) should be empty, got %+v", s.Since(e2.Seq))
	}
}

// TestStore_ReloadAfterRestart_PreservesHistory simulates the page-reload
// and process-restart requirements together: after a clean Close and a
// fresh Open on the same path, previously appended history is still there
// with the same sequence numbers (so a browser's stale Last-Event-ID still
// makes sense).
func TestStore_ReloadAfterRestart_PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	now := time.Now().UTC()

	s1, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first, _, err := s1.Append(sakuteiEvent("leaf1", "before-restart", now))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer s2.Close()

	got := s2.Since(0)
	if len(got) != 1 || got[0].Event.Message != "before-restart" || got[0].Seq != first.Seq {
		t.Fatalf("Since(0) after restart = %+v, want the one entry from before restart with Seq %d", got, first.Seq)
	}

	// A new event after restart must get a Seq strictly after the
	// preserved one, not reset to 1.
	second, _, err := s2.Append(sakuteiEvent("leaf1", "after-restart", now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("Append after restart: %v", err)
	}
	if second.Seq <= first.Seq {
		t.Fatalf("Seq after restart = %d, want strictly greater than pre-restart Seq %d", second.Seq, first.Seq)
	}
}

// TestStore_SurvivesWithoutCleanClose simulates a process killed without
// ever calling Close: Append is documented to write and fsync
// synchronously, so a fresh Store opened on the same path afterward must
// still see everything that was appended.
func TestStore_SurvivesWithoutCleanClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	now := time.Now().UTC()

	s1, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := s1.Append(sakuteiEvent("leaf1", string(rune('a'+i)), now.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	// Deliberately no s1.Close() here.

	s2, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("re-Open without prior Close: %v", err)
	}
	defer s2.Close()

	got := s2.Since(0)
	if len(got) != 3 {
		t.Fatalf("Since(0) after ungraceful restart = %d entries, want 3: %+v", len(got), got)
	}
}

func TestStore_DedupPluginEventsByID(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{Path: filepath.Join(dir, "events.jsonl")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	_, stored1, err := s.Append(pluginEvent("42", "node3", "restarted", now))
	if err != nil || !stored1 {
		t.Fatalf("first Append: stored=%v err=%v", stored1, err)
	}
	// Same id, even with a different message (simulating the plugin
	// resending the identical SSE line on reconnect) — must be treated as
	// the same event.
	_, stored2, err := s.Append(pluginEvent("42", "node3", "restarted (resent)", now))
	if err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if stored2 {
		t.Fatal("second Append with the same plugin event id was stored, want it recognized as a duplicate")
	}

	if got := s.Since(0); len(got) != 1 {
		t.Fatalf("Since(0) = %+v, want exactly 1 entry", got)
	}

	// A different id is a genuinely new event.
	_, stored3, err := s.Append(pluginEvent("43", "node3", "restarted again", now.Add(time.Second)))
	if err != nil || !stored3 {
		t.Fatalf("third Append (new id): stored=%v err=%v", stored3, err)
	}
	if got := s.Since(0); len(got) != 2 {
		t.Fatalf("Since(0) = %+v, want exactly 2 entries", got)
	}
}

// TestStore_DedupSakuteiEventsByContentHash covers Sakutei's events, which
// never carry a source id at all (see internal/source/sakutei.Event):
// re-appending one with the exact same content and the exact same
// Sakutei-reported time (as happens on an /events reconnect while the
// condition is still firing) must dedup, but a genuinely new occurrence —
// even of the same kind/resource — must not.
func TestStore_DedupSakuteiEventsByContentHash(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{Path: filepath.Join(dir, "events.jsonl")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	at := time.Date(2026, 10, 28, 1, 2, 3, 0, time.UTC)
	_, stored1, err := s.Append(sakuteiEvent("leaf1", "leaf1 drifted", at))
	if err != nil || !stored1 {
		t.Fatalf("first Append: stored=%v err=%v", stored1, err)
	}

	// Identical content and identical time: a reconnect re-receiving the
	// still-firing condition.
	_, stored2, err := s.Append(sakuteiEvent("leaf1", "leaf1 drifted", at))
	if err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if stored2 {
		t.Fatal("second Append with identical content+time was stored, want it recognized as a duplicate")
	}

	// Same content but a different time: a genuinely new occurrence (e.g.
	// resolved, then fired again later) must not be deduped away.
	_, stored3, err := s.Append(sakuteiEvent("leaf1", "leaf1 drifted", at.Add(time.Hour)))
	if err != nil || !stored3 {
		t.Fatalf("third Append (new time): stored=%v err=%v", stored3, err)
	}

	if got := s.Since(0); len(got) != 2 {
		t.Fatalf("Since(0) = %+v, want exactly 2 entries", got)
	}
}

func TestStore_RetentionByCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	s, err := Open(Config{Path: path, MaxCount: 3})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if _, _, err := s.Append(sakuteiEvent("leaf1", string(rune('a'+i)), now.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	got := s.Since(0)
	if len(got) != 3 {
		t.Fatalf("Since(0) = %d entries, want 3 (MaxCount)", len(got))
	}
	want := []string{"c", "d", "e"}
	for i, e := range got {
		if e.Event.Message != want[i] {
			t.Errorf("entry %d message = %q, want %q (oldest two should have been pruned)", i, e.Event.Message, want[i])
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The pruning must also have been compacted to disk, not just kept out
	// of memory: reopening from the file must see the same 3 entries, not
	// all 5.
	s2, err := Open(Config{Path: path, MaxCount: 3})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer s2.Close()
	got2 := s2.Since(0)
	if len(got2) != 3 {
		t.Fatalf("Since(0) after reopen = %d entries, want 3 (retention must be compacted to disk)", len(got2))
	}
}

func TestStore_RetentionByAge(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{Path: filepath.Join(dir, "events.jsonl"), MaxAge: time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if _, _, err := s.Append(sakuteiEvent("leaf1", "old", now.Add(-2*time.Hour))); err != nil {
		t.Fatalf("Append old: %v", err)
	}
	if _, _, err := s.Append(sakuteiEvent("leaf1", "new", now)); err != nil {
		t.Fatalf("Append new: %v", err)
	}

	got := s.Since(0)
	if len(got) != 1 || got[0].Event.Message != "new" {
		t.Fatalf("Since(0) = %+v, want only the entry newer than MaxAge", got)
	}
}

func TestStore_Subscribe_DeliversLiveAppendsOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{Path: filepath.Join(dir, "events.jsonl")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	// Appended before Subscribe: must NOT show up on the channel (that's
	// what Since is for).
	if _, _, err := s.Append(sakuteiEvent("leaf1", "before-subscribe", now)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	ch, cancel := s.Subscribe()
	defer cancel()

	if _, _, err := s.Append(sakuteiEvent("leaf1", "after-subscribe", now.Add(time.Second))); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// A duplicate must not be published either.
	if _, dup, err := s.Append(sakuteiEvent("leaf1", "after-subscribe", now.Add(time.Second))); err != nil || dup {
		t.Fatalf("expected duplicate Append to be rejected, got stored=%v err=%v", dup, err)
	}

	select {
	case got := <-ch:
		if got.Event.Message != "after-subscribe" {
			t.Fatalf("received %+v, want the after-subscribe event", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the live event on the subscription channel")
	}

	select {
	case extra, ok := <-ch:
		if ok {
			t.Fatalf("received an unexpected extra event: %+v", extra)
		}
	case <-time.After(50 * time.Millisecond):
		// No extra delivery (e.g. the rejected duplicate) — as expected.
	}

	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
}

func TestStore_LastPluginEventID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	now := time.Now().UTC()

	s1, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s1.LastPluginEventID(); got != "" {
		t.Fatalf("LastPluginEventID on empty store = %q, want \"\"", got)
	}
	if _, _, err := s1.Append(sakuteiEvent("leaf1", "x", now)); err != nil {
		t.Fatalf("Append sakutei: %v", err)
	}
	if got := s1.LastPluginEventID(); got != "" {
		t.Fatalf("LastPluginEventID after only a sakutei event = %q, want \"\"", got)
	}
	if _, _, err := s1.Append(pluginEvent("7", "node1", "x", now)); err != nil {
		t.Fatalf("Append plugin: %v", err)
	}
	if got := s1.LastPluginEventID(); got != "7" {
		t.Fatalf("LastPluginEventID = %q, want \"7\"", got)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Must survive a restart too, since it is what cmd/sakutei-view passes
	// as the plugin's resume id on the very next process start.
	s2, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer s2.Close()
	if got := s2.LastPluginEventID(); got != "7" {
		t.Fatalf("LastPluginEventID after restart = %q, want \"7\"", got)
	}
}

func TestStore_RequiresPath(t *testing.T) {
	if _, err := Open(Config{}); err == nil {
		t.Fatal("Open with empty Path should fail")
	}
}
