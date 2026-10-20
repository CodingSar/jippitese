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

// ModelClient answers both `llm<role>` calls and Agent turns.
type ModelClient interface {
	LlmClient
	AgentClient
}

// Models routes calls by role; a role without a client (or with a nil
// one, from a provider NewModel does not speak) reaches the boundary.
type Models map[string]ModelClient

func (m Models) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	c := m[role]
	if c == nil {
		boundary("llm")
	}
	return c.Call(ctx, role, prompt, input, out)
}

func (m Models) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	c := m[role]
	if c == nil {
		boundary("llm")
	}
	return c.Turn(ctx, role, req)
}

// Base URLs of the providers that speak the chat-completions protocol.
const (
	OpenAIURL = "https://api.openai.com/v1"
	OllamaURL = "http://localhost:11434/v1"
)

// NewModel returns the client of one `models {}` entry, nil for a provider
// the runtime does not speak yet. The API key of role is read from
// JIP_MODELS__<ROLE>__KEY, for `openai` falling back to OPENAI_API_KEY.
func NewModel(role, provider, url, model string) ModelClient {
	key := os.Getenv("JIP_MODELS__" + strings.ToUpper(role) + "__KEY")
	switch provider {
	case "openai":
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		url = OpenAIURL
	case "ollama":
		url = OllamaURL
	case "custom":
	default:
		return nil
	}
	return &ChatClient{URL: strings.TrimSuffix(url, "/"), Model: model, Key: key}
}

// ChatClient speaks the OpenAI-compatible chat-completions protocol:
// POST <URL>/chat/completions.
type ChatClient struct {
	URL   string
	Model string
	Key   string // sent as a bearer token when set
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
}

func text(s string) *string { return &s }

// Call asks for one JSON answer of type out. The schema wraps the answer in
// {"value": …}, since structured outputs want an object at the root.
func (c *ChatClient) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	in, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"value": SchemaOf(out)},
		"required":             []string{"value"},
		"additionalProperties": false,
	}
	body := map[string]any{
		"model": c.Model,
		"messages": []chatMsg{
			{Role: "system", Content: text("Answer with a JSON object {\"value\": …} whose value follows the schema.")},
			{Role: "user", Content: text(prompt + "\n\nInput:\n" + string(in))},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "answer", "schema": schema},
		},
	}
	msg, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	if msg.Refusal != "" {
		return nil, &RefusedError{Reason: msg.Refusal}
	}
	if msg.Content == nil {
		return nil, errors.New("reply has no content")
	}
	var wrap struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(*msg.Content), &wrap); err != nil || wrap.Value == nil {
		// Not wrapped: Llm decodes the content itself and reports Schema.
		return Json{Raw: []byte(*msg.Content)}, nil
	}
	return Json{Raw: wrap.Value}, nil
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
	msg, err := c.post(ctx, body)
	if err != nil {
		return AgentReply{}, err
	}
	if msg.Refusal != "" {
		return AgentReply{Text: msg.Refusal, Refused: true}, nil
	}
	var r AgentReply
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

// post sends one chat-completions request and returns the first choice.
func (c *ChatClient) post(ctx *Ctx, body any) (chatMsg, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return chatMsg{}, err
	}
	req, err := http.NewRequestWithContext(ctx.Go, "POST", c.URL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return chatMsg{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return chatMsg{}, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return chatMsg{}, err
	}
	if resp.StatusCode/100 != 2 {
		return chatMsg{}, fmt.Errorf("%s: status %d: %s", c.URL, resp.StatusCode, strings.TrimSpace(string(rb[:min(len(rb), 512)])))
	}
	var r chatReply
	if err := json.Unmarshal(rb, &r); err != nil {
		return chatMsg{}, fmt.Errorf("%s: %v", c.URL, err)
	}
	if len(r.Choices) == 0 {
		return chatMsg{}, fmt.Errorf("%s: reply has no choices", c.URL)
	}
	return r.Choices[0].Message, nil
}
