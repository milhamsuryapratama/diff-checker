package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/trace"
)

// callTimeout bounds a single model call.
//
// mining-legal-backend has no per-call timeout at all — only one 600s deadline
// around the whole pipeline — which is why a single wedged request there takes
// the entire job down with it and needs a janitor to clean up afterwards. A
// per-call budget lets one slow request fail and be retried instead.
const callTimeout = 120 * time.Second

// maxAttempts is the retry budget per call. Transient 429/5xx responses are the
// common case, so a small budget with backoff recovers most of them.
const maxAttempts = 3

// caller wraps a tier's model with retry, usage accounting, JSON decoding and
// reasoning capture.
type caller struct {
	entry models.Entry
	node  string
	usage *Usage

	// rec receives the model's reasoning as it streams, so the UI can show the
	// same token-by-token thinking a chat client does. traceNode names the
	// pipeline step the lines belong to. Both may be zero: the CLI has nowhere
	// to display them.
	rec       trace.Recorder
	traceNode string
}

// recorder returns the trace sink, never nil.
func (c caller) recorder() trace.Recorder {
	if c.rec != nil {
		return c.rec
	}
	return trace.Nop{}
}

// completion is one model reply plus what it cost.
type completion struct {
	Text  string
	Usage NodeUsage
}

// complete sends one request and collects the streamed reply into a single
// string, retrying transient failures with exponential backoff.
func (c caller) complete(ctx context.Context, req *model.Request) (completion, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := sleepBackoff(ctx, attempt); err != nil {
				return completion{}, err
			}
		}

		out, err := c.attempt(ctx, req)
		if err == nil {
			c.usage.Add(c.node, out.Usage)
			return out, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// The caller gave up, or the job was cancelled. Retrying would only
			// burn the remaining budget against a dead context.
			if ctx.Err() != nil {
				return completion{}, err
			}
		}
	}
	return completion{}, fmt.Errorf("node %s: %d percobaan gagal: %w", c.node, maxAttempts, lastErr)
}

// sleepBackoff waits before retry attempt n (n >= 2): 500ms, 1s, 2s.
// Short enough that a user watching the progress page does not conclude the
// job has died, long enough to clear a provider rate-limit window.
func sleepBackoff(ctx context.Context, attempt int) error {
	d := time.Duration(1<<(attempt-2)) * 500 * time.Millisecond
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (c caller) attempt(ctx context.Context, req *model.Request) (completion, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	ch, err := c.entry.Model.GenerateContent(ctx, req)
	if err != nil {
		return completion{}, err
	}

	var text strings.Builder
	var usage NodeUsage
	usage.Model = c.entry.Name

	for {
		select {
		case <-ctx.Done():
			return completion{}, ctx.Err()
		case rsp, ok := <-ch:
			if !ok {
				c.recorder().EndStream(c.traceNode)
				if text.Len() == 0 {
					return completion{}, fmt.Errorf("model tidak mengembalikan konten")
				}
				return completion{Text: text.String(), Usage: usage}, nil
			}
			if rsp == nil {
				continue
			}
			if rsp.Error != nil {
				return completion{}, fmt.Errorf("model error: %s", rsp.Error.Message)
			}
			for _, ch := range rsp.Choices {
				// Extended thinking arrives on its own field, separate from the
				// answer. Forwarding it as it streams is what makes the UI show
				// reasoning live rather than a spinner; it is deliberately not
				// mixed into text, which must stay parseable as JSON.
				if ch.Delta.ReasoningContent != "" {
					c.recorder().Stream(c.traceNode, trace.KindThought, ch.Delta.ReasoningContent)
				}
				// Streaming deltas and the final non-streamed message arrive on
				// the same channel; taking both would duplicate the text.
				if ch.Delta.Content != "" {
					text.WriteString(ch.Delta.Content)
				} else if ch.Message.Content != "" && rsp.Done {
					text.WriteString(ch.Message.Content)
				}
			}
			if rsp.Usage != nil {
				usage.Input = rsp.Usage.PromptTokens
				usage.Output = rsp.Usage.CompletionTokens
				usage.CacheRead = rsp.Usage.PromptTokensDetails.CacheReadTokens
				usage.CacheWrite = rsp.Usage.PromptTokensDetails.CacheCreationTokens
				usage.InputUncach = usage.Input - usage.CacheRead - usage.CacheWrite
				if usage.InputUncach < 0 {
					usage.InputUncach = usage.Input
				}
				usage.USD = c.entry.Rates.Cost(
					usage.InputUncach, usage.CacheRead, usage.CacheWrite, usage.Output)
			}
		}
	}
}

// completeJSON asks for a structured reply and decodes it into out.
//
// out must be a pointer. The contract lives in the Go struct: the request-level
// schema and the prompt-level shape are both derived from it by reflection, so
// there is no hand-maintained schema to drift out of step. The shape is
// appended to the user message because not every provider adapter forwards the
// request-level schema — see jsonshape.go for what that silently cost.
func (c caller) completeJSON(ctx context.Context, system, user string, out any, desc string) error {
	req := model.NewRequest(
		[]model.Message{
			model.NewSystemMessage(system),
			model.NewUserMessage(user + contractFor(out)),
		},
		model.WithStructuredOutputJSON(out, true, desc),
	)
	req.Stream = true

	rsp, err := c.complete(ctx, req)
	if err != nil {
		return err
	}
	return decodeJSON(rsp.Text, out)
}

// decodeJSON parses a model reply into out.
//
// Structured output makes a bare JSON body the normal case, but not every
// provider honours it strictly, so a fenced block or a JSON object embedded in
// prose is unwrapped rather than rejected. A reply that still will not parse is
// a hard error: guessing at malformed output is how unreviewable findings get
// into a report.
func decodeJSON(text string, out any) error {
	s := strings.TrimSpace(text)
	if s == "" {
		return fmt.Errorf("balasan model kosong")
	}
	if err := json.Unmarshal([]byte(s), out); err == nil {
		return nil
	}
	if inner, ok := extractJSON(s); ok {
		if err := json.Unmarshal([]byte(inner), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("balasan model bukan JSON yang valid: %s", truncate(s, 200))
}

// extractJSON pulls the outermost JSON object or array out of a reply,
// tolerating ```json fences and leading prose.
func extractJSON(s string) (string, bool) {
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			candidate := strings.TrimSpace(rest[:j])
			if candidate != "" {
				return candidate, true
			}
		}
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return "", false
	}
	open := rune(s[start])
	close := '}'
	if open == '[' {
		close = ']'
	}
	depth, inStr, esc := 0, false, false
	for i, r := range s[start:] {
		switch {
		case esc:
			esc = false
		case r == '\\' && inStr:
			esc = true
		case r == '"':
			inStr = !inStr
		case inStr:
			// Braces inside a string literal are not nesting.
		case r == open:
			depth++
		case r == close:
			depth--
			if depth == 0 {
				return s[start : start+i+1], true
			}
		}
	}
	return "", false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
