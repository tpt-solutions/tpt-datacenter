// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package topology models the physical plant as a directed graph: racks, PDUs,
// UPS units, cooling loops, sensors and the cables/power/data relationships
// between them. It backs the "what cools this rack" / "what feeds this PDU"
// questions used by the dashboard and the Thermal AI Brain.
//
// Storage choice: an in-memory adjacency graph (no external graph DB). At
// facility scale the node count is bounded (thousands), well within memory,
// and the graph changes slowly — so a full graph database would be operational
// overhead without benefit. The graph is authored from a JSON spec and/or
// enriched by auto-discovery from the telemetry device registry, and can be
// snapshotted to JSON for persistence/audit.
package topology

// NodeKind classifies a physical element.

// NodeKind classifies a physical element.
type NodeKind string

const (
	KindRoom        NodeKind = "room"
	KindRack        NodeKind = "rack"
	KindPDU         NodeKind = "pdu"
	KindUPS         NodeKind = "ups"
	KindCoolingLoop NodeKind = "cooling_loop"
	KindSensor      NodeKind = "sensor"
	KindServer      NodeKind = "server"
	KindCable       NodeKind = "cable"
)

// EdgeKind classifies a relationship between two nodes.
type EdgeKind string

const (
	// EdgePowers: electrical power flows from->to (UPS -> PDU -> rack -> server).
	EdgePowers EdgeKind = "powers"
	// EdgeCools: thermal influence from->to (cooling loop -> rack).
	EdgeCools EdgeKind = "cools"
	// EdgeContains: physical containment (room -> rack, rack -> pdu).
	EdgeContains EdgeKind = "contains"
	// EdgeConnects: data/network link (server <-> server, sensor -> rack).
	EdgeConnects EdgeKind = "connects"
	// EdgeFeeds: cooling supply linkage (cooling loop -> rack via ductwork).
	EdgeFeeds EdgeKind = "feeds"
)

// Node is a vertex in the topology graph.
type Node struct {
	ID     string            `json:"id"`
	Kind   NodeKind          `json:"kind"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	From   string            `json:"from"`
	To     string            `json:"to"`
	Kind   EdgeKind          `json:"kind"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Spec is the on-disk / over-the-wire topology document.
type Spec struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}
