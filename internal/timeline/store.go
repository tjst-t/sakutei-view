// Package timeline persists sakutei-view's own merged event feed
// (internal/aggregate.Event) to a local append-only file, so that:
//
//   - reloading the browser page shows events that happened before the
//     page was opened;
//   - restarting the sakutei-view process does not lose history;
//   - reconnecting to the plugin's or Sakutei's event stream does not
//     record the same logical event twice.
//
// It is intentionally not a general-purpose time-series store: retention
// is bounded by count and/or age (see Config), and there is no query
// language — only "give me everything after sequence N" (Since) and
// "notify me of everything new" (Subscribe). Months-long history is
// explicitly out of scope; point an external log/metrics tool at the
// JSONL file itself (or sakutei-view's stdout) for that.
//
// Storage format: one JSON object per line (JSONL), appended to a single
// file. This avoids adding a database dependency (e.g. SQLite) just to
// keep sakutei-view a dependency-light single binary; a JSONL file is
// trivial to inspect, grep, and ship or rotate alongside the binary.
//
// See docs/event-timeline.md for the de-duplication and resume design
// this package implements.
package timeline

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
)

// DefaultMaxCount is a reasonable default for Config.MaxCount, used by
// cmd/sakutei-view when EVENTS_STORE_PATH is set but EVENTS_RETENTION_COUNT
// is not, so that enabling the timeline without tuning its retention still
// bounds the file rather than growing it forever.
const DefaultMaxCount = 10000

// Entry is one stored event: the normalized aggregate.Event plus the
// sequence number timeline assigns it. Seq is strictly increasing across
// the whole lifetime of the store's file (it is never reused or reset,
// even across a process restart or a compaction that drops old entries),
// so it is safe to use as the SSE "id:" field and as the key callers pass
// back to Since to resume where they left off — see internal/api.
type Entry struct {
	Seq   uint64          `json:"seq"`
	Event aggregate.Event `json:"event"`
}

// entryOnDisk is Entry's on-disk JSON shape. aggregate.Event.SourceEventID
// is deliberately tagged json:"-" so it is never sent to the browser (it is
// an internal resume/dedup detail — see its doc comment), but timeline
// still needs to persist it across a restart (LastPluginEventID) and
// recompute dedup keys from it on load, so it is round-tripped here as its
// own field rather than through aggregate.Event's own JSON encoding.
type entryOnDisk struct {
	Seq           uint64          `json:"seq"`
	Event         aggregate.Event `json:"event"`
	SourceEventID string          `json:"source_event_id,omitempty"`
}

// MarshalJSON implements the on-disk round-trip described on entryOnDisk.
func (e Entry) MarshalJSON() ([]byte, error) {
	return json.Marshal(entryOnDisk{Seq: e.Seq, Event: e.Event, SourceEventID: e.Event.SourceEventID})
}

// UnmarshalJSON implements the on-disk round-trip described on entryOnDisk.
func (e *Entry) UnmarshalJSON(data []byte) error {
	var d entryOnDisk
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	d.Event.SourceEventID = d.SourceEventID
	e.Seq = d.Seq
	e.Event = d.Event
	return nil
}

// Config configures a Store. MaxCount and MaxAge are independent: whichever
// one would remove more entries wins for a given entry. Leaving both at
// zero means no retention at all (the file grows forever) — cmd/sakutei-view
// applies a non-zero default so that leaving the EVENTS_RETENTION_* env vars
// unset in practice still bounds the file; see its doc comment.
type Config struct {
	// Path is the JSONL file to read from and append to. Its parent
	// directory is created if missing. Required.
	Path string

	// MaxCount, if non-zero, keeps at most this many of the most recent
	// entries; older ones are pruned.
	MaxCount int

	// MaxAge, if non-zero, prunes entries whose Event.Time is older than
	// this, regardless of MaxCount.
	MaxAge time.Duration
}

// storedEntry is an Entry plus its pre-computed de-duplication key, kept
// only in memory (the key is cheap to recompute from Entry.Event and is
// never written to disk).
type storedEntry struct {
	Entry    Entry
	dedupKey string
}

type subscriber struct {
	ch chan Entry
}

// Store is a timeline's open file plus its in-memory index. A Store is
// safe for concurrent use.
type Store struct {
	path     string
	maxCount int
	maxAge   time.Duration

	mu                sync.Mutex
	file              *os.File
	entries           []storedEntry
	dedup             map[string]struct{}
	nextSeq           uint64
	lastPluginEventID string
	subs              []*subscriber
}

