# sakutei-view's web UI

React + TypeScript + Vite + [Cytoscape.js](https://js.cytoscape.org/), built
against sakutei-view's own `GET /api/snapshot` and `GET /api/events` (see
[../README.md](../README.md)). This is a scaffold: it fetches the current
snapshot, shows each source's health, lists nodes with their color-coded
state, and follows the live event stream. The topology diagram itself
(Cytoscape.js canvas, see Backlog `item-2`) is not built here yet.

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

The eventual production binary embeds this `dist/` into the Go binary (see
Backlog `embed`) so a deployment is a single executable with no Node.js
involved.
