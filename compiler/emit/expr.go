package emit

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// gen lowers one function body. Expressions return a Go expression string;
// anything that needs statements (blocks, if, match, `?`, record update)
// writes them to w first and returns a temp (`_tN`).
type gen struct {
	f    *file
	e    *emitter
	prog *checker.Program
	fn   *checker.FnInfo
	w    *strings.Builder
	tmp  *int
	dead bool // the last statement written cannot fall through

	ret      checker.Type // return type of the innermost function or closure
	scenario bool         // `?` fails the scenario instead of returning
	selfName string
	selfType checker.Type
	olds     map[*ast.OldExpr]string
}

func newGen(f *file, fi *checker.FnInfo) *gen {
	n := 0
	g := &gen{f: f, e: f.p.e, prog: f.p.e.prog, fn: fi, w: &strings.Builder{}, tmp: &n, olds: map[*ast.OldExpr]string{}}
	if fi != nil {
		g.ret = fi.Type.Ret
		if fi.HasSelf {
			g.selfName = paramGoName(g.prog, &fi.Decl.Params[0])
		}
	}
	return g
}

// sub is a gen for a nested Go function (closure) sharing the temp counter.
func (g *gen) sub(ret checker.Type) *gen {
	s := *g
	s.w = &strings.Builder{}
	s.ret = ret
	s.dead = false
	return &s
}

func (g *gen) newTmp() string {
	*g.tmp++
	return "_t" + strconv.Itoa(*g.tmp)
}

func (g *gen) printf(format string, args ...any) {
	if g.dead {
		return
	}
	g.w.WriteString(sprintf(format, args...))
}

func (g *gen) rt() string { return g.f.rt() }

func (g *gen) typeOf(e ast.Expr) checker.Type {
	t, ok := g.prog.Types[e]
	if !ok {
		g.e.failf("no type for expression at %s", e.ExprPos())
	}
	return t
}

func (g *gen) line(pos ast.Pos) {
	if g.dead || g.e.opts.GoPositions || pos.File == "" {
		return
	}
	g.w.WriteString("\n//line " + pos.File + ":" + strconv.Itoa(pos.Line) + ":" + strconv.Itoa(pos.Col) + "\n")
}

// capture runs f with a fresh statement buffer and returns what it wrote.
func (g *gen) capture(f func() string) (stmts, val string, dead bool) {
	old, oldDead := g.w, g.dead
	g.w = &strings.Builder{}
	g.dead = false
	val = f()
	stmts, dead = g.w.String(), g.dead
	g.w, g.dead = old, oldDead
	return
}

var trivialRe = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*|-?[0-9][0-9_.eE+-]*|"(?:[^"\\]|\\.)*"|true|false|int64\([0-9-]+\)|float64\([0-9.eE+-]+\))$`)

func trivial(v string) bool { return trivialRe.MatchString(v) }

// exprs lowers a list left to right, spilling earlier values to temps when
// a later one needs statements, so evaluation order is preserved.
func (g *gen) exprs(es []ast.Expr) []string {
	vals := make([]string, 0, len(es))
	for _, x := range es {
		stmts, v, dead := g.capture(func() string { return g.expr(x) })
		if stmts != "" || dead {
			for j := range vals {
				if !trivial(vals[j]) {
					t := g.newTmp()
					g.printf("%s := %s\n", t, vals[j])
					vals[j] = t
				}
			}
			g.printf("%s", stmts)
			if dead {
				g.dead = true
			}
		}
		vals = append(vals, v)
	}
	return vals
}

// ---- sinks ------------------------------------------------------------------

// sink receives the value of an expression lowered in statement context.
type sink func(v string)

func (g *gen) discard(v string) {
	if v == "" || trivial(v) || v == g.rt()+".U" {
		return
	}
	g.printf("_ = %s\n", v)
}

func (g *gen) assignTo(t string) sink { return func(v string) { g.printf("%s = %s\n", t, v) } }

func (g *gen) returnSink(v string) {
	g.printf("return %s\n", v)
	g.dead = true
}

