package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/milhamsuryapratama/diff-checker/internal/trace"
)

// clipArgs renders tool arguments compactly for the trace.
func clipArgs(raw []byte) string { return clip(string(raw), 160) }

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// maxToolRounds bounds how many times the model may go back for more context
// before it must answer.
//
// A bound is required, not defensive: a model that keeps calling get_paragraph
// with no stopping condition would walk the whole document one call at a time
// and cost more than simply having pasted the document in. Five rounds is
// comfortably above what a well-formed analysis needs and far below the point
// where the tool loop stops being cheaper than the naive approach.
const maxToolRounds = 5

// completeJSONWithTools runs a bounded tool-calling loop and decodes the final
// answer into out.
//
// Streaming is disabled for this loop on purpose: with streaming, tool calls
// arrive as deltas that must be reassembled by index, and a mis-assembled call
// is indistinguishable from one the model never made. The analyze node is not
// user-facing token-by-token — the progress page reports node completion, not
// partial text — so there is nothing to gain from streaming here and a real
// correctness risk in it.
func (c caller) completeJSONWithTools(
	ctx context.Context,
	system, user string,
	toolset map[string]tool.Tool,
	out any,
	desc string,
) error {
	messages := []model.Message{
		model.NewSystemMessage(system),
		model.NewUserMessage(user),
	}

	budget := c.entry.MaxOutputTokens
	if budget <= 0 {
		budget = defaultMaxOutputTokens
	}

	for round := 0; round < maxToolRounds; round++ {
		req := &model.Request{Messages: messages, Tools: toolset}
		req.Stream = false
		req.MaxTokens = &budget

		rsp, err := c.completeRaw(ctx, req)
		if err != nil {
			return err
		}
		if len(rsp.Choices) == 0 {
			return fmt.Errorf("model tidak mengembalikan pilihan jawaban")
		}
		msg := rsp.Choices[0].Message

		if len(msg.ToolCalls) == 0 {
			// The model is done gathering context. Ask for the structured
			// answer in a final call with no tools attached, so the schema is
			// enforced and the model cannot start another lookup instead of
			// answering.
			return c.finalJSON(ctx, messages, msg.Content, out, desc)
		}

		messages = append(messages, msg)
		for _, call := range msg.ToolCalls {
			// A tool call is the most legible form of model reasoning there is:
			// it shows exactly what the model went looking for before deciding.
			c.recorder().Note(c.traceNode, trace.KindToolCall,
				fmt.Sprintf("%s(%s)", call.Function.Name, clipArgs(call.Function.Arguments)))

			reply := c.runTool(ctx, toolset, call)
			c.recorder().Note(c.traceNode, trace.KindToolResult, clip(reply.Content, 400))
			messages = append(messages, reply)
		}
	}

	// Out of rounds. Force an answer from whatever context was gathered rather
	// than failing the node: a partial analysis marked low-confidence is more
	// useful to a reviewer than no analysis at all.
	return c.finalJSON(ctx, messages,
		"Batas pemanggilan tool tercapai. Jawab sekarang dengan konteks yang sudah kamu kumpulkan.",
		out, desc)
}

// runTool executes one tool call and renders the reply as a tool message.
//
// A tool that errors returns the error to the model as content rather than
// aborting the node: "that paragraph index does not exist" is information the
// model can act on, and is exactly the feedback that stops it guessing.
func (c caller) runTool(ctx context.Context, toolset map[string]tool.Tool, call model.ToolCall) model.Message {
	name := call.Function.Name
	t, ok := toolset[name]
	if !ok {
		return model.NewToolMessage(call.ID, name,
			fmt.Sprintf(`{"error":"tool %q tidak tersedia"}`, name))
	}
	callable, ok := t.(tool.CallableTool)
	if !ok {
		return model.NewToolMessage(call.ID, name,
			fmt.Sprintf(`{"error":"tool %q tidak dapat dipanggil"}`, name))
	}

	result, err := callable.Call(ctx, call.Function.Arguments)
	if err != nil {
		return model.NewToolMessage(call.ID, name,
			fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return model.NewToolMessage(call.ID, name,
			fmt.Sprintf(`{"error":"hasil tool tidak dapat diserialisasi: %s"}`, err))
	}
	return model.NewToolMessage(call.ID, name, string(payload))
}

// finalJSON asks for the structured answer once the context gathering is done.
func (c caller) finalJSON(
	ctx context.Context,
	history []model.Message,
	nudge string,
	out any,
	desc string,
) error {
	messages := append([]model.Message{}, history...)
	if nudge != "" {
		messages = append(messages, model.NewAssistantMessage(nudge))
	}
	messages = append(messages, model.NewUserMessage(
		"Sekarang keluarkan jawaban akhir dalam JSON."+contractFor(out)))

	return c.completeStructured(ctx, out, func(maxTokens int) *model.Request {
		req := model.NewRequest(messages, model.WithStructuredOutputJSON(out, true, desc))
		req.Stream = true
		req.MaxTokens = &maxTokens
		return req
	})
}

// completeRaw is the non-streaming sibling of complete, used by the tool loop
// where the assembled message matters more than incremental text.
func (c caller) completeRaw(ctx context.Context, req *model.Request) (*model.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := sleepBackoff(ctx, attempt); err != nil {
				return nil, err
			}
		}
		rsp, err := c.attemptRaw(ctx, req)
		if err == nil {
			return rsp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("node %s: %d percobaan gagal: %w", c.node, maxAttempts, lastErr)
}

func (c caller) attemptRaw(ctx context.Context, req *model.Request) (*model.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	ch, err := c.entry.Model.GenerateContent(ctx, req)
	if err != nil {
		return nil, err
	}

	var final *model.Response
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case rsp, ok := <-ch:
			if !ok {
				if final == nil {
					return nil, fmt.Errorf("model tidak mengembalikan respons")
				}
				return final, nil
			}
			if rsp == nil {
				continue
			}
			if rsp.Error != nil {
				return nil, fmt.Errorf("model error: %s", rsp.Error.Message)
			}
			if rsp.Usage != nil {
				c.recordUsage(rsp.Usage)
			}
			if len(rsp.Choices) > 0 {
				final = rsp
			}
		}
	}
}

// recordUsage attributes a raw response's tokens to this node.
func (c caller) recordUsage(u *model.Usage) {
	var n NodeUsage
	n.Model = c.entry.Name
	n.Input = u.PromptTokens
	n.Output = u.CompletionTokens
	n.CacheRead = u.PromptTokensDetails.CacheReadTokens
	n.CacheWrite = u.PromptTokensDetails.CacheCreationTokens
	n.InputUncach = n.Input - n.CacheRead - n.CacheWrite
	if n.InputUncach < 0 {
		n.InputUncach = n.Input
	}
	n.USD = c.entry.Rates.Cost(n.InputUncach, n.CacheRead, n.CacheWrite, n.Output)
	c.usage.Add(c.node, n)
}
