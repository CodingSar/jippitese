package rt

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// scriptClient answers turns from a script; the last reply sticks.
type scriptClient struct {
	replies []AgentReply
	reqs    []AgentRequest
}

func (c *scriptClient) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	return nil, errors.New("not an llm call")
}

func (c *scriptClient) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	c.reqs = append(c.reqs, req)
	if len(c.reqs) > len(c.replies) {
		return c.replies[len(c.replies)-1], nil
	}
	return c.replies[len(c.reqs)-1], nil
}

func call(args string) AgentReply {
	return AgentReply{Calls: []AgentCallReq{{ID: "c", Name: "lookup", Args: Json{Raw: []byte(args)}}}}
}

// An agent with one tool `lookup`; the state counts tool results and stops
// on the text "done".
func echoSpec(ran *int, gate Gate, budget Budget) *AgentSpec[Unit, int, string, int] {
	return &AgentSpec[Unit, int, string, int]{
		Name:   "Echo",
		Init:   func(*Ctx) (Unit, error) { return U, nil },
		Model:  "smart",
		Budget: budget,
		Tools: []AgentTool[int]{{
			Tool:    Tool{Name: "lookup"},
			Variant: 0,
			Call: func(_ *Ctx, args Json) string {
				var a struct{ Id int64 }
				if msg, ok := ToolArgs(args, &a); !ok {
					return msg
				}
				*ran++
				return ToolResult(a.Id * 2)
			},
		}},
		Begin: func(*Ctx, Unit, string) int { return 0 },
		Observe: func(_ *Ctx, _ Unit, st int, ev Event[int]) int {
			switch ev.Tag {
			case EventTag_Returned:
				return st + 1
			case EventTag_Said:
				if ev.Said_0 == "done" {
					return -1
				}
			}
			return st
		},
		Stop: func(_ *Ctx, _ Unit, st int) Option[string] {
			if st < 0 {
				return Option_Some("finished")
			}
			return Option_None[string]()
		},
		Gate: func(*Ctx, Unit, ToolCall[int]) Gate { return gate },
	}
}

func TestAgentFinishes(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	c := &scriptClient{replies: []AgentReply{call(`{"id": 21}`), {Text: "done"}}}
	ctx.Llm = c
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 5, Max_cost: 100}), "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Finished || out.Finished_0 != "finished" || ran != 1 {
		t.Fatalf("finished after one tool call: %+v, ran %d", out, ran)
	}
	tr := c.reqs[1].Transcript
	if len(tr) != 3 || tr[0].Text != "go" || tr[1].Tool != "lookup" || tr[2].Text != "42" {
		t.Fatalf("transcript is goal, call, result: %+v", tr)
	}
}

func TestAgentTurnBudget(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	ctx.Llm = &scriptClient{replies: []AgentReply{call(`{"id": 1}`)}}
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 3, Max_cost: 100}), "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Stopped || out.Stopped_0.Tag != BudgetExceededTag_Turns || ran != 3 {
		t.Fatalf("stopped on turns after 3: %+v, ran %d", out, ran)
	}
}

func TestAgentCostBudget(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	r := call(`{"id": 1}`)
	r.Cost = 30
	c := &scriptClient{replies: []AgentReply{r}}
	ctx.Llm = c
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 10, Max_cost: 50}), "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Stopped || out.Stopped_0.Tag != BudgetExceededTag_Cost || len(c.reqs) != 2 {
		t.Fatalf("cost is checked before each call, exceeded by at most one: %+v, %d turns", out, len(c.reqs))
	}
}

func TestAgentRefused(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	ctx.Llm = &scriptClient{replies: []AgentReply{{Text: "no", Refused: true}}}
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 3, Max_cost: 1}), "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Refused || out.Refused_0 != "no" {
		t.Fatalf("refused: %+v", out)
	}
}

func TestAgentGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate Gate
		ans  []Result[Unit, Rejected]
		want string
		ran  int
	}{
		{"deny", Gate_Deny("not today"), nil, "denied: not today", 0},
		{"ask-yes", Gate_Ask(), []Result[Unit, Rejected]{Result_Ok[Unit, Rejected](U)}, "2", 1},
		{"ask-no", Gate_Ask(), []Result[Unit, Rejected]{Result_Err[Unit](Rejected{Reason: Option_Some("risky")})}, "denied by a reviewer: risky", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran := 0
			ctx := NewCtx()
			fa := FakeApproveScript(ctx, tc.ans)
			ctx.Approve = fa
			c := &scriptClient{replies: []AgentReply{call(`{"id": 1}`), {Text: "done"}}}
			ctx.Llm = c
			AgentRun(ctx, echoSpec(&ran, tc.gate, Budget{Max_turns: 5, Max_cost: 1}), "Echo.run@t", "go")
			tr := c.reqs[1].Transcript
			if got := tr[len(tr)-1].Text; got != tc.want || ran != tc.ran {
				t.Fatalf("tool result %q (want %q), ran %d", got, tc.want, ran)
			}
			if len(tc.ans) > 0 && !strings.Contains(fa.asked[0].Title, "Echo wants to call lookup") {
				t.Fatalf("review title: %q", fa.asked[0].Title)
			}
		})
	}
}

func TestAgentBadArgs(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	c := &scriptClient{replies: []AgentReply{call(`{"id": "x"}`), {Text: "done"}}}
	ctx.Llm = c
	AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 5, Max_cost: 1}), "Echo.run@t", "go")
	if got := c.reqs[1].Transcript[2].Text; ran != 0 || !strings.HasPrefix(got, "error: invalid arguments") {
		t.Fatalf("bad arguments go back to the model: %q, ran %d", got, ran)
	}
}

func TestAgentJournal(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	ctx.Llm = &scriptClient{replies: []AgentReply{call(`{"id": 1}`), {Text: "done"}}}
	j := NewJournal("W", 1, nil)
	ctx.Journal = j
	spec := echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 5, Max_cost: 1})
	AgentRun(ctx, spec, "Echo.run@t", "go")
	var keys []string
	for _, e := range j.Entries {
		keys = append(keys, e.Key)
	}
	want := "Echo.run@t#1/turn#1 Echo.run@t#1/gate#1 Echo.run@t#1/tool#1 Echo.run@t#1/turn#2"
	if got := strings.Join(keys, " "); got != want {
		t.Fatalf("journal keys:\n got %s\nwant %s", got, want)
	}
	j.reset()
	ctx.Llm = &scriptClient{replies: []AgentReply{{Refused: true}}}
	out := AgentRun(ctx, spec, "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Finished || ran != 1 {
		t.Fatalf("replay reads turns and tool results from the journal: %+v, ran %d", out, ran)
	}
}

func TestFakeLlmTurns(t *testing.T) {
	ran := 0
	ctx := NewCtx()
	ctx.Llm = FakeLlmTurns(ctx, "smart", []TestingReply{TestingReply_Call("lookup", `{"id": 4}`), TestingReply_Say("done")})
	out := AgentRun(ctx, echoSpec(&ran, Gate_Allow(), Budget{Max_turns: 5, Max_cost: 1}), "Echo.run@t", "go")
	if out.Tag != OutcomeTag_Finished || ran != 1 {
		t.Fatalf("scripted turns: %+v, ran %d", out, ran)
	}
}
