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
   exposing `/status`, `/plan`, `/events` (SSE), `/graph` and `/metrics`.
   See the [Sakutei repository](https://github.com/tjst-t/sakutei) for the
   exact shapes.
3. **A device plugin** — a small HTTP service that a deployment implements
   itself to expose extra device state (for example, storage or other
   appliance state) and optional demo scripts ("disturb this", "let Sakutei
   fix it"). The contract is defined in
   [docs/plugin-contract.md](docs/plugin-contract.md); a minimal reference
   implementation lives under [examples/example-plugin](examples/example-plugin).
   A deployment's actual plugin implementation is expected to live in its
   own, separate repository — this repository only defines and consumes
   the contract.

## Building

```sh
go build ./cmd/sakutei-view
```

(The web UI build step will be documented once the frontend is in place.)

## License

Apache License 2.0, see [LICENSE](LICENSE).