// lowerInto lowers e in statement context, handing its value to s.
func (g *gen) lowerInto(e ast.Expr, s sink) {
	if g.dead {
		return
	}
	switch x := e.(type) {
	case *ast.Block:
		g.block(x, s)
		return
	case *ast.IfExpr:
		g.ifExpr(x, s)
		return
	case *ast.MatchExpr:
		g.match(x, s)
		return
	case *ast.WhileExpr:
		g.while(x)
		s(g.rt() + ".U")
		return
	case *ast.ForExpr:
		g.forExpr(x)
		s(g.rt() + ".U")
		return
	case *ast.SpawnExpr:
		g.block(x.Body, s)
		return
	}
	v := g.expr(e)
	if !g.dead {
		s(v)
	}
}

// ---- function bodies ------------------------------------------------------

// fnBody writes the body of a declared function: pre checks, old captures,
// the body, post checks.
func (g *gen) fnBody(d *ast.FnDecl, ret checker.Type) {
	for _, p := range d.Pre {
		g.contract("pre", p)
	}
	if len(d.Post) == 0 {
		g.lowerInto(d.Body, g.returnSink)
		g.finish()
		return
	}
	for _, p := range d.Post {
		ast.WalkExpr(p, func(x ast.Expr) {
			if o, ok := x.(*ast.OldExpr); ok {
				t := g.newTmp()
				g.printf("%s := %s\n", t, g.expr(o.X))
				g.olds[o] = t
			}
		})
	}
	s := g.sub(ret)
	s.lowerInto(d.Body, s.returnSink)
	s.finish()
	g.printf("_ret := func() %s {\n%s}()\n", g.f.goType(ret), s.w.String())
	for _, p := range d.Post {
		g.contract("post", p)
	}
	g.printf("return _ret\n")
	g.dead = true
}

// finish closes a function whose body may not end in a Go terminating
// statement.
func (g *gen) finish() {
	if !g.dead {
		g.e.failf("function body falls through")
	}
}

// contract writes one guarded check: `if rt.Contracts.Pre && !(c) { fail }`.
func (g *gen) contract(kind string, c ast.Expr) {
	flag := map[string]string{"pre": "Pre", "post": "Post", "inv": "Inv"}[kind]
	stmts, v, _ := g.capture(func() string { return g.expr(c) })
	at := c.ExprPos().String()
	g.printf("if %s.Contracts.%s {\n%sif !(%s) {\n%s.ContractFail(ctx, %q, %q, %q)\n}\n}\n",
		g.rt(), flag, stmts, v, g.rt(), kind, checker.ExprString(c), at)
}

// implParams sets self for consts lowered inside an impl.
func (g *gen) implParams(impl *checker.ImplInfo) {}

// ---- statements and blocks -------------------------------------------------

func (g *gen) block(b *ast.Block, s sink) {
	for _, st := range b.Stmts {
		if g.dead {
			return
		}
		g.stmt(st)
	}
	if g.dead {
		return
	}
	if b.Tail == nil {
		s(g.rt() + ".U")
		return
	}
	g.lowerInto(b.Tail, s)
}

func (g *gen) stmt(st ast.Stmt) {
	g.line(st.StmtPos())
	switch s := st.(type) {
	case *ast.LetStmt:
		if bp, ok := s.Pat.(*ast.BindPat); ok {
			l := g.prog.PatBinds[bp]
			v := g.expr(s.Value)
			name := g.localGo(l, bp.Name)
			if s.Type != nil || l == nil {
				g.printf("var %s %s = %s\n_ = %s\n", name, g.f.goType(g.typeOf(s.Value)), v, name)
			} else {
				g.printf("var %s %s = %s\n_ = %s\n", name, g.f.goType(l.Type), v, name)
			}
			return
		}
		v := g.expr(s.Value)
		t := g.newTmp()
		g.printf("%s := %s\n_ = %s\n", t, v, t)
		_, binds := g.pattern(s.Pat, t, g.typeOf(s.Value))
		for _, b := range binds {
			g.printf("%s", b)
		}
	case *ast.VarStmt:
		l := g.prog.VarDefs[s]
		v := g.expr(s.Value)
		name := g.localGo(l, s.Name)
		g.printf("var %s %s = %s\n_ = %s\n", name, g.f.goType(l.Type), v, name)
	case *ast.AssignStmt:
		r := g.prog.Refs[s.Target]
		name := localName(s.Target.Name)
		var t checker.Type
		if r != nil && r.Local != nil {
			name = g.localGo(r.Local, s.Target.Name)
			t = r.Local.Type
		}
		v := g.expr(s.Value)
		if s.Op == "=" {
			g.printf("%s = %s\n", name, v)
			return
		}
		op := strings.TrimSuffix(s.Op, "=")
		g.printf("%s = %s\n", name, g.arith(op, name, v, t))
	case *ast.ExprStmt:
		g.lowerInto(s.X, g.discard)
	default:
		g.e.failf("unknown statement %T", st)
	}
}