// Open loads cfg.Path (if it exists) and opens it for appending. Existing
// entries are de-duplicated and retention-pruned against cfg exactly as
// Append would, so lowering MaxCount/MaxAge between runs takes effect
// immediately rather than waiting for new events.
func Open(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("timeline: Config.Path is required")
	}
	if dir := filepath.Dir(cfg.Path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("timeline: creating directory for %s: %w", cfg.Path, err)
		}
	}

	s := &Store{
		path:     cfg.Path,
		maxCount: cfg.MaxCount,
		maxAge:   cfg.MaxAge,
		dedup:    make(map[string]struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(cfg.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("timeline: opening %s: %w", cfg.Path, err)
	}
	s.file = f

	s.mu.Lock()
	if s.applyRetentionLocked(time.Now()) {
		if err := s.compactLocked(); err != nil {
			s.mu.Unlock()
			f.Close()
			return nil, err
		}
	}
	s.mu.Unlock()

	return s, nil
}

// load reads any existing file at s.path line by line into memory. A line
// that fails to parse as JSON (e.g. a torn write from a process killed
// mid-append) is logged and skipped rather than failing the whole load, so
// a single corrupt trailing line never prevents startup.
func (s *Store) load() error {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("timeline: opening %s: %w", s.path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			log.Printf("timeline: skipping unreadable line %d of %s: %v", lineNo, s.path, err)
			continue
		}
		key := dedupKeyFor(e.Event)
		s.entries = append(s.entries, storedEntry{Entry: e, dedupKey: key})
		s.dedup[key] = struct{}{}
		if e.Seq > s.nextSeq {
			s.nextSeq = e.Seq
		}
		if e.Event.Source == aggregate.EventSourcePlugin && e.Event.SourceEventID != "" {
			s.lastPluginEventID = e.Event.SourceEventID
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("timeline: reading %s: %w", s.path, err)
	}
	return nil
}

// dedupKeyFor computes the de-duplication key for ev. Plugin events carry
// their own per-event id (the "id:" field of GET /plugin/events, per
// docs/plugin-contract.md), which is used directly. Sakutei's /events has
// no per-event id at all (see internal/source/sakutei's Event type), so
// its events — and, defensively, any plugin event that unusually arrives
// without an id — are keyed by a hash of their semantic content instead.
// Sakutei resends a firing event's *original* occurrence time unchanged on
// every reconnect while it is still firing (see
// aggregate.runSakuteiEventLoop's doc comment), so hashing Event.Time along
// with the rest of the content still recognizes a resent firing event as
// the same logical occurrence rather than a new one, while a genuinely new
// occurrence (even of the same kind/resource) gets a new time and so a new
// key.
func dedupKeyFor(ev aggregate.Event) string {
	if ev.Source == aggregate.EventSourcePlugin && ev.SourceEventID != "" {
		return "plugin:id:" + ev.SourceEventID
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s",
		ev.Source, ev.Kind, ev.Severity, ev.State, ev.Resource, ev.Message,
		ev.Time.UTC().Format(time.RFC3339Nano))
	return "hash:" + hex.EncodeToString(h.Sum(nil))
}

// Append records ev if it is not a duplicate of an already-stored entry
// (see dedupKeyFor), assigning it the next sequence number and
// synchronously writing it to disk before returning, so that even a process
// killed immediately afterward does not lose it. It reports whether ev was
// actually stored (false means it was recognized as a duplicate and
// nothing changed).
func (s *Store) Append(ev aggregate.Event) (Entry, bool, error) {
	key := dedupKeyFor(ev)

	s.mu.Lock()
	if _, dup := s.dedup[key]; dup {
		s.mu.Unlock()
		return Entry{}, false, nil
	}

	seq := s.nextSeq + 1
	entry := Entry{Seq: seq, Event: ev}
	line, err := json.Marshal(entry)
	if err != nil {
		s.mu.Unlock()
		return Entry{}, false, fmt.Errorf("timeline: marshal entry: %w", err)
	}
	line = append(line, '\n')
	if _, err := s.file.Write(line); err != nil {
		s.mu.Unlock()
		return Entry{}, false, fmt.Errorf("timeline: write entry: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		// The write itself succeeded; losing the fsync race against a
		// crash in the next instant is an acceptable, documented gap
		// (see docs/event-timeline.md), not a reason to fail Append.
		log.Printf("timeline: fsync %s: %v", s.path, err)
	}

	s.nextSeq = seq
	s.dedup[key] = struct{}{}
	s.entries = append(s.entries, storedEntry{Entry: entry, dedupKey: key})
	if ev.Source == aggregate.EventSourcePlugin && ev.SourceEventID != "" {
		s.lastPluginEventID = ev.SourceEventID
	}

	if s.applyRetentionLocked(time.Now()) {
		if err := s.compactLocked(); err != nil {
			log.Printf("timeline: compacting %s after retention prune: %v", s.path, err)
		}
	}

	subs := make([]*subscriber, len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- entry:
		default:
			log.Printf("timeline: a subscriber's channel is full; dropping a live event for it (it can still catch up via Since)")
		}
	}

	return entry, true, nil
}

// applyRetentionLocked drops entries that violate maxAge and/or maxCount
// from s.entries (and their keys from s.dedup). Callers must hold s.mu. It
// reports whether anything was actually pruned, so the caller knows
// whether the on-disk file now needs compacting to match.
func (s *Store) applyRetentionLocked(now time.Time) bool {
	pruned := false

	if s.maxAge > 0 {
		cutoff := now.Add(-s.maxAge)
		i := 0
		for i < len(s.entries) && s.entries[i].Entry.Event.Time.Before(cutoff) {
			delete(s.dedup, s.entries[i].dedupKey)
			i++
		}
		if i > 0 {
			s.entries = s.entries[i:]
			pruned = true
		}
	}

	if s.maxCount > 0 && len(s.entries) > s.maxCount {
		excess := len(s.entries) - s.maxCount
		for _, e := range s.entries[:excess] {
			delete(s.dedup, e.dedupKey)
		}
		s.entries = s.entries[excess:]
		pruned = true
	}

	return pruned
}

// compactLocked rewrites the on-disk file to contain exactly s.entries,
// via a temp file plus an atomic rename, then reopens s.file for further
// appends. Callers must hold s.mu. This keeps the file from growing
// forever once retention is pruning entries in memory: without it, pruned
// entries would vanish from Since/Subscribe immediately but linger on disk
// until the next full restart's load().
func (s *Store) compactLocked() error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".timeline-*.tmp")
	if err != nil {
		return fmt.Errorf("timeline: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}

	w := bufio.NewWriter(tmp)
	for _, e := range s.entries {
		line, err := json.Marshal(e.Entry)
		if err != nil {
			cleanup()
			return fmt.Errorf("timeline: marshal during compaction: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			cleanup()
			return fmt.Errorf("timeline: writing temp file: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			cleanup()
			return fmt.Errorf("timeline: writing temp file: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		cleanup()
		return fmt.Errorf("timeline: flushing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("timeline: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("timeline: closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("timeline: renaming temp file into place: %w", err)
	}

	if s.file != nil {
		s.file.Close()
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("timeline: reopening %s after compaction: %w", s.path, err)
	}
	s.file = f
	return nil
}

// Since returns every stored entry with Seq strictly greater than seq, in
// ascending Seq order. Pass 0 to get everything currently retained. A
// caller resuming from a seq that no longer exists (because retention
// pruned it, or because the store was recreated) simply gets everything
// currently retained that is newer than that seq — not an error.
func (s *Store) Since(seq uint64) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.entries {
		if e.Entry.Seq > seq {
			out = append(out, e.Entry)
		}
	}
	return out
}

// LastPluginEventID returns the SourceEventID of the most recently
// appended plugin-sourced event (loaded from disk at Open, or recorded by
// a later Append), or "" if none has ever been recorded. A caller that
// wants the plugin's event stream to resume across a process restart
// (rather than just a mid-process reconnect, which internal/aggregate
// already handles on its own) passes this as Events' pluginResumeID.
func (s *Store) LastPluginEventID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPluginEventID
}

// Subscribe registers for every entry Append stores from now on (not
// including backlog — pair with Since for that; see internal/api's
// handleEvents for the subscribe-before-reading-backlog ordering this
// requires to avoid a gap). The returned channel is buffered; if a
// consumer falls far enough behind that the buffer fills, further live
// events are dropped for it (logged, not blocking Append) rather than
// stalling every other subscriber and every future Append. Call the
// returned cancel func when done to free the channel; it is safe to call
// more than once.
func (s *Store) Subscribe() (<-chan Entry, func()) {
	sub := &subscriber{ch: make(chan Entry, 64)}

	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			for i, x := range s.subs {
				if x == sub {
					s.subs = append(s.subs[:i], s.subs[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
			close(sub.ch)
		})
	}
	return sub.ch, cancel
}

// Close compacts (if retention would prune anything that hasn't been
// compacted away yet) and closes the underlying file. It does not close
// any channels returned by Subscribe — callers own those via their cancel
// func.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applyRetentionLocked(time.Now()) {
		if err := s.compactLocked(); err != nil {
			return err
		}
	}
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}
