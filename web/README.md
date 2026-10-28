# sakutei-view's web UI

React + TypeScript + Vite + [Cytoscape.js](https://js.cytoscape.org/), built
against sakutei-view's own `GET /api/snapshot` and `GET /api/events` (see
[../README.md](../README.md)). It shows the containerlab topology (as a
Cytoscape.js diagram, color-coded by each node's Sakutei/plugin-derived
state), Sakutei's resource summary and halted-reason list, its dependency
graph grouped by wave, a table of nodes, and a live merged event timeline.

## Developing

Run the real backend first (see the top-level README for its environment
variables), then:

```sh
npm install
npm run dev
```

`npm run dev` proxies `/api/*` to `http://127.0.0.1:8080` (the backend's
default `LISTEN_ADDR`) so the browser only ever talks to Vite's dev server —
see `vite.config.ts`. Point it elsewhere with:

```sh
VITE_API_PROXY_TARGET=http://127.0.0.1:9000 npm run dev
```

`npm run build` type-checks (`tsc -b`) and produces a static `dist/`;
`npm run lint` runs [oxlint](https://oxc.rs/).

## Production build / embedding into the Go binary

The production binary embeds this frontend's build output directly (see
[../internal/webui](../internal/webui)), so a deployment is a single
executable with no Node.js involved. That embedded copy is **not** rebuilt
automatically — it lives, committed, under `../internal/webui/dist`. After
changing anything here, refresh it and commit the result:

```sh
../scripts/build-frontend.sh
```

This runs `npm ci && npm run build` in this directory and copies `dist/`
into `../internal/webui/dist`. Without this step, `go build
./cmd/sakutei-view` will keep serving whatever frontend build was last
committed, not your local changes.