func (g *gen) localGo(l *checker.Local, name string) string {
	if l != nil && l.Go != "" {
		return localName(l.Go)
	}
	return localName(name)
}

// ---- control flow -----------------------------------------------------------

// valueOf lowers a control-flow expression in value context into a temp.
func (g *gen) valueOf(e ast.Expr) string {
	t := g.typeOf(e)
	if checker.IsUnit(t) {
		g.lowerInto(e, g.discard)
		return g.rt() + ".U"
	}
	tmp := g.newTmp()
	g.printf("var %s %s\n", tmp, g.f.goType(t))
	g.lowerInto(e, g.assignTo(tmp))
	return tmp
}

func (g *gen) ifExpr(x *ast.IfExpr, s sink) {
	cond := g.expr(x.Cond)
	if g.dead {
		return
	}
	g.printf("if %s {\n", cond)
	g.dead = false
	g.block(x.Then, s)
	thenDead := g.dead
	g.dead = false
	switch el := x.Else.(type) {
	case nil:
		g.printf("}\n")
		s(g.rt() + ".U")
		thenDead = false
	case *ast.Block:
		g.printf("} else {\n")
		g.block(el, s)
		elseDead := g.dead
		g.dead = false
		g.printf("}\n")
		thenDead = thenDead && elseDead
	case *ast.IfExpr:
		g.printf("} else {\n")
		g.ifExpr(el, s)
		elseDead := g.dead
		g.dead = false
		g.printf("}\n")
		thenDead = thenDead && elseDead
	}
	if thenDead {
		g.dead = true
	}
}

func (g *gen) while(x *ast.WhileExpr) {
	stmts, cond, _ := g.capture(func() string { return g.expr(x.Cond) })
	if stmts == "" {
		g.printf("for %s {\n", cond)
	} else {
		g.printf("for {\n%sif !(%s) {\nbreak\n}\n", stmts, cond)
	}
	g.block(x.Body, g.discard)
	g.dead = false
	g.printf("}\n")
}

func (g *gen) forExpr(x *ast.ForExpr) {
	if r, ok := x.Iter.(*ast.RangeExpr); ok {
		lo, hi := g.expr(r.Lo), g.expr(r.Hi)
		i := g.newTmp()
		op := "<"
		if r.Incl {
			op = "<="
		}
		g.printf("for %s := %s; %s %s %s; %s++ {\n", i, lo, i, op, hi, i)
		_, binds := g.pattern(x.Pat, i, g.elemType(g.typeOf(x.Iter)))
		for _, b := range binds {
			g.printf("%s", b)
		}
	} else {
		it := g.expr(x.Iter)
		el := g.newTmp()
		g.printf("for _, %s := range %s {\n", el, g.iterable(it, g.typeOf(x.Iter)))
		_, binds := g.pattern(x.Pat, el, g.elemType(g.typeOf(x.Iter)))
		for _, b := range binds {
			g.printf("%s", b)
		}
	}
	g.block(x.Body, g.discard)
	g.dead = false
	g.printf("}\n")
}

