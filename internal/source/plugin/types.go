// Package plugin is an HTTP client for the device plugin contract
// (docs/plugin-contract.md, v0.1). It only knows how to call the contract's
// endpoints and decode their responses; it has no opinion about how a
// particular plugin is deployed.
package plugin

import (
	"encoding/json"
	"time"
)

// Info is the response of GET /plugin.
type Info struct {
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

// HasCapability reports whether cap is listed in Capabilities.
func (i Info) HasCapability(cap string) bool {
	for _, c := range i.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// Topology is the response of GET /plugin/topology.
type Topology struct {
	Inspect  json.RawMessage `json:"inspect"`
	ClabYAML string          `json:"clab_yaml"`
}

// Resource is one element of StatusResponse.Resources.
type Resource struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Label         string         `json:"label"`
	State         string         `json:"state"` // ok | drifted | unknown | error
	Node          string         `json:"node"`
	Fields        map[string]any `json:"fields"`
	DriftedFields []string       `json:"drifted_fields"`
}

// StatusResponse is the response of GET /plugin/status.
type StatusResponse struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Resources   []Resource `json:"resources"`
}

// Script is one element of ScriptsResponse.Scripts.
type Script struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
}

// ScriptsResponse is the response of GET /plugin/scripts.
type ScriptsResponse struct {
	Scripts []Script `json:"scripts"`
}

// ActionResult is the response of POST /plugin/scripts/{id}/actions/{action},
// for both the 202 Accepted and 409 Conflict cases.
type ActionResult struct {
	Accepted bool   `json:"accepted"`
	RunID    string `json:"run_id"`
	Reason   string `json:"reason,omitempty"`
}

// Run is the response of GET /plugin/runs/{run_id}.
type Run struct {
	RunID      string     `json:"run_id"`
	ScriptID   string     `json:"script_id"`
	Action     string     `json:"action"`
	Status     string     `json:"status"` // running | succeeded | failed
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Message    string     `json:"message"`
}

// Event is one event from GET /plugin/events.
type Event struct {
	Kind       string    `json:"kind"`
	Level      string    `json:"level"` // firing | resolved
	ResourceID string    `json:"resource_id,omitempty"`
	Message    string    `json:"message"`
	At         time.Time `json:"at"`
}
