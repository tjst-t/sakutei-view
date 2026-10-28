# Event timeline (persisted history)

`GET /api/events` is, by default, a bare live pass-through: it only shows
events that happen while a browser tab is connected. Setting
`EVENTS_STORE_PATH` turns on the `internal/timeline` package, which persists
the merged event feed to a local file so that:

- reopening or reloading the page shows events that happened before it was
  opened;
- restarting the `sakutei-view` process does not lose history;
- reconnecting to the plugin's or Sakutei's event stream does not record the
  same logical event twice.

This document describes the design and the trade-offs it deliberately makes.
It is not a general-purpose observability store — see "Out of scope" below.

## Configuration

| Env var | Meaning | Default |
| --- | --- | --- |
| `EVENTS_STORE_PATH` | Path to the JSONL file. Unset disables persistence entirely (legacy live-only behavior). | unset |
| `EVENTS_RETENTION_COUNT` | Keep at most this many of the most recent entries. `0` disables count-based retention. | `10000` (applied only when `EVENTS_STORE_PATH` is set and this var is unset) |
| `EVENTS_RETENTION_AGE` | Prune entries older than this (a Go duration, e.g. `72h`). Unset disables age-based retention. | unset |

The two retention settings are independent; whichever would remove more
entries for a given entry wins. Leaving both at `0`/unset is possible but not
the default — it is treated as an explicit choice to let the file grow
without bound.

## Storage format

One JSON object per line (JSONL), appended to a single file. This was chosen
over a database (e.g. SQLite) specifically to avoid adding a new third-party
Go dependency — `sakutei-view` ships as a single, dependency-light binary,
and a JSONL file is trivial to inspect (`tail -f`, `jq`), grep, back up, or
rotate externally without any tooling specific to this project.

Each line is `{"seq": <uint64>, "event": {...aggregate.Event...},
"source_event_id": "<string, omitted if empty>"}`. `seq` is assigned by the
store, strictly increasing for the whole lifetime of the file (never reused
or renumbered, even across a restart or a compaction that drops old
entries), and is what callers resume from (see "Resuming" below) — it is
unrelated to the plugin's own per-event id, which is instead round-tripped
separately as `source_event_id` (see "Why `source_event_id` is a separate
field" below).

Writes are synchronous: `Append` writes the line and calls `fsync` before
returning. An `fsync` failure is logged but does not fail `Append` — losing
the fsync race against a crash in the exact next instant is an accepted,
documented gap, not a correctness guarantee this package makes.

### Why `source_event_id` is a separate on-disk field

`aggregate.Event.SourceEventID` is tagged `json:"-"` so it is never sent to
the browser in `/api/events`' payload — it is an internal resume/dedup
detail, not something the UI needs. Because of that tag, naively running
`aggregate.Event` (embedded in `timeline.Entry`) through the standard
`encoding/json` marshaler for on-disk storage would silently drop it on
every write, and reloading from disk would always produce an empty
`SourceEventID` — breaking resume-after-restart for the plugin's stream and,
for plugin events specifically, correctness of dedup on reload. `timeline`
avoids this with custom `MarshalJSON`/`UnmarshalJSON` methods on `Entry`
that copy the value into/out of its own `source_event_id` JSON field, while
leaving `aggregate.Event`'s own (browser-facing) JSON encoding untouched.

## De-duplication strategy

Reconnecting to an upstream event stream (the plugin's SSE, or Sakutei's
`/events`) can resend an event already seen. `timeline.Append` silently
drops anything it recognizes as a duplicate of an already-stored entry,
using one of two keys depending on the event's source:

- **Plugin events**: keyed by the plugin's own per-event SSE `id` (the
  `id:` field of `GET /plugin/events`, per `docs/plugin-contract.md`),
  carried internally as `aggregate.Event.SourceEventID`. The plugin
  contract requires this id to be stable and unique per logical event, so
  it is used directly: `"plugin:id:" + SourceEventID`.

