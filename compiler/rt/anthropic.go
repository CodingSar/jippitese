package rt

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// AnthropicURL is the base URL of the Anthropic API.
const AnthropicURL = "https://api.anthropic.com"

// anthropicMaxTokens bounds one reply, thinking included.
const anthropicMaxTokens = 16000

// AnthropicClient speaks the Anthropic Messages API: POST <URL>/v1/messages.
type AnthropicClient struct {
	URL   string
	Model string
	Key   string // sent as x-api-key when set
	Price Price
	HTTP  *http.Client
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type anthropicBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anthropicReply struct {
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      struct {
		Input         int64 `json:"input_tokens"`
		Output        int64 `json:"output_tokens"`
		CacheCreation int64 `json:"cache_creation_input_tokens"`
		CacheRead     int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// Call asks for one JSON answer of type out.
func (c *AnthropicClient) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	return c.answer(ctx, "Answer with a JSON object {\"value\": …} whose value follows the schema.", prompt+"\n\nInput:\n", input, out, false)
}

// Decide asks for one `decide<role>` answer of type out.
func (c *AnthropicClient) Decide(ctx *Ctx, role string, input any, out reflect.Type) (any, error) {
	return c.answer(ctx, decideSystem(role), "Input:\n", input, out, true)
}

// answer asks for one {"value": …} answer under the schema of out, as a
// structured output.
func (c *AnthropicClient) answer(ctx *Ctx, system, user string, input any, out reflect.Type, decide bool) (any, error) {
	in, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":      c.Model,
		"max_tokens": anthropicMaxTokens,
		"system":     system,
		"messages": []anthropicMsg{
			{Role: "user", Content: []any{map[string]any{"type": "text", "text": user + string(in)}}},
		},
		"output_config": map[string]any{
			"format": map[string]any{"type": "json_schema", "schema": answerSchema(out)},
		},
	}
	r, _, err := c.post(ctx, body, llmTimeout(ctx, decide))
	if err != nil {
		return nil, err
	}
	blocks, err := c.blocks(r)
	if err != nil {
		return nil, err
	}
	txt := blocksText(blocks)
	switch r.StopReason {
	case "refusal":
		return nil, &RefusedError{Reason: txt}
	case "max_tokens":
		return nil, fmt.Errorf("%s: reply hit max_tokens", c.URL)
	}
	if txt == "" {
		return nil, errors.New("reply has no content")
	}
	return unwrapAnswer(txt), nil
}

// Turn runs one Agent turn with the tools as client tools. A reply's
// content blocks come back as AgentReply.Raw and are sent back verbatim,
// thinking included, on the next turns.
func (c *AnthropicClient) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	var tools []any
	for _, t := range req.Tools {
		props := map[string]any{}
		names := []string{}
		for _, p := range t.Params {
			props[p.Name] = toolParamSchema(p)
			names = append(names, p.Name)
		}
		tools = append(tools, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": map[string]any{"type": "object", "properties": props, "required": names},
		})
	}
	body := map[string]any{
		"model":      c.Model,
		"max_tokens": anthropicMaxTokens,
		"messages":   anthropicTranscript(req.Transcript),
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	r, cost, err := c.post(ctx, body, llmTimeout(ctx, false))
	if err != nil {
		return AgentReply{}, err
	}
	blocks, err := c.blocks(r)
	if err != nil {
		return AgentReply{}, err
	}
	txt := blocksText(blocks)
	switch r.StopReason {
	case "refusal":
		if txt == "" {
			txt = "refused"
		}
		return AgentReply{Text: txt, Refused: true, Cost: cost}, nil
	case "max_tokens":
		return AgentReply{}, fmt.Errorf("%s: reply hit max_tokens", c.URL)
	}
	raw, err := json.Marshal(r.Content)
	if err != nil {
		return AgentReply{}, err
	}
	reply := AgentReply{Text: txt, Cost: cost, Raw: Json{Raw: raw}}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			args := b.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			reply.Calls = append(reply.Calls, AgentCallReq{ID: b.ID, Name: b.Name, Args: Json{Raw: args}})
		}
	}
	return reply, nil
}

// anthropicTranscript encodes an Agent transcript as alternating user and
// assistant messages. An assistant entry with Raw starts a turn whose
// content is Raw verbatim, so the later entries of that reply are already
// in it; tool results go in the next user message, minus those whose call
// compaction dropped. A transcript ending on the assistant is continued
// with a user message, since a trailing assistant message is a prefill.
func anthropicTranscript(transcript []AgentMsg) []anthropicMsg {
	var msgs []anthropicMsg
	add := func(role string, blocks ...any) {
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
			return
		}
		msgs = append(msgs, anthropicMsg{Role: role, Content: blocks})
	}
	called := map[string]bool{}
	inRaw := false
	for _, m := range transcript {
		switch {
		case m.Role == "assistant" && len(m.Raw.Raw) > 0:
			var blocks []json.RawMessage
			if err := json.Unmarshal(m.Raw.Raw, &blocks); err != nil {
				continue
			}
			if n := len(msgs); n > 0 && msgs[n-1].Role == "assistant" {
				add("user", map[string]any{"type": "text", "text": "Continue."})
			}
			msgs = append(msgs, anthropicMsg{Role: "assistant"})
			for _, b := range blocks {
				msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, b)
				var blk anthropicBlock
				if json.Unmarshal(b, &blk) == nil && blk.Type == "tool_use" {
					called[blk.ID] = true
				}
			}
			inRaw = true
		case m.Role == "assistant" && inRaw:
			// already in the turn's Raw
		case m.Role == "assistant" && m.Tool != "":
			args := json.RawMessage(m.Args.Raw)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			add("assistant", map[string]any{"type": "tool_use", "id": m.ID, "name": m.Tool, "input": args})
			called[m.ID] = true
		case m.Role == "assistant":
			add("assistant", map[string]any{"type": "text", "text": m.Text})
		case m.Role == "tool":
			if called[m.ID] { // compaction may have dropped the call
				add("user", map[string]any{"type": "tool_result", "tool_use_id": m.ID, "content": m.Text})
			}
		default:
			inRaw = false
			add("user", map[string]any{"type": "text", "text": m.Text})
		}
	}
	if n := len(msgs); n > 0 && msgs[n-1].Role == "assistant" {
		add("user", map[string]any{"type": "text", "text": "Continue."})
	}
	return msgs
}

// blocks decodes the content blocks of r.
func (c *AnthropicClient) blocks(r anthropicReply) ([]anthropicBlock, error) {
	out := make([]anthropicBlock, len(r.Content))
	for i, b := range r.Content {
		if err := json.Unmarshal(b, &out[i]); err != nil {
			return nil, fmt.Errorf("%s: content block: %v", c.URL, err)
		}
	}
	return out, nil
}

// blocksText joins the text blocks; thinking is left out.
func blocksText(blocks []anthropicBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// post sends one Messages request and returns the reply and its cost,
// which it charges to the program's meter.
func (c *AnthropicClient) post(ctx *Ctx, body any, timeout time.Duration) (anthropicReply, int64, error) {
	var r anthropicReply
	header := http.Header{}
	if c.Key != "" {
		header.Set("x-api-key", c.Key)
	}
	header.Set("anthropic-version", "2023-06-01")
	if err := postJSON(ctx, c.HTTP, timeout, c.URL, c.URL+"/v1/messages", header, body, &r); err != nil {
		return anthropicReply{}, 0, err
	}
	u := r.Usage
	return r, ctx.charge(c.Price, u.Input+u.CacheCreation+u.CacheRead, u.Output), nil
}
