// Package trace records what each pipeline step was doing and why.
//
// A comparison that takes half a minute and then prints a verdict asks the
// reader to trust it. Showing the work as it happens — which paragraphs the
// parser found, which sequences the planner rewrote, what the model looked up
// before deciding — turns that into something a reviewer can audit, and makes a
// wrong answer diagnosable instead of merely wrong.
//
// Deterministic steps get traced too, not only the model. They are the majority
// of the pipeline and the part whose reasoning is actually reproducible, so
// their notes are the more useful half of the record.
package trace

import (
	"context"
	"sync"
	"time"
)

// Kind separates the sorts of trace line, because they mean different things
// and the UI renders them differently.
type Kind string

const (
	// KindNote is the deterministic engine explaining a step it just took.
	// Reproducible: the same documents always produce the same notes.
	KindNote Kind = "note"
	// KindThought is model reasoning, streamed as it arrives.
	KindThought Kind = "thought"
	// KindToolCall records the model asking for context.
	KindToolCall Kind = "tool_call"
	// KindToolResult records what it got back.
	KindToolResult Kind = "tool_result"
)

// Entry is one line of the record.
//
// Seq is assigned by the recorder and is stable: the browser uses it to decide
// whether an event appends a new line or extends one it already shows, which is
// what makes token-by-token streaming work without duplicating text.
type Entry struct {
	Seq  int       `json:"seq"`
	Node string    `json:"node"`
	Kind Kind      `json:"kind"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Recorder is what pipeline nodes write to.
//
// The pipeline holds this interface rather than a concrete type so that a run
// with nowhere to publish — the CLI, a unit test — costs nothing and needs no
// nil checks at every call site.
type Recorder interface {
	// Note records a complete line.
	Note(node string, kind Kind, text string)
	// Stream appends to the node's current entry of this kind, creating it on
	// first call. Used for model output that arrives a token at a time.
	Stream(node string, kind Kind, chunk string)
	// EndStream closes the current streamed entry so the next Stream call
	// starts a new line.
	EndStream(node string)
}

// Nop discards everything. Used by callers that do not display a trace.
type Nop struct{}

func (Nop) Note(string, Kind, string)   {}
func (Nop) Stream(string, Kind, string) {}
func (Nop) EndStream(string)            {}

// Buffer is the concrete recorder: it keeps every entry and calls a listener
// whenever one is added or extended.
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	// open tracks the in-progress streamed entry per node, by index.
	open map[string]int
	next int

	// onChange is called with the entry that changed, outside the lock.
	onChange func(Entry)
}

// NewBuffer creates a recorder. onChange may be nil.
func NewBuffer(onChange func(Entry)) *Buffer {
	return &Buffer{open: map[string]int{}, onChange: onChange}
}

// Note records a complete line and closes any open stream for the node, so a
// note never lands in the middle of streamed reasoning.
func (b *Buffer) Note(node string, kind Kind, text string) {
	if text == "" {
		return
	}
	b.EndStream(node)

	b.mu.Lock()
	e := Entry{Seq: b.next, Node: node, Kind: kind, Text: text, At: time.Now()}
	b.next++
	b.entries = append(b.entries, e)
	b.mu.Unlock()

	b.emit(e)
}

// Stream extends the node's open entry, or opens one.
func (b *Buffer) Stream(node string, kind Kind, chunk string) {
	if chunk == "" {
		return
	}
	b.mu.Lock()
	idx, ok := b.open[node]
	if !ok || b.entries[idx].Kind != kind {
		e := Entry{Seq: b.next, Node: node, Kind: kind, At: time.Now()}
		b.next++
		b.entries = append(b.entries, e)
		idx = len(b.entries) - 1
		b.open[node] = idx
	}
	b.entries[idx].Text += chunk
	e := b.entries[idx]
	b.mu.Unlock()

	// The listener receives the whole entry, not the chunk. Sending cumulative
	// text costs a little bandwidth and removes an entire class of bug: a
	// browser that missed one frame still ends up with the correct line.
	b.emit(e)
}

// EndStream closes the node's open entry.
func (b *Buffer) EndStream(node string) {
	b.mu.Lock()
	delete(b.open, node)
	b.mu.Unlock()
}

func (b *Buffer) emit(e Entry) {
	if b.onChange != nil {
		b.onChange(e)
	}
}

// Entries returns a copy of the record so far.
func (b *Buffer) Entries() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Entry(nil), b.entries...)
}

// Load restores previously persisted entries, so a page opened after a restart
// shows the same record it did while the job was running.
func (b *Buffer) Load(entries []Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append([]Entry(nil), entries...)
	b.open = map[string]int{}
	b.next = 0
	for _, e := range b.entries {
		if e.Seq >= b.next {
			b.next = e.Seq + 1
		}
	}
}

// Recorders travel through context rather than through the pipeline's state
// map.
//
// The graph executor reflects over every state value to serialise it into
// progress events, and that reflection races with a node concurrently writing
// reasoning: the copier reads the buffer's internals while Stream holds its
// lock. There is no way to opt a key out — the executor's list of
// non-serialisable keys is its own. Context is the correct home in any case: a
// recorder is ambient run-scoped infrastructure, not data flowing from one node
// to the next.
type ctxKey struct{}

// NewContext returns ctx carrying rec.
func NewContext(ctx context.Context, rec Recorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, rec)
}

// FromContext returns the recorder in ctx, or a no-op one.
func FromContext(ctx context.Context) Recorder {
	if rec, ok := ctx.Value(ctxKey{}).(Recorder); ok && rec != nil {
		return rec
	}
	return Nop{}
}