- **Sakutei events, and any plugin event that unusually arrives without an
  id**: Sakutei's `/events` has no per-event id at all (see
  `internal/source/sakutei`'s `Event` type), so these are keyed by a SHA-256
  hash of their semantic content instead:
  `source|kind|severity|state|resource|message|time(RFC3339Nano, UTC)`.

  This relies on an observed property of Sakutei's `/events`: it resends a
  still-firing event's *original* occurrence time unchanged on every
  reconnect (see `aggregate.runSakuteiEventLoop`'s doc comment) rather than
  stamping a new time on each resend. Hashing `Time` along with the rest of
  the content therefore still recognizes a resent firing event as the same
  logical occurrence (same key, so it's dropped as a duplicate), while a
  genuinely new occurrence — even of the same kind/resource — gets a new
  `Time` and so a new key, and is correctly stored as a new entry.

Both the in-memory dedup index and the keys themselves are rebuilt from the
on-disk file on every `Open`, so dedup correctness survives a process
restart, not just an in-process reconnect.

## Resuming the plugin's stream across a restart

Within a single process run, `internal/aggregate.Aggregator.Events` already
retries the plugin's SSE with `Last-Event-ID` on a dropped connection. What
`timeline` adds is resuming *across a process restart*: `Store` tracks
`LastPluginEventID()` (the `SourceEventID` of the most recently appended
plugin event, loaded from disk at `Open` and updated by every later
`Append`), and `cmd/sakutei-view`'s event pump (`api.Server.RunEventPump`)
passes it as `Events`' `pluginResumeID` parameter, which is sent as
`Last-Event-ID` on the plugin's very first connection of the new process —
not only on an in-process reconnect.

Sakutei's `/events` has no id, so there is nothing to resume there; a
restart simply re-subscribes and relies on content-hash dedup (above) to
avoid re-storing anything Sakutei resends.

## Serving `/api/events` from the timeline

A single background goroutine (`api.Server.RunEventPump`, started once from
`cmd/sakutei-view`) owns the one upstream subscription to
`Aggregator.Events` and persists everything it receives via
`Timeline.Append`. This runs independently of whether any browser tab is
connected — without it, an event that occurred while no tab was open would
never be recorded.

Each browser's own `GET /api/events` connection, when `Timeline` is
configured, does not open its own upstream connection at all. Instead it:

1. Subscribes to `Timeline.Subscribe()` for live events.
2. Replays `Timeline.Since(lastEventID)` as backlog, sent as ordinary SSE
   `data:` frames each preceded by an `id: <seq>` line.
3. Forwards the live feed from step 1, skipping anything with
   `Seq <= lastSent` (the last backlog entry sent), since the backlog may
   overlap the start of the live feed — step 1 happens *before* step 2
   specifically so that an event arriving in the gap between them is not
   lost; the resulting possible double-delivery is what the `lastSent`
   check filters back out.

`lastEventID` comes from the browser's `Last-Event-ID` request header
(sent automatically by `EventSource` on reconnect, and also usable by a
fresh tab that remembers the last id it saw). If missing or unparsable,
backlog starts from the beginning of what's retained.

If `Timeline` is `nil` (the default), `handleEvents` is unchanged from
before this feature existed: a direct per-connection pass-through of
`Aggregator.Events`, with no backlog and no `id:` lines.

## Retention and compaction

`Store.Append` and `Store.Close` both check retention (`MaxAge`/`MaxCount`)
after every change. When pruning actually removes anything, the whole file
is rewritten via a temp file plus an atomic `os.Rename` (so a crash
mid-compaction cannot corrupt the file — the old file remains intact until
the rename succeeds), preserving each surviving entry's original `seq`
rather than renumbering.

## Out of scope

Months-long history is explicitly not a goal of this package. There is no
query language beyond "everything after sequence N" and "everything new
from now on" — no filtering, aggregation, or export. If longer-term history
or analysis is needed, point an external log/metrics tool at the JSONL file
itself (it is just JSON Lines) or at `sakutei-view`'s stdout, rather than
extending this package to do that job.

## Testing

`internal/timeline/store_test.go` directly exercises each of this feature's
acceptance criteria: reopening/reloading shows history
(`TestStore_AppendAndSince`, `TestStore_ReloadAfterRestart_PreservesHistory`),
restarting preserves history (`TestStore_ReloadAfterRestart_PreservesHistory`,
`TestStore_SurvivesWithoutCleanClose`, `TestStore_LastPluginEventID`),
reconnecting does not duplicate (`TestStore_DedupPluginEventsByID`,
`TestStore_DedupSakuteiEventsByContentHash`), and the retention limit expires
old entries (`TestStore_RetentionByCount`, `TestStore_RetentionByAge`).
`internal/api/api_test.go` additionally covers the `/api/events` HTTP-level
backlog replay and `Last-Event-ID` resume behavior, and the event pump's
wiring of `Timeline.LastPluginEventID()` into `Events`' `pluginResumeID`.
