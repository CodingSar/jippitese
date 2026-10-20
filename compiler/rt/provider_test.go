package rt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type verdict struct {
	Label string `json:"label"`
	Score int64  `json:"score"`
}

// chatServer answers every request with reply and records the request
// bodies and authorization headers.
func chatServer(t *testing.T, status int, reply string) (*httptest.Server, *[]map[string]any, *[]string) {
	var bodies []map[string]any
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("body: %v", err)
		}
		bodies = append(bodies, m)
		auths = append(auths, r.Header.Get("Authorization"))
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies, &auths
}

func content(s string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": s}}}})
	return string(b)
}

func llmCtx(srv *httptest.Server) *Ctx {
	ctx := NewCtx()
	ctx.Llm = Models{"smart": &ChatClient{URL: srv.URL, Model: "m1", Key: "k1"}}
	return ctx
}

func TestChatCallRecord(t *testing.T) {
	srv, bodies, auths := chatServer(t, 200, content(`{"value":{"label":"spam","score":3}}`))
	r := Llm[verdict](llmCtx(srv), "smart", "Classify.", map[string]string{"text": "hi"})
	if r.Tag != ResultTag_Ok || r.Ok_0 != (verdict{"spam", 3}) {
		t.Fatalf("got %+v", r)
	}
	if (*auths)[0] != "Bearer k1" {
		t.Errorf("auth %q", (*auths)[0])
	}
	b := (*bodies)[0]
	if b["model"] != "m1" {
		t.Errorf("model %v", b["model"])
	}
	msgs := b["messages"].([]any)
	user := msgs[1].(map[string]any)["content"].(string)
	if !strings.HasPrefix(user, "Classify.") || !strings.Contains(user, `{"text":"hi"}`) {
		t.Errorf("user message %q", user)
	}
	rf, _ := json.Marshal(b["response_format"])
	if !strings.Contains(string(rf), `"label":{"type":"string"}`) || !strings.Contains(string(rf), `"score":{"type":"integer"}`) {
		t.Errorf("response_format %s", rf)
	}
}

func TestChatCallEnumUnwrapped(t *testing.T) {
	srv, _, _ := chatServer(t, 200, content(`{"Refused":"no"}`))
	r := Llm[LlmError](llmCtx(srv), "smart", "p", U)
	if r.Tag != ResultTag_Ok || r.Ok_0 != LlmError_Refused("no") {
		t.Fatalf("got %+v", r)
	}
}

func TestChatCallErrors(t *testing.T) {
	srv, _, _ := chatServer(t, 200, content(`{"value":{"label":3}}`))
	if r := Llm[verdict](llmCtx(srv), "smart", "p", U); r.Tag != ResultTag_Err || r.Err_0.Tag != LlmErrorTag_Schema {
		t.Errorf("schema: %+v", r)
	}
	refusal := `{"choices":[{"message":{"role":"assistant","content":null,"refusal":"not allowed"}}]}`
	srv, _, _ = chatServer(t, 200, refusal)
	if r := Llm[verdict](llmCtx(srv), "smart", "p", U); r.Err_0 != LlmError_Refused("not allowed") {
		t.Errorf("refused: %+v", r)
	}
	srv, _, _ = chatServer(t, 429, `{"error":"slow down"}`)
	r := Llm[verdict](llmCtx(srv), "smart", "p", U)
	if r.Err_0.Tag != LlmErrorTag_Transport || !strings.Contains(r.Err_0.Transport_0, "status 429") {
		t.Errorf("transport: %+v", r)
	}
}

func TestModelsUnknownRole(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no boundary panic")
		}
	}()
	ctx := NewCtx()
	ctx.Llm = Models{}
	Llm[verdict](ctx, "fast", "p", U)
}

