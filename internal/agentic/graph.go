package agentic

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

// Pipeline builds and runs the comparison graph.
type Pipeline struct {
	registry *models.Registry
	compiled *graph.Graph
}

// New assembles the pipeline. The graph is compiled once and reused: it is
// stateless, with all per-run data carried in graph.State.
func New(registry *models.Registry) (*Pipeline, error) {
	p := &Pipeline{registry: registry}
	g, err := p.build()
	if err != nil {
		return nil, err
	}
	p.compiled = g
	return p, nil
}

// build wires the graph.
//
// Shape:
//
//	start ──┬── ingest_prev ──┐
//	        └── ingest_curr ──┴── deterministic ── triage ──┬── analyze ── recommend ──┐
//	                                                       └──────────────────────────┴── assemble ── end
//
// The two ingest branches are independent I/O and run concurrently, joining
// before the deterministic tier. The conditional edge after triage is the cost
// control that matters most: a revision that turns out to be pure reformatting
// skips both expensive models and lands straight on assemble.
func (p *Pipeline) build() (*graph.Graph, error) {
	sg := graph.NewStateGraph(NewSchema())

	sg.AddNode(NodeStart, startNode)
	sg.AddNode(NodeIngestPrev, ingestNode(docmodel.SidePrev))
	sg.AddNode(NodeIngestCurr, ingestNode(docmodel.SideCurr))
	sg.AddNode(NodeDeterm, deterministicNode)
	sg.AddNode(NodeTriage, p.triageNode)
	sg.AddNode(NodeAnalyze, p.analyzeNode)
	sg.AddNode(NodeRecommend, p.recommendNode)
	sg.AddNode(NodeAssemble, p.assembleNode)

	// Fan out to both ingests, then join.
	//
	// A multi-conditional edge is what actually produces parallel branches;
	// two plain edges out of Start only ever activate the declared entry point.
	// The entry node itself does no work — it exists to be the thing the
	// fan-out edge leaves from.
	sg.SetEntryPoint(NodeStart)
	sg.AddMultiConditionalEdges(NodeStart, fanOutIngest, map[string]string{
		NodeIngestPrev: NodeIngestPrev,
		NodeIngestCurr: NodeIngestCurr,
	})
	sg.AddJoinEdge([]string{NodeIngestPrev, NodeIngestCurr}, NodeDeterm)

	sg.AddEdge(NodeDeterm, NodeTriage)
	sg.AddConditionalEdges(NodeTriage, routeAfterTriage, map[string]string{
		routeAnalyze:  NodeAnalyze,
		routeAssemble: NodeAssemble,
	})
	sg.AddEdge(NodeAnalyze, NodeRecommend)
	sg.AddEdge(NodeRecommend, NodeAssemble)
	sg.SetFinishPoint(NodeAssemble)

	return sg.Compile()
}

const (
	routeAnalyze  = "analyze"
	routeAssemble = "assemble"
)

// fanOutIngest activates both ingest branches. It is unconditional: the two
// documents are always both needed, and the conditional-edge form is simply how
// this library expresses parallelism.
func fanOutIngest(ctx context.Context, s graph.State) ([]string, error) {
	return []string{NodeIngestPrev, NodeIngestCurr}, nil
}

// routeAfterTriage implements the shortcut. It is a pure function of state so
// it can be unit-tested without a model.
func routeAfterTriage(ctx context.Context, s graph.State) (string, error) {
	st := Wrap(s)
	if st.Options().NoLLM {
		return routeAssemble, nil
	}
	t := st.Triage()
	if t == nil || !t.Substantive || len(t.ChangeIDs) == 0 {
		return routeAssemble, nil
	}
	return routeAnalyze, nil
}

// caller resolves a tier's model and binds it to a node for usage accounting.
func (p *Pipeline) caller(tier models.Tier, node string, usage *Usage) (caller, error) {
	if p.registry == nil {
		return caller{}, fmt.Errorf("registry model belum dikonfigurasi")
	}
	entry, err := p.registry.Get(tier)
	if err != nil {
		return caller{}, err
	}
	return caller{entry: entry, node: node, usage: usage}, nil
}

// Agent wraps the compiled graph as an agent, which is what gives the run an
// event stream the SSE hub can forward to a browser without the job layer
// having to invent its own progress protocol.
func (p *Pipeline) Agent(name string) (*graphagent.GraphAgent, error) {
	return graphagent.New(name, p.compiled)
}

// Result is what a completed run produced.
type Result struct {
	Report *docmodel.Report
	Usage  *Usage
}

// Progress is one step update, forwarded to the UI as it happens.
type Progress struct {
	Node  string `json:"node"`
	Label string `json:"label"`
	// Status is "start", "done", or "error".
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Run executes the pipeline and reports progress through onProgress, which may
// be nil.
//
// The graph emits an event per node transition; the job layer turns those into
// server-sent events so the browser shows a checklist filling in rather than a
// spinner. Both the CLI and the web worker come through here, so there is no
// second execution path that could drift from the streamed one.
func (p *Pipeline) Run(
	ctx context.Context,
	prevPath, currPath string,
	opts Options,
	onProgress func(Progress),
) (*Result, error) {
	exec, err := graph.NewExecutor(p.compiled)
	if err != nil {
		return nil, fmt.Errorf("gagal menyiapkan eksekutor: %w", err)
	}

	initial, sink := Initial(prevPath, currPath, opts)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "run"}),
	)
	inv.AgentName = "diff-checker"

	events, err := exec.Execute(ctx, initial, inv)
	if err != nil {
		return nil, err
	}
	if err := drain(ctx, events, onProgress); err != nil {
		return nil, err
	}

	rep := sink.Report()
	if rep == nil {
		return nil, fmt.Errorf("pipeline selesai tanpa menghasilkan laporan")
	}
	return &Result{Report: rep, Usage: sink.Usage()}, nil
}

// drain consumes the executor's event stream, translating node lifecycle
// events into progress callbacks and surfacing the first node error.
//
// A node error must abort the run: the graph's later nodes assume their inputs
// exist, and reporting a half-built comparison as if it were complete is worse
// than reporting the failure.
func drain(ctx context.Context, events <-chan *event.Event, onProgress func(Progress)) error {
	emit := func(p Progress) {
		if onProgress != nil {
			onProgress(p)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-events:
			if !ok {
				return nil
			}
			if e == nil {
				continue
			}
			if e.Error != nil {
				return fmt.Errorf("%s", e.Error.Message)
			}
			node, status := nodeStatus(e)
			if node == "" {
				continue
			}
			emit(Progress{Node: node, Label: NodeLabels[node], Status: status})
		}
	}
}

// nodeStatus extracts the node ID and lifecycle phase from a graph event.
func nodeStatus(e *event.Event) (string, string) {
	if e.Response == nil {
		return "", ""
	}
	var status string
	switch e.Response.Object {
	case graph.ObjectTypeGraphNodeStart:
		status = "start"
	case graph.ObjectTypeGraphNodeComplete:
		status = "done"
	case graph.ObjectTypeGraphNodeError:
		status = "error"
	default:
		return "", ""
	}
	raw, ok := e.StateDelta[graph.MetadataKeyNode]
	if !ok {
		return "", ""
	}
	var meta graph.NodeExecutionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", ""
	}
	return meta.NodeID, status
}
