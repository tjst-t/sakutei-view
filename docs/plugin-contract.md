# Device plugin contract — v0.3

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

This document describes **0.3**. It is still pre-1.0 and may change in
backwards-incompatible ways before 1.0, but 0.1, 0.2 and 0.3 are all
compatible with each other in practice: every field a later version adds
is optional, and a sakutei-view that understands 0.3 also understands a
0.1 or 0.2 plugin's responses (the newer fields simply come back empty).
A 0.1 or 0.2 plugin needs no changes to keep working; see "Changes in
v0.3" and "Changes in v0.2" below if you are writing or updating a
plugin.

## Conventions

- All endpoints are plain HTTP/1.1 (or HTTP/2), JSON in and out, unless
  noted otherwise.
- Timestamps are RFC 3339 (`2026-10-28T06:00:00Z`).
- When sakutei-view calls a plugin, it sends a bearer token:
  `Authorization: Bearer <token>`. The token is configured on the
  sakutei-view side; a plugin may ignore it (for local examples/demos) or
  enforce it (recommended for anything reachable beyond localhost).
- A plugin may be served over plain HTTP (for local examples/demos) or
  HTTPS. For HTTPS, sakutei-view verifies the plugin's certificate; if the
  plugin uses a private CA, that CA's certificate must be configured on
  the sakutei-view side. sakutei-view has no option to skip certificate
  verification, so a plugin reachable beyond localhost should use either a
  publicly-trusted certificate or a CA the deployment is willing to
  configure on the sakutei-view side.
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

## Changes in v0.3

- **`GET /plugin/status`'s `resources[].node` field is clarified**: it
  must be the *topology* node name — the same identifier a `.clab.yml`
  uses as a node's key, and that containerlab's own `inspect` output
  turns into a container named `clab-<lab-name>-<node>`. It is not that
  container name. This was always the intended meaning; this is a wording
  clarification, not a behavior change, so it does not require any plugin
  to change anything.
- **`GET /plugin/status` gained an optional `sakutei_bindings` field**,
  letting a plugin explicitly map a Sakutei resource id to the topology
  node it belongs to:

  ```json
  {
    "generated_at": "2026-10-28T06:00:00Z",
    "resources": [],
    "sakutei_bindings": [
      { "resource_id": "res-nftables-fw1-forward", "node": "fw1" }
    ]
  }
  ```

  sakutei-view's default rule for matching a Sakutei resource to a
  containerlab node is generic: it takes the first `/`-delimited segment
  of the resource's `local_id` (Sakutei's own per-resource identifier,
  distinct from its globally-unique `id`) and treats that as the node
  name — e.g. a `local_id` of `fw1/inet/filter/forward` matches node
  `fw1`. This works for resource kinds whose `local_id` happens to be
  scoped under the node it belongs to, but not every provider's
  `local_id` is shaped that way (and some resources, like a DNS zone or a
  cloud account, do not belong to any containerlab node at all — those
  are correctly left unmatched rather than guessed at).

  `sakutei_bindings` lets a plugin override or supplement that guess for
  any specific resource id, by its Sakutei `id` (not `local_id`). An
  entry here always wins over the generic rule for that resource id. The
  field is entirely optional; a plugin that doesn't need it can omit it,
  and sakutei-view then falls back to the generic rule for every
  resource, exactly as a v0.1/v0.2 plugin's responses are already
  understood.

## Changes in v0.2

Both changes are about the two ways `POST .../actions/...` and
`GET /plugin/runs/{run_id}` can report something other than a run in
progress — generalizing beyond "another run is already in progress",
since a plugin's exclusivity is not always held by one of its own runs.

- **`POST .../actions/...`'s 409 response**: `run_id` is now optional, and
  a new `busy_by` field says what kind of thing is holding exclusivity
  (`"run"` or `"external"`). A plugin's own run-tracking is not always
  what makes it unable to start a new action — the deployment's
  environment can be mid-rebuild, or held by some other exclusive
  operation the plugin does not itself track as a run. In that case there
  is no run id to report, so `run_id` may be omitted. See `busy_by` in the
  endpoint section below for the exact rule. A v0.1 plugin's 409 response
  (which always sets `run_id`, and never sets `busy_by`) still decodes
  correctly; a caller should treat a missing `busy_by` as `"run"`.
