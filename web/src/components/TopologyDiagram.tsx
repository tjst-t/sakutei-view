import cytoscape from 'cytoscape'
import type { Core, ElementDefinition, NodeSingular, StylesheetJsonBlock } from 'cytoscape'
import { useEffect, useRef, useState } from 'react'
import type { SnapshotLink, SnapshotNode } from '../api/types'
import { statusColorHex, statusLabel } from './colors'
import { StatusDot } from './StatusDot'

// Literal hex twins of this palette's dark-surface tokens (see
// statusColorHex's doc comment for why Cytoscape needs literals, not
// var(--x) references).
const SURFACE_RAISED = '#1b2227'
const BORDER = '#262f35'
const TEXT = '#e4e9ec'
const TEXT_FAINT = '#5a656b'

// A small chip/device glyph, shown as a corner badge on nodes that carry a
// device-plugin overlay (internal/aggregate's PluginOverlay) — i.e. a
// resource the plugin itself reports on, distinct from what containerlab or
// Sakutei see. Drawn as a tiny inline SVG so it tracks the dark palette
// exactly rather than pulling in an icon font/library for one glyph.
const PLUGIN_BADGE = `data:image/svg+xml,${encodeURIComponent(
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` +
    `<rect x="7" y="7" width="10" height="10" rx="1.5" fill="none" stroke="${TEXT}" stroke-width="2"/>` +
    `<path d="M9 7V3M15 7V3M9 21v-4M15 21v-4M7 9H3M7 15H3M21 9h-4M21 15h-4" stroke="${TEXT}" stroke-width="2" stroke-linecap="round"/>` +
    `</svg>`,
)}`

function nodeElement(n: SnapshotNode): ElementDefinition {
  return {
    group: 'nodes',
    data: {
      id: n.name,
      label: n.name,
      borderColor: statusColorHex(n.color),
      statusLabel: statusLabel(n.color),
      hasPlugin: Boolean(n.plugin),
      kind: n.kind ?? '',
      state: n.state ?? '',
    },
  }
}

function linkId(l: SnapshotLink, i: number): string {
  // containerlab links have no natural id of their own; the endpoint pair
  // is stable across polls as long as the topology itself doesn't change,
  // which is what the diff below relies on. Fall back to the index only if
  // an endpoint is missing a node name (shouldn't happen in practice).
  if (l.a.node && l.b.node) {
    return [l.a.node, l.a.interface ?? '', l.b.node, l.b.interface ?? ''].join('|')
  }
  return `link-${i}`
}

function linkElement(l: SnapshotLink, i: number): ElementDefinition | null {
  if (!l.a.node || !l.b.node) return null
  return {
    group: 'edges',
    data: {
      id: linkId(l, i),
      source: l.a.node,
      target: l.b.node,
      label: [l.a.interface, l.b.interface].filter(Boolean).join(' ↔ '),
    },
  }
}

const STYLE: StylesheetJsonBlock[] = [
  {
    selector: 'node',
    style: {
      shape: 'round-rectangle',
      width: 'label',
      height: 34,
      padding: '10px',
      'background-color': SURFACE_RAISED,
      'border-width': 3,
      'border-color': 'data(borderColor)',
      label: 'data(label)',
      color: TEXT,
      'font-family': 'ui-monospace, Consolas, monospace',
      'font-size': 12,
      'text-valign': 'center',
      'text-halign': 'center',
    },
  },
  {
    selector: 'node[?hasPlugin]',
    style: {
      'background-image': PLUGIN_BADGE,
      'background-width': 14,
      'background-height': 14,
      'background-position-x': '100%',
      'background-position-y': '100%',
      'background-offset-x': -2,
      'background-offset-y': -2,
      'background-fit': 'none',
      'background-clip': 'none',
      'background-repeat': 'no-repeat',
      'padding-right': '20px',
    },
  },
  {
    selector: 'node:selected',
    style: {
      'border-width': 4,
      'overlay-color': TEXT,
      'overlay-opacity': 0.12,
      'overlay-padding': 6,
    },
  },
  {
    selector: 'edge',
    style: {
      width: 1.5,
      'line-color': BORDER,
      'curve-style': 'bezier',
      'target-arrow-shape': 'none',
      label: 'data(label)',
      'font-size': 9,
      color: TEXT_FAINT,
      'font-family': 'ui-monospace, Consolas, monospace',
      'text-background-color': '#0d1013',
      'text-background-opacity': 0.85,
      'text-background-padding': '2px',
    },
  },
]

interface Props {
  nodes: SnapshotNode[]
  links: SnapshotLink[]
}

/**
 * The topology diagram: containerlab's nodes and links, rendered
 * with Cytoscape.js, node borders colored by the same ok/drift/down
 * vocabulary as the rest of the app (see colors.ts), and a small corner
 * badge on nodes the device plugin also reports on.
 *
 * Keeps one persistent cytoscape.Core across snapshot updates (does not
 * tear down and recreate it on every poll): node/edge data is patched
 * in-place, and layout only re-runs when the set of node/edge ids actually
 * changes, so a color flip (e.g. a disturb/fix demo action) never resets
 * the user's pan/zoom or reshuffles the layout.
 */
export function TopologyDiagram({ nodes, links }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const cyRef = useRef<Core | null>(null)
  const [selected, setSelected] = useState<SnapshotNode | null>(null)

  // Create the cytoscape instance once.
  useEffect(() => {
    if (!containerRef.current) return
    const cy = cytoscape({
      container: containerRef.current,
      style: STYLE,
      elements: [],
      wheelSensitivity: 0.25,
      minZoom: 0.3,
      maxZoom: 2.5,
    })
    cyRef.current = cy
    return () => {
      cy.destroy()
      cyRef.current = null
    }
  }, [])

  // Keep the "selected node" side panel in sync with clicks, independent of
  // the snapshot-diffing effect below.
  useEffect(() => {
    const cy = cyRef.current
    if (!cy) return
    const handler = (evt: cytoscape.EventObject) => {
      const n = evt.target as NodeSingular
      const found = (nodes ?? []).find((x) => x.name === n.id())
      setSelected(found ?? null)
    }
    const clearHandler = () => setSelected(null)
    cy.on('tap', 'node', handler)
    cy.on('tap', (evt) => {
      if (evt.target === cy) clearHandler()
    })
    return () => {
      cy.removeListener('tap')
    }
    // Re-bind whenever `nodes` changes so the closure's lookup stays fresh;
    // this is cheap (cytoscape's own element set is untouched here).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes])

  // Keep the detail panel current when the snapshot refreshes a node that's
  // already selected (e.g. a disturb/fix action changing its state) —
  // without this, clicking a node once would freeze its panel at that
  // moment's data.
  useEffect(() => {
    setSelected((prev) => {
      if (!prev) return prev
      return (nodes ?? []).find((n) => n.name === prev.name) ?? null
    })
  }, [nodes])

  // Diff the new snapshot into the existing cytoscape graph.
  useEffect(() => {
    const cy = cyRef.current
    if (!cy) return

    const wantNodes = new Map((nodes ?? []).map((n) => [n.name, n]))
    const wantEdges = new Map(
      (links ?? [])
        .map((l, i) => [linkId(l, i), l] as const)
        .filter(([, l]) => l.a.node && l.b.node),
    )

    let structureChanged = false

    cy.batch(() => {
      // Remove stale elements.
      cy.nodes().forEach((el) => {
        if (!wantNodes.has(el.id())) {
          el.remove()
          structureChanged = true
        }
      })
      cy.edges().forEach((el) => {
        if (!wantEdges.has(el.id())) {
          el.remove()
          structureChanged = true
        }
      })

      // Add/update nodes.
      for (const [id, n] of wantNodes) {
        const existing = cy.getElementById(id)
        if (existing.nonempty()) {
          existing.data({
            borderColor: statusColorHex(n.color),
            statusLabel: statusLabel(n.color),
            hasPlugin: Boolean(n.plugin),
            kind: n.kind ?? '',
            state: n.state ?? '',
          })
        } else {
          cy.add(nodeElement(n))
          structureChanged = true
        }
      }

      // Add/update edges (links carry no mutable state today, but keep the
      // same add-if-missing shape as nodes for consistency).
      for (const [id, l] of wantEdges) {
        if (cy.getElementById(id).empty()) {
          const el = linkElement(l, 0)
          if (el) {
            el.data.id = id
            cy.add(el)
            structureChanged = true
          }
        }
      }
    })

    if (structureChanged) {
      cy.layout({
        name: 'cose',
        animate: false,
        fit: true,
        padding: 40,
        nodeRepulsion: () => 12000,
        idealEdgeLength: () => 120,
      }).run()
    }
  }, [nodes, links])

  return (
    <div style={{ display: 'flex', gap: 16 }}>
      <div
        ref={containerRef}
        style={{
          flex: 1,
          height: 420,
          background: 'var(--bg)',
          border: '1px solid var(--border)',
          borderRadius: 6,
        }}
      />
      <div
        style={{
          width: 220,
          flexShrink: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
        }}
      >
        <Legend />
        {selected ? <NodeDetail node={selected} /> : null}
      </div>
    </div>
  )
}

const LEGEND_COLORS = ['ok', 'drift', 'down', 'workflow', 'unknown'] as const

function Legend() {
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 12,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        fontSize: 12,
      }}
    >
      {LEGEND_COLORS.map((c) => (
        <div key={c} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <StatusDot color={statusColorHex(c)} />
          <span style={{ color: 'var(--text-muted)' }}>{statusLabel(c)}</span>
        </div>
      ))}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
        <span
          className="mono"
          style={{
            display: 'inline-block',
            width: 9,
            height: 9,
            border: '1.5px solid var(--text)',
            borderRadius: 2,
          }}
        />
        <span style={{ color: 'var(--text-muted)' }}>has a device-plugin overlay</span>
      </div>
    </div>
  )
}

function NodeDetail({ node }: { node: SnapshotNode }) {
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 6,
        padding: 12,
        fontSize: 12,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
      }}
    >
      <div className="mono" style={{ fontWeight: 600 }}>
        {node.name}
      </div>
      <Row label="kind" value={node.kind} />
      <Row label="containerlab" value={[node.state, node.status].filter(Boolean).join(' / ')} />
      <Row label="status" value={statusLabel(node.color)} />
      {node.sakutei && (
        <Row
          label="sakutei"
          value={[node.sakutei.state, node.sakutei.drifted_fields?.join(', ')]
            .filter(Boolean)
            .join(': ')}
        />
      )}
      {node.plugin && (
        <Row
          label="plugin"
          value={[node.plugin.state, node.plugin.drifted_fields?.join(', ')]
            .filter(Boolean)
            .join(': ')}
        />
      )}
    </div>
  )
}

function Row({ label, value }: { label: string; value?: string }) {
  if (!value) return null
  return (
    <div>
      <span style={{ color: 'var(--text-faint)' }}>{label}: </span>
      <span>{value}</span>
    </div>
  )
}
