package rt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"
)

// Model providers (spec 10 rule 3): app.jip `models {}` maps each role to
// a provider and a model; main installs a Models router built from it.

// ModelClient answers `llm<role>` calls, Agent turns and `decide<role>`.
type ModelClient interface {
	LlmClient
	AgentClient
	DecideClient
}

// Models routes calls by role; a role without a client (or with a nil
// one, from a provider NewModel does not speak) reaches the boundary. Once
// effects.llm.budget is spent every call fails with ErrLlmBudget.
type Models map[string]ModelClient

func (m Models) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	c := m[role]
	if c == nil {
		boundary("llm")
	}
	if ctx.llmSpent() {
		return nil, ErrLlmBudget
	}
	return c.Call(ctx, role, prompt, input, out)
}

func (m Models) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	c := m[role]
	if c == nil {
		boundary("llm")
	}
	if ctx.llmSpent() {
		return AgentReply{}, ErrLlmBudget
	}
	return c.Turn(ctx, role, req)
}

func (m Models) Decide(ctx *Ctx, role string, input any, out reflect.Type) (any, error) {
	c := m[role]
	if c == nil {
		boundary("decide")
	}
	if ctx.llmSpent() {
		return nil, ErrLlmBudget
	}
	return c.Decide(ctx, role, input, out)
}

// decideSystem is the instruction of a `decide<role>` request: there is no
// prompt, only the role, the input and the output schema.
func decideSystem(role string) string {
	return "You are the `" + role + "` decision. Read the input and answer with a JSON object {\"value\": …} whose value follows the schema and its descriptions."
}

// answerSchema wraps the schema of out in {"value": …}, since structured
// outputs want an object at the root.
func answerSchema(out reflect.Type) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"value": SchemaOf(out)},
		"required":             []string{"value"},
		"additionalProperties": false,
	}
}

// unwrapAnswer takes the value out of a {"value": …} reply; a reply that
// is not wrapped is returned whole for the caller to decode and report.
func unwrapAnswer(content string) Json {
	var wrap struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(content), &wrap); err != nil || wrap.Value == nil {
		return Json{Raw: []byte(content)}
	}
	return Json{Raw: wrap.Value}
}

// llmTimeout is the request timeout of a provider call: decide.timeout
// for `decide<role>`, llm.timeout otherwise (spec 10 rule 3).
func llmTimeout(ctx *Ctx, decide bool) time.Duration {
	t, def := ctx.Effects.LlmTimeout, DefaultEffects().LlmTimeout
	if decide {
		t, def = ctx.Effects.DecideTimeout, DefaultEffects().DecideTimeout
	}
	if t <= 0 {
		return def
	}
	return t
}

// Base URLs of the providers that speak the chat-completions protocol.
const (
	OpenAIURL = "https://api.openai.com/v1"
	OllamaURL = "http://localhost:11434/v1"
)

// NewModel returns the client of one `models {}` entry, nil for a provider
// the runtime does not speak yet. The API key of role is read from
// JIP_MODELS__<ROLE>__KEY, for `openai` falling back to OPENAI_API_KEY and
// for `anthropic` to ANTHROPIC_API_KEY; its price from
// JIP_MODELS__<ROLE>__PRICE or ModelPrices.
func NewModel(role, provider, url, model string) ModelClient {
	key := os.Getenv("JIP_MODELS__" + strings.ToUpper(role) + "__KEY")
	price := priceFor(role, provider, model)
	switch provider {
	case "openai":
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		url = OpenAIURL
	case "ollama":
		url = OllamaURL
	case "custom":
	case "anthropic":
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		return &AnthropicClient{URL: AnthropicURL, Model: model, Key: key, Price: price}
	default:
		return nil
	}
	return &ChatClient{URL: strings.TrimSuffix(url, "/"), Model: model, Key: key, Price: price}
}

// ChatClient speaks the OpenAI-compatible chat-completions protocol:
// POST <URL>/chat/completions.
type ChatClient struct {
	URL   string
	Model string
	Key   string // sent as a bearer token when set
	Price Price
	HTTP  *http.Client
}

