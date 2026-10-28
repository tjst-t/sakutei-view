# sakutei-view

A small, read-only web view for a [Containerlab](https://containerlab.dev/)-based
lab and the declarative infrastructure convergence engine
[Sakutei](https://github.com/tjst-t/sakutei) running on top of it.

It shows, on one page:

- the lab's topology (nodes, links, addresses) and each node's state,
- Sakutei's status: how many resources are in sync, which are drifted, and
  why any operations are currently blocked or halted,
- the dependency graph Sakutei builds resources up in, wave by wave,
- a live timeline of events, merging Sakutei's own events with events from
  any connected devices.

It is meant for two audiences: engineers who want a quick picture of what a
lab is doing, and demos that want to show convergence (drift happening,
Sakutei detecting and fixing it) without reading logs.

## Status

Early development. v1 is **read-only**: there are no write/approve actions
in the view itself. See [docs/plugin-contract.md](docs/plugin-contract.md)
for how device-specific data and demo scripts are supplied without this
repository knowing anything about any particular deployment.

## How it reads data

sakutei-view is a single Go binary (with the web UI embedded) that polls or
subscribes to three plain HTTP sources and merges them into one live view
over Server-Sent Events:

1. **Containerlab** — topology and node state, either from a
   [`clab-api-server`](https://containerlab.dev/manual/gui/) instance, or
   from a plugin's `GET /plugin/topology` (see below). The data source is
   pluggable.
2. **Sakutei's read-only API** — started with `sakutei --read-listen`,
   exposing `/status`, `/plan`, `/graph` and `/events` (SSE). See the
   [Sakutei repository](https://github.com/tjst-t/sakutei) for the exact
   shapes.
3. **A device plugin** — a small HTTP service that a deployment implements
   itself to expose extra device state (for example, storage or other
   appliance state) and optional demo scripts ("disturb this", "let Sakutei
   fix it"). The contract is defined in
   [docs/plugin-contract.md](docs/plugin-contract.md); a minimal reference
   implementation lives under [examples/example-plugin](examples/example-plugin).
   A deployment's actual plugin implementation is expected to live in its
   own, separate repository — this repository only defines and consumes
   the contract.

## Running

Configuration is entirely by environment variable (no config file, no flags
yet):

| Variable | Meaning |
| --- | --- |
| `LISTEN_ADDR` | Address to listen on. Default `127.0.0.1:8080` (localhost-only). |
| `CONTAINERLAB_SOURCE` | `clab-api` or `plugin`. Required. |
| `CONTAINERLAB_BASE_URL`, `CONTAINERLAB_LAB_NAME` | Required when `CONTAINERLAB_SOURCE=clab-api`. |
| `CONTAINERLAB_CA_FILE` | Optional PEM file of CA certificates to trust for `CONTAINERLAB_BASE_URL`. Omit to use the OS trust store. |
| `CONTAINERLAB_TOKEN_FILE` / `CONTAINERLAB_TOKEN_ENV` | Optional bearer token for `CONTAINERLAB_BASE_URL`, as a file path or the name of another environment variable. |
| `SAKUTEI_BASE_URL` | Sakutei's `--read-listen` address. Optional; the view works without it. |
| `SAKUTEI_CA_FILE`, `SAKUTEI_TOKEN_FILE` / `SAKUTEI_TOKEN_ENV` | Same convention as the `CONTAINERLAB_*` equivalents, for `SAKUTEI_BASE_URL`. |
| `PLUGIN_BASE_URL` | A device plugin's address (see [docs/plugin-contract.md](docs/plugin-contract.md)). Optional; also used as the containerlab source when `CONTAINERLAB_SOURCE=plugin`. |
| `PLUGIN_CA_FILE`, `PLUGIN_TOKEN_FILE` / `PLUGIN_TOKEN_ENV` | Same convention, for `PLUGIN_BASE_URL`. |
| `BASIC_AUTH_USER`, `BASIC_AUTH_PASSWORD` / `BASIC_AUTH_PASSWORD_FILE` | Optional HTTP Basic auth in front of `/api/snapshot` and `/api/events`. Both a user and a password must be set for auth to be required. |

No outbound connection (to clab-api-server, Sakutei, or a plugin) ever skips
TLS certificate verification; see [internal/httpclient](internal/httpclient).

```sh
go build ./cmd/sakutei-view
CONTAINERLAB_SOURCE=plugin PLUGIN_BASE_URL=http://127.0.0.1:8081 ./sakutei-view
```

(The web UI build step will be documented once the frontend is in place.)

## License

Apache License 2.0, see [LICENSE](LICENSE).