// iterable adapts a collection to a Go range operand.
func (g *gen) iterable(v string, t checker.Type) string {
	if tc, ok := checker.Zonk(t).(*checker.TCon); ok {
		switch tc.Info.Name {
		case "Set", "MutList", "MutSet":
			return v + ".Items()"
		}
	}
	return v
}

func (g *gen) elemType(t checker.Type) checker.Type {
	if tc, ok := checker.Zonk(t).(*checker.TCon); ok && len(tc.Args) == 1 {
		return tc.Args[0]
	}
	return t
}

// ---- expressions --------------------------------------------------------------

func (g *gen) expr(e ast.Expr) string {
	if g.dead {
		return g.rt() + ".U"
	}
	switch x := e.(type) {
	case *ast.IntLit:
		return g.numLit(strconv.FormatInt(x.V, 10), g.typeOf(e))
	case *ast.FloatLit:
		s := strconv.FormatFloat(x.V, 'g', -1, 64)
		return "float64(" + s + ")"
	case *ast.BoolLit:
		return strconv.FormatBool(x.V)
	case *ast.CharLit:
		return strconv.QuoteRune(x.V)
	case *ast.BytesLit:
		return "[]byte(" + strconv.Quote(string(x.V)) + ")"
	case *ast.DurationLit:
		return g.f.gotime() + ".Duration(" + strconv.FormatInt(x.Ns, 10) + ")"
	case *ast.StrLit:
		return g.strLit(x)
	case *ast.Ident:
		return g.ident(x)
	case *ast.SelfExpr:
		if g.selfName == "" {
			g.e.failf("self outside a method at %s", x.Pos)
		}
		return g.selfName
	case *ast.FieldExpr:
		return g.field(x)
	case *ast.CallExpr:
		return g.call(x)
	case *ast.UnaryExpr:
		v := g.expr(x.X)
		switch x.Op {
		case "!":
			return "!(" + v + ")"
		case "-":
			if g.isInt(g.typeOf(x.X)) {
				return g.rt() + ".Sub(ctx, 0, " + v + ")"
			}
			return "-(" + v + ")"
		}
		g.e.failf("unary %s", x.Op)
	case *ast.BinaryExpr:
		return g.binary(x)
	case *ast.TryExpr:
		return g.try(x)
	case *ast.Block, *ast.IfExpr, *ast.MatchExpr, *ast.WhileExpr, *ast.ForExpr, *ast.SpawnExpr:
		return g.valueOf(e)
	case *ast.ReturnExpr:
		v := g.rt() + ".U"
		if x.X != nil {
			v = g.expr(x.X)
		}
		if g.scenario {
			g.e.failf("return in a scenario")
		}
		g.returnSink(v)
		return g.rt() + ".U"
	case *ast.BreakExpr:
		g.printf("break\n")
		g.dead = true
		return g.rt() + ".U"
	case *ast.ContinueExpr:
		g.printf("continue\n")
		g.dead = true
		return g.rt() + ".U"
	case *ast.Closure:
		return g.closure(x)
	case *ast.RecordLit:
		return g.recordLit(x)
	case *ast.TupleLit:
		vals := g.exprs(x.Elems)
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = "F" + strconv.Itoa(i) + ": " + v
		}
		return g.f.goType(g.typeOf(e)) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.ToolsExpr:
		return g.toolsExpr(x)
	case *ast.ListLit:
		vals := g.exprs(x.Elems)
		return g.f.goType(g.typeOf(e)) + "{" + strings.Join(vals, ", ") + "}"
	case *ast.TaskExpr:
		t := g.typeOf(x.X)
		s := g.sub(t)
		v := s.expr(x.X)
		return g.rt() + ".Spawn(ctx, func(ctx *" + g.rt() + ".Ctx) " + g.f.goType(t) + " {\n" + s.w.String() + "return " + v + "\n})"
	case *ast.LlmExpr:
		out := g.prog.LlmOut[x]
		vals := g.exprs([]ast.Expr{x.Prompt, x.Input})
		return g.rt() + ".Llm[" + g.f.goType(out) + "](ctx, " + strconv.Quote(x.Role) + ", " + vals[0] + ", " + vals[1] + ")"
	case *ast.DecideExpr:
		out := g.prog.DecideOut[x]
		return g.rt() + ".Decide[" + g.f.goType(out) + "](ctx, " + strconv.Quote(x.Role) + ", " + g.expr(x.Input) + ")"
	case *ast.ApproveExpr:
		out := g.prog.ApproveOut[x]
		return g.rt() + ".Approve[" + g.f.goType(out) + "](ctx, " + strconv.Quote(approveSite(x)) + ", " + g.expr(x.Review) + ")"
	case *ast.OldExpr:
		if v, ok := g.olds[x]; ok {
			return v
		}
		return g.expr(x.X)
	case *ast.RetExpr:
		return "_ret"
	case *ast.QuantExpr:
		return g.quant(x)
	case *ast.AnyExpr:
		return g.anyExpr(x)
	case *ast.RangeExpr:
		lo, hi := g.expr(x.Lo), g.expr(x.Hi)
		return g.rt() + ".RangeList(ctx, " + lo + ", " + hi + ", " + strconv.FormatBool(x.Incl) + ")"
	}
	g.e.failf("cannot lower %T at %s", e, e.ExprPos())
	return ""
}

