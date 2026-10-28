# Device plugin contract — v0.1

sakutei-view reads two kinds of data it does not generate itself:
containerlab's state and Sakutei's state. A third, optional kind — a
**device plugin** — lets a deployment add its own device state and demo
scripts without sakutei-view knowing anything about that device.

A device plugin is a small HTTP service that the deployment writes and
runs itself (in its own, typically private, repository). sakutei-view only
needs this contract to be able to read it. This document is the contract.
It is versioned independently of sakutei-view itself; a plugin declares
the contract version it implements, and sakutei-view will refuse to use a
plugin whose major version it does not understand.

This version is **0.1** — pre-1.0, and may still change in
backwards-incompatible ways before 1.0.

## Conventions

- All endpoints are plain HTTP/1.1 (or HTTP/2), JSON in and out, unless
  noted otherwise.
- Timestamps are RFC 3339 (`2026-10-28T06:00:00Z`).
- When sakutei-view calls a plugin, it sends a bearer token:
  `Authorization: Bearer <token>`. The token is configured on the
  sakutei-view side; a plugin may ignore it (for local examples/demos) or
  enforce it (recommended for anything reachable beyond localhost).
- Write-ish endpoints (`POST .../actions/...`) are the only ones that
  cause the plugin to do something; everything else is a read.
- `kind` values for state/events are free-form strings chosen by the
  plugin author (e.g. `storage`, `tape`, `bmc`). sakutei-view does not
  special-case any of them in v1 beyond choosing an icon/color if it
  recognizes one; unrecognized kinds get a generic rendering.
- Event `level` follows the same two values Sakutei itself uses:
  `firing` (something started/is true now) and `resolved` (it stopped/is
  no longer true). This lets sakutei-view merge plugin events and Sakutei
  events into one timeline without special-casing either source.

## `GET /plugin`

Capability/version discovery. Always the first call sakutei-view makes to
a configured plugin.

```json
{
  "version": "0.1",
  "name": "example-plugin",
  "capabilities": ["topology", "status", "scripts", "events"]
}
```

- `version`: the contract version this plugin implements (`"0.1"` here).
- `capabilities`: which of the optional endpoint groups below are
  implemented. `status` and `events` are expected from essentially every
  plugin; `topology` and `scripts` are optional (a plugin that only adds
  device state, with no demo scripts and no containerlab data of its own,
  can omit them).

## `GET /plugin/topology` (optional — `capabilities: ["topology"]`)

Returns containerlab's topology and state as-is, for deployments that
would rather not run a persistent `clab-api-server`. This is an
alternative to sakutei-view talking to `clab-api-server` directly; a
deployment picks one data source for containerlab data, not both.

```json
{
  "inspect": { },
  "clab_yaml": "name: demo\ntopology:\n  nodes:\n    ...\n"
}
```

- `inspect`: the raw JSON object produced by `containerlab inspect
  --format json` (shape defined by containerlab itself, passed through
  unmodified).
- `clab_yaml`: the generated `.clab.yml` file's contents, as a plain
  string.

## `GET /plugin/status`

The plugin's own view of its device(s)' state — the analogue of
Sakutei's `/status` for things Sakutei does not itself model.

```json
{
  "generated_at": "2026-10-28T06:00:00Z",
  "resources": [
    {
      "id": "demo-storage-01",
      "kind": "storage",
      "label": "Demo storage array",
      "state": "ok",
      "node": "storage01",
      "fields": { "lun_count": 4, "capacity_gb": 500 },
      "drifted_fields": []
    }
  ]
}
```

- `resources[].id`: stable identifier, unique within this plugin.
- `resources[].state`: one of `ok`, `drifted`, `unknown`, `error`.
- `resources[].node`: the containerlab node name this resource belongs
  to, so sakutei-view can draw its state on the matching topology node.
  Empty string if there is no corresponding containerlab node.
- `resources[].fields`: free-form key/value pairs, shown as-is in the
  resource's detail panel.
- `resources[].drifted_fields`: subset of the keys in `fields` that are
  currently drifted (differ from what is desired), if `state` is
  `"drifted"`. Empty otherwise.

## `GET /plugin/scripts` (optional — `capabilities: ["scripts"]`)

The list of demo scripts this plugin offers. A script groups one or more
named actions (typically `disturb` and `fix`) that a human can trigger
from the UI during a demo.

```json
{
  "scripts": [
    {
      "id": "lun-drift",
      "label": "Change the LUN count",
      "description": "Changes demo-storage-01's LUN count away from what is declared.",
      "actions": ["disturb", "fix"]
    }
  ]
}
```

## `POST /plugin/scripts/{id}/actions/{action}`

Runs one action of one script. Fire-and-forget: the call starts the
action and returns immediately with a run id to poll or to correlate
against `/plugin/events`.

- Request body: empty, or `{}`. (Reserved for future parameters.)
- Response: `202 Accepted`

```json
{ "accepted": true, "run_id": "r-0001" }
```

- Only **one run may be in progress at a time**, per plugin. If a run is
  already in progress, the plugin responds `409 Conflict`:

```json
{ "accepted": false, "reason": "another run is already in progress", "run_id": "r-0001" }
```

## `GET /plugin/runs/{run_id}`

Status of a previously started run.

```json
{
  "run_id": "r-0001",
  "script_id": "lun-drift",
  "action": "disturb",
  "status": "running",
  "started_at": "2026-10-28T06:00:05Z",
  "finished_at": null,
  "message": ""
}
```

- `status`: one of `running`, `succeeded`, `failed`.
- `finished_at` / `message` are populated once the run is no longer
  `running`.

## `GET /plugin/events` (Server-Sent Events)

A live stream of this plugin's own events, merged by sakutei-view into
the same timeline as Sakutei's `/events`.

Each SSE message carries an `id:` field (a monotonically increasing or
otherwise orderable string) so a client can resume with
`Last-Event-ID` after a reconnect without missing or duplicating events.
The plugin should keep a short backlog (at least the last few minutes)
to serve those resumes.

```
id: 42
data: {"kind":"Disturbed","level":"firing","resource_id":"demo-storage-01","message":"LUN count changed from 4 to 6","at":"2026-10-28T06:00:05Z"}

id: 43
data: {"kind":"Fixed","level":"resolved","resource_id":"demo-storage-01","message":"Sakutei restored LUN count to 4","at":"2026-10-28T06:00:12Z"}
```

- `kind`: free-form, chosen by the plugin (e.g. `Disturbed`, `Fixed`, or
  anything else that makes sense for the device).
- `level`: `firing` or `resolved`, as above.
- `resource_id`: matches a `resources[].id` from `/plugin/status`, if the
  event is about a specific resource. May be omitted for general events.

## Minimal example

See [examples/example-plugin](../examples/example-plugin) for a complete,
dependency-free Go implementation of this contract with static/fake data,
useful as a starting point and for exercising sakutei-view without a real
device behind it.
