package emit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

var fmtSprintf = fmt.Sprintf

func (g *gen) call(x *ast.CallExpr) string {
	info := g.prog.Calls[x]
	if info == nil {
		g.e.failf("unresolved call at %s", x.Pos)
	}
	v := g.callValue(x, info)
	if info.Step && !g.dead {
		t := g.typeOf(x)
		return g.rt() + ".Step[" + g.f.goType(t) + "](ctx, " + strconv.Quote(info.StepKey) + ", func() " + g.f.goType(t) + " {\nreturn " + v + "\n})"
	}
	return v
}

func (g *gen) callValue(x *ast.CallExpr, info *checker.CallInfo) string {
	switch info.Kind {
	case checker.CallFn:
		if id, ok := x.Fn.(*ast.Ident); ok && id.Name == "panic" && info.Fn != nil && info.Fn.Mod == "prelude" {
			args := g.exprs(x.Args)
			g.printf("panic(&%s.PanicError{Msg: %s})\n", g.rt(), args[0])
			g.dead = true
			return g.rt() + ".U"
		}
		args := g.exprs(x.Args)
		targs := append(append([]checker.Type{}, info.ImplTypeArgs...), info.TypeArgs...)
		callee := g.f.fnRef(info.Fn)
		if len(targs) == len(info.Fn.TypeParams) && len(targs) > 0 {
			callee += g.f.typeArgs(targs)
		}
		return callee + "(" + ctxArgs(args) + ")"
	case checker.CallMethod:
		if r, ok := info.Receiver.(*ast.RangeExpr); ok && info.Fn.Name == "contains" && info.Fn.Module != nil && info.Fn.Module.IsStd {
			// A literal range is never materialised for membership.
			vals := g.exprs([]ast.Expr{r.Lo, r.Hi, x.Args[0]})
			return g.rt() + ".InRange(" + strings.Join(vals, ", ") + ", " + strconv.FormatBool(r.Incl) + ")"
		}
		vals := g.exprs(append([]ast.Expr{info.Receiver}, x.Args...))
		return g.f.fnRef(info.Fn) + "(" + ctxArgs(vals) + ")"
	case checker.CallInstance:
		args := g.exprs(x.Args)
		if h := substitutable(info); h != "" {
			return g.rt() + "." + h + "(" + ctxArgs(append([]string{g.f.implRef(info.Impl, "spec")}, args...)) + ")"
		}
		inst := g.instance(info.Impl)
		return g.f.fnRef(info.Fn) + "(" + ctxArgs(append([]string{inst}, args...)) + ")"
	case checker.CallVariant:
		args := g.exprs(x.Args)
		return g.variantCtor(info.Variant, g.typeOf(x)) + "(" + strings.Join(args, ", ") + ")"
	case checker.CallNewtype:
		args := g.exprs(x.Args)
		v := g.f.goType(g.typeOf(x)) + "(" + args[0] + ")"
		if inv := g.f.invRef(info.Newtype); inv != "" {
			return inv + "(ctx, " + v + ")"
		}
		return v
	case checker.CallBuiltin:
		return g.builtin(x, info)
	case checker.CallClosure:
		if fe, ok := x.Fn.(*ast.FieldExpr); ok && fe.Name == "publish" {
			if r := g.prog.Fields[fe]; r == nil || r.Kind != checker.FieldRecord {
				if tr, ok := g.prog.Fields[asField(fe.X)]; ok && tr.Kind == checker.FieldTopic {
					args := g.exprs(append([]ast.Expr{fe.X}, x.Args...))
					return g.rt() + ".TopicPublish(" + ctxArgs(args) + ")"
				}
			}
		}
		vals := g.exprs(append([]ast.Expr{x.Fn}, x.Args...))
		return vals[0] + "(" + ctxArgs(vals[1:]) + ")"
	}
	g.e.failf("call kind %d at %s", info.Kind, x.Pos)
	return ""
}

func asField(e ast.Expr) *ast.FieldExpr {
	fe, _ := e.(*ast.FieldExpr)
	return fe
}

func ctxArgs(args []string) string {
	if len(args) == 0 {
		return "ctx"
	}
	return "ctx, " + strings.Join(args, ", ")
}

// substitutes maps the methods of the std behaviours whose impls a
// derived idempotency check replaces by a fake (spec 06 rule 8) to the rt
// helper that routes the call.
var substitutes = map[string]map[string]string{
	"Repository": {"get": "RepoGet", "put": "RepoPut", "delete": "RepoDelete", "query": "RepoQuery"},
	"Cache":      {"get": "CacheGet", "set": "CacheSet", "invalidate": "CacheInvalidate"},
}

func substitutable(info *checker.CallInfo) string {
	b := info.Impl.Behaviour
	if b == nil || b.Module == nil || !b.Module.IsStd {
		return ""
	}
	return substitutes[b.Name][info.Fn.Name]
}

// instance is the one running instance of a behaviour impl.
func (g *gen) instance(impl *checker.ImplInfo) string {
	return g.rt() + ".Instance(ctx, " + strconv.Quote(impl.Name) + ", " + g.f.implRef(impl, "spec") + ".Init)"
}

func (g *gen) builtin(x *ast.CallExpr, info *checker.CallInfo) string {
	spec := g.f.implRef(info.Impl, "spec")
	switch info.Builtin {
	case "enqueue":
		args := g.exprs(x.Args)
		return g.rt() + ".Enqueue(ctx, " + spec + ", " + args[0] + ")"
	case "run_workflow":
		args := g.exprs(x.Args[1:])
		return g.rt() + ".RunWorkflow(ctx, " + spec + ", " + args[0] + ")"
	case "record_workflow":
		args := g.exprs(x.Args[1:])
		return g.rt() + ".RecordWorkflow(ctx, " + spec + ", " + args[0] + ")"
	case "replay_workflow":
		args := g.exprs(x.Args[1:])
		return g.rt() + ".ReplayWorkflow(ctx, " + spec + ", " + args[0] + ", " + args[1] + ")"
	case "tick":
		return g.rt() + ".Tick(ctx, " + spec + ")"
	case "send_signal":
		args := g.exprs(x.Args[1:])
		return g.rt() + ".SendSignal(ctx, " + spec + ", " + args[0] + ")"
	}
	g.e.failf("builtin `%s`", info.Builtin)
	return ""
}