func approveSite(x *ast.ApproveExpr) string { return "approve@" + x.Pos.String() }

func (g *gen) numLit(s string, t checker.Type) string {
	gt := g.f.goType(t)
	if gt == "float64" && !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return gt + "(" + s + ")"
}

func (g *gen) strLit(x *ast.StrLit) string {
	var parts []string
	var exprs []ast.Expr
	for _, p := range x.Parts {
		if p.Expr != nil {
			exprs = append(exprs, p.Expr)
		}
	}
	vals := g.exprs(exprs)
	i := 0
	for _, p := range x.Parts {
		if p.Expr != nil {
			parts = append(parts, g.rt()+".Str("+vals[i]+")")
			i++
		} else if p.Lit != "" {
			parts = append(parts, strconv.Quote(p.Lit))
		}
	}
	if len(parts) == 0 {
		return `""`
	}
	return "(" + strings.Join(parts, " + ") + ")"
}

func (g *gen) ident(x *ast.Ident) string {
	r := g.prog.Refs[x]
	if r == nil {
		g.e.failf("unresolved identifier `%s` at %s", x.Name, x.Pos)
	}
	switch r.Kind {
	case checker.RefLocal:
		return g.localGo(r.Local, x.Name)
	case checker.RefFn:
		return g.fnValue(r.Fn, g.typeOf(x))
	case checker.RefConst:
		return g.f.constRef(r.Const) + "(ctx)"
	case checker.RefVariant:
		return g.variantCtor(r.Variant, g.typeOf(x)) + "()"
	case checker.RefTopic:
		return g.topicValue(r.Topic.Name)
	}
	g.e.failf("identifier `%s` used as a value at %s", x.Name, x.Pos)
	return ""
}

// fnValue is a function used as a value. Go infers generic instantiation
// from the assignment context.
func (g *gen) fnValue(fi *checker.FnInfo, t checker.Type) string {
	return g.f.fnRef(fi)
}

func (g *gen) topicValue(name string) string {
	t := g.prog.TopicType[name]
	return g.f.goType(t) + "{Name: " + strconv.Quote(name) + "}"
}

// variantCtor is the Go constructor of variant v of the enum type t.
func (g *gen) variantCtor(v *checker.VariantInfo, t checker.Type) string {
	tc, ok := checker.Zonk(t).(*checker.TCon)
	if !ok || tc.Info != v.Enum {
		// t is the constructor's function type; take its result.
		if ft, isFn := checker.Zonk(t).(*checker.TFn); isFn {
			tc, ok = checker.Zonk(ft.Ret).(*checker.TCon)
		}
	}
	if !ok || tc == nil {
		g.e.failf("variant `%s` without an enum type", v.Name)
	}
	return g.f.typeRef(tc.Info) + "_" + v.Name + g.f.typeArgs(tc.Args)
}

// tagConst is the Go tag constant of variant v.
func (g *gen) tagConst(v *checker.VariantInfo) string {
	return g.f.typeRef(v.Enum) + "Tag_" + v.Name
}

