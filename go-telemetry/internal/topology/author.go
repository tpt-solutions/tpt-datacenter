// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/questdb"
)

// LoadSpec reads a topology Spec from a JSON file and builds a Graph.
func LoadSpec(path string) (*Graph, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("topology: read %s: %w", path, err)
	}
	var spec Spec
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("topology: parse %s: %w", path, err)
	}
	g := NewGraph()
	if err := g.Load(spec); err != nil {
		return nil, err
	}
	return g, nil
}

// SaveSpec writes the graph as a JSON Spec (sorted, for stable diffs).
func SaveSpec(path string, g *Graph) error {
	b, err := json.MarshalIndent(g.Spec(), "", "  ")
	if err != nil {
		return fmt.Errorf("topology: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("topology: write %s: %w", path, err)
	}
	return nil
}

// DiscoverFromTelemetry enriches the graph with nodes discovered from the
// telemetry device registry (QuestDB `devices` table). Existing nodes are
// left untouched; only new device ids become nodes. This is the "auto-discovery
// from telemetry" authoring path — the manual Spec remains the source of truth
// for edges (power/cooling cabling) which telemetry cannot infer.
func (g *Graph) DiscoverFromTelemetry(ctx context.Context, client *questdb.Client) error {
	res, err := client.Exec(ctx, `SELECT device, kind FROM devices LATEST ON last_seen PARTITION BY device`)
	if err != nil {
		return fmt.Errorf("topology: discover: %w", err)
	}
	if len(res.Columns) < 2 {
		return fmt.Errorf("topology: unexpected devices columns: %v", res.Columns)
	}
	for _, row := range res.Dataset {
		if len(row) < 2 {
			continue
		}
		device, _ := row[0].(string)
		kind, _ := row[1].(string)
		if device == "" {
			continue
		}
		if _, ok := g.Node(device); ok {
			continue
		}
		node := Node{ID: device, Kind: mapKind(kind)}
		if err := g.UpsertNode(node); err != nil {
			return err
		}
	}
	return nil
}

// mapKind converts a discovered device kind string to a NodeKind, defaulting
// to sensor for unknown kinds.
func mapKind(k string) NodeKind {
	switch NodeKind(k) {
	case KindRack, KindPDU, KindUPS, KindCoolingLoop, KindServer, KindRoom, KindCable:
		return NodeKind(k)
	default:
		return KindSensor
	}
}