type chatMsg struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Refusal    string         `json:"refusal,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatReply struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
	Usage struct {
		Prompt     int64 `json:"prompt_tokens"`
		Completion int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func text(s string) *string { return &s }

// Call asks for one JSON answer of type out.
func (c *ChatClient) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	return c.answer(ctx, "Answer with a JSON object {\"value\": …} whose value follows the schema.", prompt+"\n\nInput:\n", input, out, false)
}

// Decide asks for one `decide<role>` answer of type out.
func (c *ChatClient) Decide(ctx *Ctx, role string, input any, out reflect.Type) (any, error) {
	return c.answer(ctx, decideSystem(role), "Input:\n", input, out, true)
}

// answer asks for one {"value": …} answer under the schema of out.
func (c *ChatClient) answer(ctx *Ctx, system, user string, input any, out reflect.Type, decide bool) (any, error) {
	in, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model": c.Model,
		"messages": []chatMsg{
			{Role: "system", Content: text(system)},
			{Role: "user", Content: text(user + string(in))},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "answer", "schema": answerSchema(out)},
		},
	}
	msg, _, err := c.post(ctx, body, llmTimeout(ctx, decide))
	if err != nil {
		return nil, err
	}
	if msg.Refusal != "" {
		return nil, &RefusedError{Reason: msg.Refusal}
	}
	if msg.Content == nil {
		return nil, errors.New("reply has no content")
	}
	return unwrapAnswer(*msg.Content), nil
}

// Turn runs one Agent turn with the tools as functions.
func (c *ChatClient) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	var tools []any
	for _, t := range req.Tools {
		props := map[string]any{}
		names := []string{}
		for _, p := range t.Params {
			props[p.Name] = toolParamSchema(p)
			names = append(names, p.Name)
		}
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  map[string]any{"type": "object", "properties": props, "required": names},
			},
		})
	}
	var msgs []chatMsg
	called := map[string]bool{}
	for _, m := range req.Transcript {
		switch {
		case m.Role == "assistant" && m.Tool != "":
			tc := chatToolCall{ID: m.ID, Type: "function"}
			tc.Function.Name = m.Tool
			tc.Function.Arguments = string(m.Args.Raw)
			if tc.Function.Arguments == "" {
				tc.Function.Arguments = "{}"
			}
			msgs = append(msgs, chatMsg{Role: "assistant", ToolCalls: []chatToolCall{tc}})
			called[m.ID] = true
		case m.Role == "tool":
			if called[m.ID] { // compaction may have dropped the call
				msgs = append(msgs, chatMsg{Role: "tool", ToolCallID: m.ID, Content: text(m.Text)})
			}
		default:
			msgs = append(msgs, chatMsg{Role: m.Role, Content: text(m.Text)})
		}
	}
	body := map[string]any{"model": c.Model, "messages": msgs}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	msg, cost, err := c.post(ctx, body, llmTimeout(ctx, false))
	if err != nil {
		return AgentReply{}, err
	}
	if msg.Refusal != "" {
		return AgentReply{Text: msg.Refusal, Refused: true, Cost: cost}, nil
	}
	r := AgentReply{Cost: cost}
	if msg.Content != nil {
		r.Text = *msg.Content
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		r.Calls = append(r.Calls, AgentCallReq{ID: tc.ID, Name: tc.Function.Name, Args: Json{Raw: []byte(args)}})
	}
	return r, nil
}

// post sends one chat-completions request and returns the first choice
// and its cost, which it charges to the program's meter.
func (c *ChatClient) post(ctx *Ctx, body any, timeout time.Duration) (chatMsg, int64, error) {
	var r chatReply
	header := http.Header{}
	if c.Key != "" {
		header.Set("Authorization", "Bearer "+c.Key)
	}
	if err := postJSON(ctx, c.HTTP, timeout, c.URL, c.URL+"/chat/completions", header, body, &r); err != nil {
		return chatMsg{}, 0, err
	}
	cost := ctx.charge(c.Price, r.Usage.Prompt, r.Usage.Completion)
	if len(r.Choices) == 0 {
		return chatMsg{}, cost, fmt.Errorf("%s: reply has no choices", c.URL)
	}
	return r.Choices[0].Message, cost, nil
}

// postJSON posts body to url and decodes a 2xx reply into out; hc nil is a
// client with timeout. name prefixes errors.
func postJSON(ctx *Ctx, hc *http.Client, timeout time.Duration, name, url string, header http.Header, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx.Go, "POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: status %d: %s", name, resp.StatusCode, strings.TrimSpace(string(rb[:min(len(rb), 512)])))
	}
	if err := json.Unmarshal(rb, out); err != nil {
		return fmt.Errorf("%s: %v", name, err)
	}
	return nil
}
