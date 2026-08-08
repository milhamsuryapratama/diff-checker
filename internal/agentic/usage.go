package agentic

import (
	"reflect"
	"sort"
	"sync"
)

var anyType = reflect.TypeOf((*any)(nil)).Elem()

// NodeUsage is what one LLM node cost.
//
// CacheRead is tracked separately and deliberately surfaced in the UI: the plan
// budgets for prompt caching, and a cache that silently stops working looks
// exactly like a cache that works, except for the bill. If this stays zero on
// the second run of a job, something in the prompt prefix is varying between
// requests and the caching assumption behind the cost model is void.
type NodeUsage struct {
	Node   string `json:"node"`
	Model  string `json:"model"`
	Calls  int    `json:"calls"`
	Input  int    `json:"input_tokens"`
	Output int    `json:"output_tokens"`

	CacheRead   int `json:"cache_read_tokens"`
	CacheWrite  int `json:"cache_write_tokens"`
	InputUncach int `json:"input_uncached_tokens"`

	// USD is computed from the per-model rates in the registry.
	USD float64 `json:"usd"`
}

// Usage accumulates per-node cost across a run. Nodes may execute in parallel,
// so it carries its own lock rather than relying on the graph's merge order.
type Usage struct {
	mu     sync.Mutex
	byNode map[string]*NodeUsage
}

// Add records one model call against a node.
func (u *Usage) Add(node string, n NodeUsage) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byNode == nil {
		u.byNode = map[string]*NodeUsage{}
	}
	cur, ok := u.byNode[node]
	if !ok {
		cur = &NodeUsage{Node: node, Model: n.Model}
		u.byNode[node] = cur
	}
	cur.Calls++
	cur.Input += n.Input
	cur.Output += n.Output
	cur.CacheRead += n.CacheRead
	cur.CacheWrite += n.CacheWrite
	cur.InputUncach += n.InputUncach
	cur.USD += n.USD
}

// Nodes returns per-node usage, ordered by node name for stable rendering.
func (u *Usage) Nodes() []NodeUsage {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]NodeUsage, 0, len(u.byNode))
	for _, v := range u.byNode {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// Totals sums every node. TotalUSD is the number the cost cap is checked
// against and the number shown to the user.
func (u *Usage) Totals() NodeUsage {
	var t NodeUsage
	t.Node = "total"
	for _, n := range u.Nodes() {
		t.Calls += n.Calls
		t.Input += n.Input
		t.Output += n.Output
		t.CacheRead += n.CacheRead
		t.CacheWrite += n.CacheWrite
		t.InputUncach += n.InputUncach
		t.USD += n.USD
	}
	return t
}

// mergeUsage is the state reducer for the usage field.
//
// Usage is a pointer to a locked accumulator shared by every node, so merging
// means "keep the one accumulator", not "overwrite with the newest value" —
// the default reducer would drop whichever parallel branch finished first.
func mergeUsage(existing, update any) any {
	if e, ok := existing.(*Usage); ok && e != nil {
		return e
	}
	return update
}