func (g *gen) field(x *ast.FieldExpr) string {
	r := g.prog.Fields[x]
	if r == nil {
		g.e.failf("unresolved field `%s` at %s", x.Name, x.Pos)
	}
	switch r.Kind {
	case checker.FieldRecord:
		return g.expr(x.X) + "." + fieldName(r.Field.Name)
	case checker.FieldTupleIndex:
		return g.expr(x.X) + ".F" + strconv.Itoa(r.Index)
	case checker.FieldNewtype0:
		return g.f.goType(g.typeOf(x)) + "(" + g.expr(x.X) + ")"
	case checker.FieldVariant:
		return g.variantCtor(r.Variant, g.typeOf(x)) + "()"
	case checker.FieldStaticFn:
		return g.fnValue(r.Fn, g.typeOf(x))
	case checker.FieldModuleItem:
		switch {
		case r.Fn != nil:
			return g.fnValue(r.Fn, g.typeOf(x))
		case r.Const != nil:
			return g.f.constRef(r.Const) + "(ctx)"
		}
	case checker.FieldTopic:
		return g.topicValue(r.Topic.Name)
	}
	g.e.failf("field `%s` used as a value at %s", x.Name, x.Pos)
	return ""
}

func (g *gen) isInt(t checker.Type) bool {
	tc, ok := checker.Zonk(t).(*checker.TCon)
	return ok && tc.Info.Kind == checker.Prim && tc.Info.Name == "Int"
}

// goComparable says whether `==` on t can be Go `==`.
func (g *gen) goComparable(t checker.Type) bool {
	tc, ok := checker.Zonk(t).(*checker.TCon)
	if !ok {
		return false
	}
	if tc.Info.Kind == checker.Prim {
		return tc.Info.Name != "Bytes"
	}
	if tc.Info.Kind == checker.Newtype {
		return g.goComparable(tc.Info.Inner)
	}
	return false
}

func (g *gen) arith(op, a, b string, t checker.Type) string {
	if g.isInt(t) {
		fn := map[string]string{"+": "Add", "-": "Sub", "*": "Mul", "/": "Div", "%": "Rem"}[op]
		if fn != "" {
			return g.rt() + "." + fn + "(ctx, " + a + ", " + b + ")"
		}
	}
	return "(" + a + " " + op + " " + b + ")"
}

func (g *gen) binary(x *ast.BinaryExpr) string {
	if x.Op == "&&" || x.Op == "||" {
		l := g.expr(x.L)
		stmts, r, dead := g.capture(func() string { return g.expr(x.R) })
		if stmts == "" && !dead {
			return "(" + l + " " + x.Op + " " + r + ")"
		}
		t := g.newTmp()
		cond := t
		if x.Op == "||" {
			cond = "!" + t
		}
		g.printf("%s := %s\nif %s {\n%s", t, l, cond, stmts)
		if !dead {
			g.printf("%s = %s\n", t, r)
		}
		g.printf("}\n")
		return t
	}
	vals := g.exprs([]ast.Expr{x.L, x.R})
	l, r := vals[0], vals[1]
	lt := g.typeOf(x.L)
	switch x.Op {
	case "+", "-", "*", "/", "%":
		return g.arith(x.Op, l, r, lt)
	case "==", "!=":
		if g.goComparable(lt) {
			return "(" + l + " " + x.Op + " " + r + ")"
		}
		eq := g.rt() + ".Equal(" + l + ", " + r + ")"
		if x.Op == "!=" {
			return "!" + eq
		}
		return eq
	case "<", ">", "<=", ">=":
		return "(" + l + " " + x.Op + " " + r + ")"
	}
	g.e.failf("binary %s", x.Op)
	return ""
}

