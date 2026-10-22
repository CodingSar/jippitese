package rt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// anthropicServer answers every request with reply and records the request
// bodies and headers.
func anthropicServer(t *testing.T, status int, reply string) (*httptest.Server, *[]map[string]any, *[]http.Header) {
	var bodies []map[string]any
	var headers []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("body: %v", err)
		}
		bodies = append(bodies, m)
		headers = append(headers, r.Header.Clone())
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies, &headers
}

// message is a Messages reply of blocks; usage is 1M input and 100k output
// tokens.
func message(stop string, blocks ...string) string {
	return `{"content":[` + strings.Join(blocks, ",") + `],"stop_reason":"` + stop + `","usage":{"input_tokens":900000,"cache_read_input_tokens":100000,"output_tokens":100000}}`
}

func anthropicCtx(srv *httptest.Server) *Ctx {
	ctx := NewCtx()
	m := Models{"smart": &AnthropicClient{URL: srv.URL, Model: "claude-x", Key: "ak", Price: Price{300, 1500}}}
	ctx.Llm, ctx.Decide = m, m
	return ctx
}

const thinking = `{"type":"thinking","thinking":"hmm","signature":"sig"}`

func TestAnthropicCall(t *testing.T) {
	srv, bodies, headers := anthropicServer(t, 200, message("end_turn", thinking, `{"type":"text","text":"{\"value\":{\"label\":\"spam\",\"score\":3}}"}`))
	ctx := anthropicCtx(srv)
	r := Llm[verdict](ctx, "smart", "Classify.", map[string]string{"text": "hi"})
	if r.Tag != ResultTag_Ok || r.Ok_0 != (verdict{"spam", 3}) {
		t.Fatalf("got %+v", r)
	}
	h := (*headers)[0]
	if h.Get("x-api-key") != "ak" || h.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("headers %v", h)
	}
	b, _ := json.Marshal((*bodies)[0])
	for _, want := range []string{
		`"model":"claude-x"`,
		`"max_tokens":16000`,
		`"output_config":{"format":{"schema":{"additionalProperties":false,"properties":{"value":`,
		`"text":"Classify.\n\nInput:\n{\"text\":\"hi\"}"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("request lacks %s\n%s", want, b)
		}
	}
	// 1M input at 300 + 100k output at 1500 per million: 300 + 150.
	if tokens, cost := ctx.LlmUsage(); tokens != 1_100_000 || cost != 450 {
		t.Errorf("usage %d tokens, cost %d", tokens, cost)
	}
}

func TestAnthropicCallErrors(t *testing.T) {
	srv, _, _ := anthropicServer(t, 200, message("refusal", `{"type":"text","text":"not allowed"}`))
	if r := Llm[verdict](anthropicCtx(srv), "smart", "p", U); r.Err_0 != LlmError_Refused("not allowed") {
		t.Errorf("refused: %+v", r)
	}
	srv, _, _ = anthropicServer(t, 200, message("max_tokens", thinking))
	if r := Llm[verdict](anthropicCtx(srv), "smart", "p", U); r.Err_0.Tag != LlmErrorTag_Transport || !strings.Contains(r.Err_0.Transport_0, "max_tokens") {
		t.Errorf("max_tokens: %+v", r)
	}
	srv, _, _ = anthropicServer(t, 529, `{"type":"error","error":{"type":"overloaded_error"}}`)
	if r := Llm[verdict](anthropicCtx(srv), "smart", "p", U); r.Err_0.Tag != LlmErrorTag_Transport || !strings.Contains(r.Err_0.Transport_0, "status 529") {
		t.Errorf("transport: %+v", r)
	}
}

func TestAnthropicDecide(t *testing.T) {
	srv, bodies, _ := anthropicServer(t, 200, message("end_turn", `{"type":"text","text":"{\"value\":true}"}`))
	if !Decide[bool](anthropicCtx(srv), "smart", map[string]int{"n": 1}) {
		t.Fatal("decided false")
	}
	if sys := (*bodies)[0]["system"].(string); !strings.Contains(sys, "`smart` decision") {
		t.Errorf("system %q", sys)
	}
}

func TestAnthropicTurn(t *testing.T) {
	call := `{"type":"tool_use","id":"t2","name":"lookup","input":{"q":"b"}}`
	srv, bodies, _ := anthropicServer(t, 200, message("tool_use", thinking, `{"type":"text","text":"looking"}`, call))
	ctx := anthropicCtx(srv)
	prev := `[` + thinking + `,{"type":"tool_use","id":"t1","name":"lookup","input":{"q":"a"}},{"type":"tool_use","id":"t1b","name":"lookup","input":{"q":"c"}}]`
	req := AgentRequest{
		Goal:  "find b",
		Tools: []Tool{{Name: "lookup", Description: "Looks up.", Params: []ToolParam{{Name: "q", Type: "Str"}}}},
		Transcript: []AgentMsg{
			{Role: "user", Text: "find b"},
			{Role: "tool", ID: "t0", Text: "orphan"}, // its call was compacted away
			{Role: "assistant", Tool: "lookup", ID: "t1", Args: Json{Raw: []byte(`{"q":"a"}`)}, Raw: Json{Raw: []byte(prev)}},
			{Role: "tool", ID: "t1", Text: "none"},
			{Role: "assistant", Tool: "lookup", ID: "t1b", Args: Json{Raw: []byte(`{"q":"c"}`)}},
			{Role: "tool", ID: "t1b", Text: "nothing"},
		},
	}
	r, err := ctx.Llm.(AgentClient).Turn(ctx, "smart", req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "looking" || len(r.Calls) != 1 || r.Calls[0].ID != "t2" || string(r.Calls[0].Args.Raw) != `{"q":"b"}` || r.Cost != 450 {
		t.Fatalf("reply %+v", r)
	}
	if !strings.Contains(string(r.Raw.Raw), `"signature":"sig"`) {
		t.Errorf("raw lacks the thinking: %s", r.Raw.Raw)
	}
	b, _ := json.Marshal((*bodies)[0])
	var msgs any
	json.Unmarshal([]byte(`[{"content":[{"text":"find b","type":"text"}],"role":"user"},`+
		`{"content":`+prev+`,"role":"assistant"},`+
		`{"content":[{"content":"none","tool_use_id":"t1","type":"tool_result"},{"content":"nothing","tool_use_id":"t1b","type":"tool_result"}],"role":"user"}]`), &msgs)
	wb, _ := json.Marshal(msgs)
	want := `"messages":` + string(wb)
	if !strings.Contains(string(b), want) {
		t.Errorf("messages:\n%s\nwant %s", b, want)
	}
	if !strings.Contains(string(b), `"tools":[{"description":"Looks up.","input_schema":{"properties":{"q":{"description":"Str","type":"string"}},"required":["q"],"type":"object"},"name":"lookup"}]`) {
		t.Errorf("tools: %s", b)
	}
}

func TestAnthropicTranscriptContinues(t *testing.T) {
	said := `[{"type":"text","text":"thinking aloud"}]`
	msgs := anthropicTranscript([]AgentMsg{
		{Role: "user", Text: "go"},
		{Role: "assistant", Text: "thinking aloud", Raw: Json{Raw: []byte(said)}},
		{Role: "assistant", Text: "more", Raw: Json{Raw: []byte(said)}},
	})
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,user,assistant,user" {
		t.Fatalf("roles %v", roles)
	}
}

func TestAgentCostFromPrice(t *testing.T) {
	srv, _, _ := anthropicServer(t, 200, message("tool_use", `{"type":"tool_use","id":"t","name":"lookup","input":{"id":1}}`))
	ran := 0
	ctx := anthropicCtx(srv)
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 10, Max_cost: 1000}), "Echo.run@t", "go")
	// 450 per turn: the third turn is the one past 1000.
	if out.Tag != OutcomeTag_Stopped || out.Stopped_0.Tag != BudgetExceededTag_Cost || ran != 3 {
		t.Fatalf("stopped on cost after 3 turns: %+v, ran %d", out, ran)
	}
}

func TestLlmBudget(t *testing.T) {
	srv, bodies, _ := anthropicServer(t, 200, message("end_turn", `{"type":"text","text":"{\"value\":{\"label\":\"a\",\"score\":1}}"}`))
	ctx := anthropicCtx(srv)
	ctx.Effects.LlmBudgetCost = 400
	if r := Llm[verdict](ctx, "smart", "p", U); r.Tag != ResultTag_Ok {
		t.Fatalf("first call: %+v", r)
	}
	if r := Llm[verdict](ctx, "smart", "p", U); r.Tag != ResultTag_Err || r.Err_0 != LlmError_Budget() {
		t.Fatalf("budget spent: %+v", r)
	}
	ran := 0
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 10, Max_cost: 10_000}), "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Stopped || out.Stopped_0.Tag != BudgetExceededTag_Cost || len(*bodies) != 1 {
		t.Fatalf("agent stopped by llm.budget: %+v, %d requests", out, len(*bodies))
	}
	ctx = anthropicCtx(srv)
	ctx.Effects.LlmBudgetTokens = 2_000_000
	Llm[verdict](ctx, "smart", "p", U)
	if r := Llm[verdict](ctx, "smart", "p", U); r.Tag != ResultTag_Ok {
		t.Fatalf("1.1M of 2M tokens: %+v", r)
	}
	if r := Llm[verdict](ctx, "smart", "p", U); r.Err_0 != LlmError_Budget() {
		t.Fatalf("2.2M of 2M tokens: %+v", r)
	}
}

func TestParseLlmBudget(t *testing.T) {
	for _, c := range []struct {
		in           string
		tokens, cost int64
	}{
		{"", 0, 0},
		{"2_000_000 tokens", 2_000_000, 0},
		{"Money(500)", 0, 500},
		{"1_500", 0, 1500},
	} {
		tk, cost, err := ParseLlmBudget(c.in)
		if err != nil || tk != c.tokens || cost != c.cost {
			t.Errorf("%q: %d %d %v", c.in, tk, cost, err)
		}
	}
	for _, bad := range []string{"tokens", "-1 tokens", "Money(x)", "5 dollars", "0"} {
		if _, _, err := ParseLlmBudget(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if p, err := ParsePrice("1_000/5_000"); err != nil || p != (Price{1000, 5000}) {
		t.Errorf("price %+v %v", p, err)
	}
	for _, bad := range []string{"3", "a/b", "-1/2"} {
		if _, err := ParsePrice(bad); err == nil {
			t.Errorf("price %q accepted", bad)
		}
	}
}