- **`GET /plugin/runs/{run_id}`'s `status`**: two values were added —
  `rejected` (the run could not be started at all) and `cancelled` (the
  run was stopped before finishing), alongside v0.1's `running`,
  `succeeded`, `failed`. `status` was always a free-form string for this
  reason; a caller encountering a status it does not recognize (from this
  or some future contract version) should treat it as an unknown,
  terminal state rather than failing or polling forever.

## `GET /plugin`

Capability/version discovery. Always the first call sakutei-view makes to
a configured plugin.

```json
{
  "version": "0.3",
  "name": "example-plugin",
  "capabilities": ["topology", "status", "scripts", "events"]
}
```

- `version`: the contract version this plugin implements (e.g. `"0.1"`,
  `"0.2"` or `"0.3"`).
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

**Secrecy of `inspect`/`clab_yaml`.** A `.clab.yml`'s node definitions can
carry secrets in their `env` blocks (API tokens, credential keys, SNMP
community strings, database passwords, ...), since that file is also how
the deployment configures its own nodes. A plugin SHOULD redact such
secrets (and any similarly secret-shaped fields) out of `clab_yaml` and
`inspect` before returning them — sakutei-view has no way to tell a
legitimate configuration value from a secret on its own. Regardless of
whether a plugin does this, sakutei-view MUST NOT expose `inspect` or
`clab_yaml` content (including `env` values) via its own API (`GET
/api/snapshot`, `GET /api/events`, or anywhere else a browser can reach) —
it only ever derives link and node-attribute data from them and discards
everything else, by construction, independent of what a plugin sends.

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
  ],
  "sakutei_bindings": []
}
```

- `resources[].id`: stable identifier, unique within this plugin.
- `resources[].state`: one of `ok`, `drifted`, `unknown`, `error`.
- `resources[].node`: the *topology* node name this resource belongs to
  (a `.clab.yml` node key — never containerlab's `clab-<lab-name>-<node>`
  container name), so sakutei-view can draw its state on the matching
  topology node. Empty string if there is no corresponding containerlab
  node.
- `resources[].fields`: free-form key/value pairs, shown as-is in the
  resource's detail panel.
- `resources[].drifted_fields`: subset of the keys in `fields` that are
  currently drifted (differ from what is desired), if `state` is
  `"drifted"`. Empty otherwise.
- `sakutei_bindings` (optional, added in v0.3): explicit Sakutei
  resource-id-to-node overrides — see "Changes in v0.3" above. Omit
  entirely (or send an empty array) if not needed.

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
{
  "accepted": false,
  "reason": "another run is already in progress",
  "run_id": "r-0001",
  "busy_by": "run"
}
```

- `busy_by` (added in v0.2) says what kind of thing is holding
  exclusivity:
  - `"run"`: one of this plugin's own runs (the usual case above).
    `run_id` is set, and can be polled via `GET /plugin/runs/{run_id}`.
  - `"external"`: something outside this plugin's own run-tracking holds
    exclusivity — for example, the deployment's environment as a whole is
    being rebuilt or re-validated by a process the plugin does not treat
    as one of its own runs. There is no run id to report in this case, so
    `run_id` is omitted:

    ```json
    { "accepted": false, "reason": "environment rebuild in progress", "busy_by": "external" }
    ```

  A v0.1 plugin never sets `busy_by` and always sets `run_id` on a 409; a
  caller should treat a missing `busy_by` as `"run"`.
- `reason` is always set when `accepted` is `false`, and is free-form —
  shown to a human as-is, not parsed.

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

- `status`: `running`, `succeeded`, `failed` (v0.1), plus, as of v0.2,
  `rejected` (the run could not be started at all, so it never actually
  ran) and `cancelled` (the run was stopped before finishing). This field
  is intentionally a free-form string rather than a closed enum: a caller
  should treat any value other than `running` as finished, and any value
  it does not otherwise recognize as an unknown-but-terminal state, so a
  future contract version can add further statuses without breaking
  existing callers.
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