// try lowers `x?`: on Err (or None) return early, lifting the error
// through the checker's chosen variant; in a scenario, fail it.
func (g *gen) try(x *ast.TryExpr) string {
	ti := g.prog.Tries[x]
	v := g.expr(x.X)
	t := g.newTmp()
	g.printf("%s := %s\n", t, v)
	if ti.Option {
		g.printf("if %s.Tag == %s.OptionTag_None {\n", t, g.rt())
		g.tryFail(ti, "", x.Pos)
		g.printf("}\n")
		return t + ".Some_0"
	}
	g.printf("if %s.Tag == %s.ResultTag_Err {\n", t, g.rt())
	g.tryFail(ti, t+".Err_0", x.Pos)
	g.printf("}\n")
	return t + ".Ok_0"
}

func (g *gen) tryFail(ti *checker.TryInfo, errV string, pos ast.Pos) {
	if ti.To == nil {
		g.printf("scn.Fail(%q, %q+%s.Show(%s))\n", pos.String(), "`?` failed: ", g.rt(), orUnit(errV, g.rt()))
		return
	}
	to, _ := checker.Zonk(ti.To).(*checker.TCon)
	if to == nil {
		g.e.failf("`?` without a return type")
	}
	if ti.Option {
		g.printf("return %s.Option_None[%s]()\n", g.rt(), g.f.goType(to.Args[0]))
		return
	}
	if ti.Lift != nil {
		errV = g.variantCtor(ti.Lift, to.Args[1]) + "(" + errV + ")"
	}
	g.printf("return %s.Result_Err[%s, %s](%s)\n", g.rt(), g.f.goType(to.Args[0]), g.f.goType(to.Args[1]), errV)
}

func orUnit(v, rt string) string {
	if v == "" {
		return rt + ".U"
	}
	return v
}

func (g *gen) closure(x *ast.Closure) string {
	ci := g.prog.Closures[x]
	if ci == nil {
		g.e.failf("unchecked closure at %s", x.Pos)
	}
	ft := checker.Zonk(ci.Type).(*checker.TFn)
	var b strings.Builder
	b.WriteString("func(ctx *" + g.rt() + ".Ctx")
	for i := range x.Params {
		b.WriteString(", " + paramGoName(g.prog, &x.Params[i]) + " " + g.f.goType(ft.Params[i]))
	}
	b.WriteString(") " + g.f.goType(ft.Ret) + " {\n")
	s := g.sub(ft.Ret)
	s.lowerInto(x.Body, s.returnSink)
	b.WriteString(s.w.String())
	b.WriteString("}")
	return b.String()
}

func (g *gen) recordLit(x *ast.RecordLit) string {
	ti := g.prog.RecordLits[x]
	t := g.typeOf(x)
	gt := g.f.goType(t)
	var exprs []ast.Expr
	if x.Base != nil {
		exprs = append(exprs, x.Base)
	}
	for _, fi := range x.Fields {
		exprs = append(exprs, fi.Value)
	}
	vals := g.exprs(exprs)
	var v string
	if x.Base != nil {
		tmp := g.newTmp()
		g.printf("%s := %s\n", tmp, vals[0])
		for i, fi := range x.Fields {
			g.printf("%s.%s = %s\n", tmp, fieldName(fi.Name), vals[i+1])
		}
		v = tmp
	} else {
		parts := make([]string, len(x.Fields))
		for i, fi := range x.Fields {
			parts[i] = fieldName(fi.Name) + ": " + vals[i]
		}
		v = gt + "{" + strings.Join(parts, ", ") + "}"
	}
	if ti != nil {
		if inv := g.f.invRef(ti); inv != "" {
			return inv + "(ctx, " + v + ")"
		}
	}
	return v
}

func (g *gen) quant(x *ast.QuantExpr) string {
	l := g.prog.QuantLocals[x]
	coll := g.expr(x.Coll)
	s := g.sub(nil)
	v := s.expr(x.Body)
	fn := "ListAll"
	if !x.Forall {
		fn = "ListSome"
	}
	return g.rt() + "." + fn + "(ctx, " + coll + ", func(ctx *" + g.rt() + ".Ctx, " + g.localGo(l, x.Var) + " " + g.f.goType(l.Type) + ") bool {\n" + s.w.String() + "return " + v + "\n})"
}

func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmtSprintf(format, args...)
}