func TestChatTurn(t *testing.T) {
	reply := `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"t2","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"b\"}"}}]}}]}`
	srv, bodies, _ := chatServer(t, 200, reply)
	ctx := llmCtx(srv)
	req := AgentRequest{
		Goal:  "find b",
		Tools: []Tool{{Name: "lookup", Description: "Looks up.", Params: []ToolParam{{Name: "q", Type: "Str"}, {Name: "ids", Type: "List<Int>", Doc: "ids"}}}},
		Transcript: []AgentMsg{
			{Role: "user", Text: "find b"},
			{Role: "tool", ID: "t0", Text: "orphan"}, // its call was compacted away
			{Role: "assistant", Tool: "lookup", ID: "t1", Args: Json{Raw: []byte(`{"q":"a"}`)}},
			{Role: "tool", ID: "t1", Text: "none"},
		},
	}
	r, err := ctx.Llm.(AgentClient).Turn(ctx, "smart", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Calls) != 1 || r.Calls[0].ID != "t2" || r.Calls[0].Name != "lookup" || string(r.Calls[0].Args.Raw) != `{"q":"b"}` {
		t.Fatalf("reply %+v", r)
	}
	got, _ := json.Marshal((*bodies)[0])
	for _, want := range []string{
		`"tools":[{"function":{"description":"Looks up."`,
		`"q":{"description":"Str","type":"string"}`,
		`"ids":{"description":"List\u003cInt\u003e: ids"}`,
		`"tool_calls":[{"function":{"arguments":"{\"q\":\"a\"}","name":"lookup"},"id":"t1","type":"function"}]`,
		`{"content":"none","role":"tool","tool_call_id":"t1"}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("request lacks %s\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "orphan") {
		t.Errorf("orphan tool result sent: %s", got)
	}
}

func TestNewModel(t *testing.T) {
	t.Setenv("JIP_MODELS__SMART__KEY", "sk")
	t.Setenv("OPENAI_API_KEY", "ok")
	if c := NewModel("smart", "custom", "http://h/v1/", "m").(*ChatClient); c.URL != "http://h/v1" || c.Key != "sk" || c.Model != "m" {
		t.Errorf("custom %+v", c)
	}
	if c := NewModel("fast", "openai", "", "gpt").(*ChatClient); c.URL != OpenAIURL || c.Key != "ok" {
		t.Errorf("openai %+v", c)
	}
	if c := NewModel("fast", "ollama", "", "llama").(*ChatClient); c.URL != OllamaURL || c.Key != "" {
		t.Errorf("ollama %+v", c)
	}
	if NewModel("smart", "anthropic", "", "c") != nil {
		t.Error("anthropic is not spoken yet")
	}
}

type tree struct {
	Name     string `json:"name"`
	Children []tree `json:"children"`
}

func TestSchemaOf(t *testing.T) {
	for _, c := range []struct {
		t    reflect.Type
		want string
	}{
		{reflect.TypeFor[Option[int64]](), `{"anyOf":[{"type":"integer"},{"type":"null"}]}`},
		{reflect.TypeFor[Map[string, bool]](), `{"additionalProperties":{"type":"boolean"},"type":"object"}`},
		{reflect.TypeFor[Map[int64, string]](), `{"items":{"additionalProperties":false,"properties":{"F0":{"type":"integer"},"F1":{"type":"string"}},"required":["F0","F1"],"type":"object"},"type":"array"}`},
		{reflect.TypeFor[Set[string]](), `{"items":{"type":"string"},"type":"array"}`},
		{reflect.TypeFor[Json](), `{}`},
		{reflect.TypeFor[[]byte](), `{"contentEncoding":"base64","type":"string"}`},
		{reflect.TypeFor[tree](), `{"additionalProperties":false,"properties":{"children":{"items":{},"type":"array"},"name":{"type":"string"}},"required":["name","children"],"type":"object"}`},
		{reflect.TypeFor[Result[int64, string]](), `{"anyOf":[{"additionalProperties":false,"properties":{"Ok":{"type":"integer"}},"required":["Ok"],"type":"object"},{"additionalProperties":false,"properties":{"Err":{"type":"string"}},"required":["Err"],"type":"object"}]}`},
	} {
		got, _ := json.Marshal(SchemaOf(c.t))
		if string(got) != c.want {
			t.Errorf("%v:\n got %s\nwant %s", c.t, got, c.want)
		}
	}
	got, _ := json.Marshal(SchemaOf(reflect.TypeFor[LlmError]()))
	if !strings.Contains(string(got), `{"const":"Budget"}`) || !strings.Contains(string(got), `"properties":{"Schema":{"type":"string"}}`) {
		t.Errorf("LlmError: %s", got)
	}
}
