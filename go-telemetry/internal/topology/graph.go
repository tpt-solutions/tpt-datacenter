// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package topology

import (
	"fmt"
	"sort"
	"sync"
)

// Graph is the thread-safe physical topology graph.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	out   map[string][]Edge // outgoing edges keyed by From
	in    map[string][]Edge // incoming edges keyed by To
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes: map[string]*Node{},
		out:   map[string][]Edge{},
		in:    map[string][]Edge{},
	}
}

// UpsertNode adds or replaces a node.
func (g *Graph) UpsertNode(n Node) error {
	if n.ID == "" {
		return fmt.Errorf("topology: node id required")
	}
	if n.Kind == "" {
		return fmt.Errorf("topology: node %q kind required", n.ID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cp := n
	cp.Labels = cloneLabels(n.Labels)
	g.nodes[n.ID] = &cp
	return nil
}

// UpsertEdge adds a directed edge, creating neither endpoint.
func (g *Graph) UpsertEdge(e Edge) error {
	if e.From == "" || e.To == "" {
		return fmt.Errorf("topology: edge endpoints required")
	}
	if e.Kind == "" {
		return fmt.Errorf("topology: edge kind required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[e.From]; !ok {
		return fmt.Errorf("topology: unknown from node %q", e.From)
	}
	if _, ok := g.nodes[e.To]; !ok {
		return fmt.Errorf("topology: unknown to node %q", e.To)
	}
	cp := e
	cp.Labels = cloneLabels(e.Labels)
	g.out[e.From] = append(g.out[e.From], cp)
	g.in[e.To] = append(g.in[e.To], cp)
	return nil
}

// Load replaces the graph contents with the given spec.
func (g *Graph) Load(spec Spec) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes = map[string]*Node{}
	g.out = map[string][]Edge{}
	g.in = map[string][]Edge{}
	for _, n := range spec.Nodes {
		cp := n
		cp.Labels = cloneLabels(n.Labels)
		g.nodes[n.ID] = &cp
	}
	for _, e := range spec.Edges {
		if _, ok := g.nodes[e.From]; !ok {
			return fmt.Errorf("topology: edge references unknown from node %q", e.From)
		}
		if _, ok := g.nodes[e.To]; !ok {
			return fmt.Errorf("topology: edge references unknown to node %q", e.To)
		}
		cp := e
		cp.Labels = cloneLabels(e.Labels)
		g.out[e.From] = append(g.out[e.From], cp)
		g.in[e.To] = append(g.in[e.To], cp)
	}
	return nil
}

// Node returns a node by id.
func (g *Graph) Node(id string) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

// Nodes returns all nodes sorted by id.
func (g *Graph) Nodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Spec returns the full graph as a serializable spec.
func (g *Graph) Spec() Spec {
	return Spec{Nodes: g.Nodes(), Edges: g.edgesSorted()}
}

func (g *Graph) edgesSorted() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Edge
	for _, es := range g.out {
		out = append(out, es...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].To != out[j].To {
			return out[i].To < out[j].To
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// Successors returns outgoing edges from id.
func (g *Graph) Successors(id string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]Edge(nil), g.out[id]...)
}

// Predecessors returns incoming edges to id.
func (g *Graph) Predecessors(id string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]Edge(nil), g.in[id]...)
}

// Cools returns the cooling loops / cooling edges that influence the given
// node — the answer to "what cools this rack?".
func (g *Graph) Cools(id string) []Edge {
	return g.predByKind(id, EdgeCools)
}

// Feeds returns the supply relationships (cooling ductwork) into the node.
func (g *Graph) Feeds(id string) []Edge {
	return g.predByKind(id, EdgeFeeds)
}

// PowersChain returns the upstream power sources of a node — the answer to
// "what feeds this PDU?" (UPS -> PDU -> rack). It walks predecessors along
// powers edges, breadth-first, and returns them in topological order.
func (g *Graph) PowersChain(id string) []Node {
	return g.walkUp(id, EdgePowers)
}

// CoolingChain returns the downstream nodes a cooling loop influences.
func (g *Graph) CoolingChain(id string) []Node {
	return g.walkDown(id, EdgeCools)
}

func (g *Graph) predByKind(id string, k EdgeKind) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Edge
	for _, e := range g.in[id] {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func (g *Graph) walkUp(id string, k EdgeKind) []Node {
	return g.bfs(id, k, true)
}

func (g *Graph) walkDown(id string, k EdgeKind) []Node {
	return g.bfs(id, k, false)
}

// bfs walks the graph from id along edges of kind k. When up is true it
// follows predecessors (incoming), otherwise successors (outgoing).
func (g *Graph) bfs(id string, k EdgeKind, up bool) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	seen := map[string]bool{id: true}
	var order []Node
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		edges := g.out[cur]
		if up {
			edges = g.in[cur]
		}
		for _, e := range edges {
			if e.Kind != k {
				continue
			}
			next := e.To
			if up {
				next = e.From
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			if n, ok := g.nodes[next]; ok {
				order = append(order, *n)
				queue = append(queue, next)
			}
		}
	}
	return order
}

func cloneLabels(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
