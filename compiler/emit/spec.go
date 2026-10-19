package emit

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// specMethods lists, per v0 behaviour, the rt spec type parameters after S
// (associated types or behaviour arguments) and the spec fields filled
// from methods, in struct order.
var specShapes = map[string]struct {
	params  []string // "assoc:Name" or "arg:i"
	methods [][2]string
	consts  [][2]string
}{
	"Service":    {[]string{"assoc:Req", "assoc:Resp", "assoc:E"}, [][2]string{{"Handle", "handle"}, {"Stop", "stop"}, {"Health", "health"}}, nil},
	"Worker":     {[]string{"assoc:Msg", "assoc:E"}, [][2]string{{"Perform", "perform"}, {"Stop", "stop"}, {"Health", "health"}}, [][2]string{{"Retry", "retry"}}},
	"Cron":       {[]string{"assoc:E"}, [][2]string{{"Run", "run"}, {"Stop", "stop"}, {"Health", "health"}}, [][2]string{{"Schedule", "schedule"}}},
	"Workflow":   {[]string{"assoc:Input", "assoc:Output", "assoc:Signal", "assoc:E"}, [][2]string{{"Run", "run"}, {"OnSignal", "on_signal"}}, [][2]string{{"Version", "version"}}},
	"Repository": {[]string{"arg:0", "arg:1"}, [][2]string{{"Get", "get"}, {"Put", "put"}, {"Delete", "delete"}, {"Query", "query"}, {"Stop", "stop"}, {"Health", "health"}}, nil},
	"Cache":      {[]string{"arg:0", "arg:1"}, [][2]string{{"Get", "get"}, {"Set", "set"}, {"Invalidate", "invalidate"}, {"Stop", "stop"}, {"Health", "health"}}, nil},
}

// emitSpec writes `var <Target>_spec = &rt.<B>Spec[…]{…}`: the value the
// runtime drives and scenarios reach through tick/run_workflow/enqueue.
func (f *file) emitSpec(impl *checker.ImplInfo) {
	e := f.p.e
	b := impl.Behaviour.Name
	shape := specShapes[b]
	base := e.implNames[impl]
	name := f.p.alloc(base + "_spec")
	if name != base+"_spec" {
		e.failf("spec name `%s_spec` is taken", base)
	}
	rtp := f.rt()
	s := f.goType(impl.Target)
	targs := []string{s}
	for _, p := range shape.params {
		kind, arg, _ := strings.Cut(p, ":")
		var t checker.Type
		if kind == "assoc" {
			t = impl.Assoc[arg]
		} else {
			i, _ := strconv.Atoi(arg)
			t = impl.BehaviourArgs[i]
		}
		if t == nil {
			e.failf("impl %s for %s: no type for %s", b, base, p)
		}
		targs = append(targs, f.goType(t))
	}
	f.printf("\n// %s is the runtime spec of `impl %s for %s`.\n", name, b, base)
	f.printf("var %s = &%s.%sSpec[%s]{\n", name, rtp, b, strings.Join(targs, ", "))
	f.printf("\tName: %q,\n", impl.Name)
	for _, c := range shape.consts {
		if ci := impl.Consts[c[1]]; ci != nil {
			f.printf("\t%s: %s,\n", c[0], f.constValue(ci))
		}
	}
	if b == "Workflow" {
		var sites []string
		if run := impl.Methods["run"]; run != nil && run.Decl != nil && run.Decl.Body != nil {
			sites = workflowSites(e.prog, run.Decl.Body)
		}
		f.printf("\tSites: []string{")
		for i, st := range sites {
			if i > 0 {
				f.printf(", ")
			}
			f.printf("%q", st)
		}
		f.printf("},\n")
	}
	// Init: call the impl's init with the program config and turn an Err
	// into the error rt.Instance reports.
	initFn := impl.Methods["init"]
	if initFn == nil {
		e.failf("impl %s for %s has no init", b, base)
	}
	f.printf("\tInit: func(ctx *%s.Ctx) (%s, error) {\n", rtp, s)
	f.printf("\t\tcfg, _ := ctx.Config.(%s)\n", f.goType(initFn.Type.Params[0]))
	f.printf("\t\tr := %s(ctx, cfg)\n", f.fnRef(initFn))
	f.printf("\t\tif r.Tag == %s.ResultTag_Err {\n\t\t\tvar zero %s\n\t\t\treturn zero, %s.InitFailed(r.Err_0)\n\t\t}\n", rtp, s, rtp)
	f.printf("\t\treturn r.Ok_0, nil\n\t},\n")
	for _, m := range shape.methods {
		fi := impl.Methods[m[1]]
		if fi != nil && hasMethod(impl, m[1]) {
			f.printf("\t%s: %s,\n", m[0], f.fnRef(fi))
			continue
		}
		switch m[1] {
		case "stop", "on_signal":
			extra := ""
			if m[1] == "on_signal" {
				extra = ", _ " + targs[3]
			}
			f.printf("\t%s: func(ctx *%s.Ctx, _ %s%s) %s.Unit { return %s.U },\n", m[0], rtp, s, extra, rtp, rtp)
		case "health":
			if b == "Repository" || b == "Cache" {
				f.printf("\tHealth: func(ctx *%s.Ctx, _ %s) %s.Health { return %s.HealthUp(ctx) },\n", rtp, s, rtp, rtp)
			} else {
				f.printf("\tHealth: func(ctx *%s.Ctx, s %s) %s.Health { return %s.HealthAggregate(ctx, s) },\n", rtp, s, rtp, rtp)
			}
		default:
			e.failf("impl %s for %s lacks `%s`", b, base, m[1])
		}
	}
	f.printf("}\n")
	if b == "Worker" && e.prog.App != nil {
		if r := e.prog.App.RunFor(impl.Name); r != nil {
			if en := r.Fields["topic"]; en != nil {
				topic := appValueString(en.Value)
				f.printf("\n// %s consumes topic %q (app.jip `run %s { topic: %s }`).\n", base, topic, impl.Name, topic)
				f.printf("func init() {\n\t%s.Subscribe(%q, func(ctx *%s.Ctx, m any) %s.Result[%s.Unit, %s.QueueError] {\n\t\treturn %s.Enqueue(ctx, %s, m.(%s))\n\t})\n}\n",
					rtp, topic, rtp, rtp, rtp, rtp, rtp, name, targs[1])
			}
		}
	}
}

// constValue is a const's value outside any function: consts lower to
// functions of ctx, which a constant expression never reads.
func (f *file) constValue(ci *checker.ConstInfo) string {
	if lit, ok := ci.Decl.Value.(*ast.IntLit); ok {
		return strconv.FormatInt(lit.V, 10)
	}
	return f.constRef(ci) + "(nil)"
}

// workflowSites lists the journal sites of a Workflow.run body in source
// order: every journaled step and every `approve`.
func workflowSites(prog *checker.Program, body *ast.Block) []string {
	var sites []string
	seen := map[string]bool{}
	ast.WalkBlock(body, func(x ast.Expr) {
		var site string
		switch c := x.(type) {
		case *ast.CallExpr:
			if info := prog.Calls[c]; info != nil && info.Step {
				site = info.StepKey
			}
		case *ast.ApproveExpr:
			site = approveSite(c)
		}
		if site != "" && !seen[site] {
			seen[site] = true
			sites = append(sites, site)
		}
	})
	return sites
}
