package emit

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// Agent support (spec 07, rules 17 to 23): the `Tools` of an Agent spec
// and the `tools [...]` expression.

// toolFns are the functions of an Agent impl's `const tools`, in order.
func (f *file) toolFns(impl *checker.ImplInfo) []*checker.FnInfo {
	ci := impl.Consts["tools"]
	if ci == nil {
		return nil
	}
	te, ok := ci.Decl.Value.(*ast.ToolsExpr)
	if !ok {
		f.p.e.failf("impl Agent for %s: `const tools` is not `tools [...]`", impl.Name)
	}
	return f.toolsOf(te)
}

func (f *file) toolsOf(te *ast.ToolsExpr) []*checker.FnInfo {
	var out []*checker.FnInfo
	for _, id := range te.Fns {
		r := f.p.e.prog.Refs[id]
		if r == nil || r.Fn == nil {
			f.p.e.failf("unresolved tool `%s` at %s", id.Name, id.Pos)
		}
		out = append(out, r.Fn)
	}
	return out
}

// toolDesc is the `rt.Tool` the model is told about fi: its name, the first
// line of its doc, its parameters and its effects.
func (f *file) toolDesc(fi *checker.FnInfo) string {
	rtp := f.rt()
	desc, _, _ := strings.Cut(strings.TrimSpace(fi.Doc), "\n")
	names := fi.Decl_Params()
	var ps []string
	for i, t := range fi.Type.Params {
		ps = append(ps, "{Name: "+strconv.Quote(names[i])+", Type: "+strconv.Quote(checker.TypeString(t))+"}")
	}
	var effs []string
	for _, e := range fi.Type.Effects {
		effs = append(effs, strconv.Quote(e.String()))
	}
	return rtp + ".Tool{Name: " + strconv.Quote(fi.Name) + ", Description: " + strconv.Quote(desc) +
		", Params: []" + rtp + ".ToolParam{" + strings.Join(ps, ", ") + "}, Effects: []string{" + strings.Join(effs, ", ") + "}}"
}

// toolCall is the closure that decodes the model's arguments for fi, calls
// it and encodes its result; an `Err` goes back to the model as an error.
func (f *file) toolCall(fi *checker.FnInfo) string {
	rtp := f.rt()
	names := fi.Decl_Params()
	var b strings.Builder
	b.WriteString("func(ctx *" + rtp + ".Ctx, args " + rtp + ".Json) string {\n")
	b.WriteString("\t\t\tvar a struct {\n")
	args := []string{"ctx"}
	for i, t := range fi.Type.Params {
		fld := "P" + strconv.Itoa(i)
		b.WriteString("\t\t\t\t" + fld + " " + f.goType(t) + " `json:" + strconv.Quote(names[i]) + "`\n")
		args = append(args, "a."+fld)
	}
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tif msg, ok := " + rtp + ".ToolArgs(args, &a); !ok {\n\t\t\t\treturn msg\n\t\t\t}\n")
	b.WriteString("\t\t\tr := " + f.fnRef(fi) + "(" + strings.Join(args, ", ") + ")\n")
	if tc, ok := checker.Resolve(fi.Type.Ret).(*checker.TCon); ok && tc.Info.Name == "Result" && tc.Info.Mod == "prelude" {
		b.WriteString("\t\t\tif r.Tag == " + rtp + ".ResultTag_Err {\n\t\t\t\treturn \"error: \" + " + rtp + ".ToolResult(r.Err_0)\n\t\t\t}\n")
		b.WriteString("\t\t\treturn " + rtp + ".ToolResult(r.Ok_0)\n")
	} else {
		b.WriteString("\t\t\treturn " + rtp + ".ToolResult(r)\n")
	}
	b.WriteString("\t\t}")
	return b.String()
}

// toolsExpr is the value of `tools [f, g]`: the descriptions only.
func (g *gen) toolsExpr(te *ast.ToolsExpr) string {
	var ds []string
	for _, fi := range g.f.toolsOf(te) {
		ds = append(ds, g.f.toolDesc(fi))
	}
	return "[]" + g.rt() + ".Tool{" + strings.Join(ds, ", ") + "}"
}

// agentSite is the journal site of `X.run(goal)`: its step key inside a
// workflow, `X.run` elsewhere.
func agentSite(info *checker.CallInfo) string {
	if info.StepKey != "" {
		return info.StepKey
	}
	return info.Impl.Name + ".run"
}
