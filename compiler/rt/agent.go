package rt

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Agent runtime (spec 07, rules 17 to 23): the model loop behind
// `Name.run(goal)`.

// Tool is the prelude `Tool`: what the model is told about one function.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Params      []ToolParam `json:"params"`
	Effects     []string    `json:"effects"`
}

// ToolParam is one parameter of a Tool; Type is the jippitese type as
// written.
type ToolParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Doc  string `json:"doc,omitempty"`
}

// AgentTool is one entry of an impl's `const tools`: its description, its
// `Tools` variant, and the call that decodes the arguments, runs the
// function and encodes the result.
type AgentTool[T any] struct {
	Tool
	Variant T
	Call    func(ctx *Ctx, args Json) string
}

// AgentSpec is what the compiler generates for `impl Agent for X`.
type AgentSpec[S, St, Ans, T any] struct {
	Name    string
	Init    func(*Ctx) (S, error)
	Model   Role
	Budget  Budget
	Tools   []AgentTool[T]
	Begin   func(*Ctx, S, string) St
	Observe func(*Ctx, S, St, Event[T]) St
	Stop    func(*Ctx, S, St) Option[Ans]
	Gate    func(*Ctx, S, ToolCall[T]) Gate
}

// AgentMsg is one transcript entry. Role is "user", "assistant" or
// "tool"; an assistant entry with Tool set is a tool call, a tool entry
// is its result.
type AgentMsg struct {
	Role string `json:"role"`
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	ID   string `json:"id,omitempty"`
	Args Json   `json:"args,omitempty"`
}

// AgentRequest is one model turn: the goal, the tools and the transcript.
type AgentRequest struct {
	Goal       string     `json:"goal"`
	Tools      []Tool     `json:"tools"`
	Transcript []AgentMsg `json:"transcript"`
}

// AgentCallReq is a tool call the model asked for.
type AgentCallReq struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args Json   `json:"args"`
}

// AgentReply is the model's answer to one turn. Cost is in Money minor
// units.
type AgentReply struct {
	Text    string         `json:"text,omitempty"`
	Calls   []AgentCallReq `json:"calls,omitempty"`
	Refused bool           `json:"refused,omitempty"`
	Cost    int64          `json:"cost,omitempty"`
}

// AgentClient runs one model turn of an Agent loop.
type AgentClient interface {
	Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error)
}

func (boundaryLlm) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	boundary("llm")
	return AgentReply{}, nil
}

// The runtime compacts the transcript past agentCompactAt entries, keeping
// the goal and the last agentCompactKeep.
const (
	agentCompactAt   = 64
	agentCompactKeep = 32
)

// AgentRun implements `Name.run(goal)`. Inside a workflow every model
// turn, gate decision and tool execution is one journal step under
// `<site>#n/turn`, `/gate` and `/tool` (rule 22).
func AgentRun[S, St, Ans, T any](ctx *Ctx, spec *AgentSpec[S, St, Ans, T], site, goal string) Outcome[Ans] {
	client, ok := ctx.Llm.(AgentClient)
	if !ok {
		boundary("llm")
	}
	base := site
	if j := ctx.Journal; j != nil && j.depth == 0 {
		base = j.key(site)
	}
	s := Instance(ctx, spec.Name, spec.Init)
	pure := ctx.child()
	st := spec.Begin(pure, s, goal)
	tools := make([]Tool, len(spec.Tools))
	for i, t := range spec.Tools {
		tools[i] = t.Tool
	}
	transcript := []AgentMsg{{Role: "user", Text: goal}}
	var turns, cost int64
	for {
		if turns >= spec.Budget.Max_turns {
			return Outcome_Stopped[Ans](BudgetExceeded_Turns())
		}
		if cost >= int64(spec.Budget.Max_cost) {
			return Outcome_Stopped[Ans](BudgetExceeded_Cost())
		}
		if len(transcript) > agentCompactAt {
			transcript = append([]AgentMsg{transcript[0]}, transcript[len(transcript)-agentCompactKeep:]...)
			st = spec.Observe(pure, s, st, Event_Compacted[T]())
		}
		req := AgentRequest{Goal: goal, Tools: tools, Transcript: append([]AgentMsg(nil), transcript...)}
		reply := Step(ctx, base+"/turn", func() AgentReply {
			r, err := client.Turn(ctx, string(spec.Model), req)
			if err != nil {
				ctx.failf("%s.run: %s", spec.Name, err.Error())
			}
			return r
		})
		turns++
		cost += reply.Cost
		if reply.Refused {
			return Outcome_Refused[Ans](reply.Text)
		}
		if reply.Text != "" {
			transcript = append(transcript, AgentMsg{Role: "assistant", Text: reply.Text})
			st = spec.Observe(pure, s, st, Event_Said[T](reply.Text))
		}
		for _, c := range reply.Calls {
			transcript = append(transcript, AgentMsg{Role: "assistant", Tool: c.Name, ID: c.ID, Args: c.Args})
			out := agentCall(ctx, spec, s, base, c, &st)
			transcript = append(transcript, AgentMsg{Role: "tool", Tool: c.Name, ID: c.ID, Text: out})
		}
		if a := spec.Stop(pure, s, st); a.Tag == OptionTag_Some {
			return Outcome_Finished(a.Some_0)
		}
	}
}

// agentCall gates and runs one tool call and returns what the model sees.
func agentCall[S, St, Ans, T any](ctx *Ctx, spec *AgentSpec[S, St, Ans, T], s S, base string, c AgentCallReq, st *St) string {
	var tool *AgentTool[T]
	for i := range spec.Tools {
		if spec.Tools[i].Name == c.Name {
			tool = &spec.Tools[i]
		}
	}
	if tool == nil {
		return "error: unknown tool " + strconv.Quote(c.Name)
	}
	pure := ctx.child()
	tc := ToolCall[T]{Tool: tool.Variant, Args: c.Args}
	*st = spec.Observe(pure, s, *st, Event_Called(tc))
	g := Step(ctx, base+"/gate", func() Gate { return spec.Gate(ctx, s, tc) })
	switch g.Tag {
	case GateTag_Deny:
		return "denied: " + g.Deny_0
	case GateTag_Ask:
		review := Review{
			Title: fmt.Sprintf("%s wants to call %s", spec.Name, c.Name),
			Body:  string(c.Args.Raw),
			Data:  Option_Some(c.Args),
		}
		if r := Approve[Unit](ctx, base+"/approve", review); r.Tag == ResultTag_Err {
			why := "denied by a reviewer"
			if r.Err_0.Timed_out {
				why += ": timed out"
			} else if r.Err_0.Reason.Tag == OptionTag_Some {
				why += ": " + r.Err_0.Reason.Some_0
			}
			return why
		}
	}
	out := Step(ctx, base+"/tool", func() string { return tool.Call(ctx, c.Args) })
	*st = spec.Observe(pure, s, *st, Event_Returned(tc, out))
	return out
}

// ToolResult encodes a tool's return value for the model.
func ToolResult(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "error: " + err.Error()
	}
	return string(b)
}

// ToolArgs decodes the arguments of a tool call into the named fields of
// dst, a pointer to a struct the compiler generates per tool; it returns
// what the model sees when they do not decode.
func ToolArgs(args Json, dst any) (string, bool) {
	raw := args.Raw
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return "error: invalid arguments: " + err.Error(), false
	}
	return "", true
}
