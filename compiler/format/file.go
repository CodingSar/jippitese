package format

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// File prints a parsed file in canonical form (`jip fmt`, spec 13 rule
// 5): two-space indentation, one item per paragraph, one statement per
// line, short bodies inline. src is the original text; its `//` comments,
// which the parser drops, are carried over: a comment alone on a line goes
// before the first printed line from at or after its line, a trailing
// comment stays at the end of the line printed for its source line.
func File(f *ast.File, src string) string {
	w := &writer{}
	if f.App != nil {
		w.appBlock(f.App, 0)
	} else {
		if f.Module != nil {
			w.line(1, "module "+strings.Join(f.Module, "."))
		}
		for _, u := range f.Uses {
			s := "use " + strings.Join(u.Path, ".")
			if u.Alias != "" {
				s += " as " + u.Alias
			}
			w.line(u.Pos.Line, s)
		}
		for _, it := range f.Items {
			w.blank()
			w.item(it)
		}
		for _, sc := range f.Scenarios {
			w.blank()
			w.scenario(sc)
		}
	}
	return attachComments(w.lines, scanComments(src))
}

// outLine is one printed line with the source line it came from (0 when
// it has none, such as a closing brace).
type outLine struct {
	text string
	src  int
}

type writer struct {
	lines  []outLine
	indent int
}

func (w *writer) pad() string { return strings.Repeat("  ", w.indent) }

func (w *writer) line(src int, s string) {
	w.lines = append(w.lines, outLine{text: w.pad() + s, src: src})
}

func (w *writer) blank() {
	if n := len(w.lines); n > 0 && w.lines[n-1].text != "" {
		w.lines = append(w.lines, outLine{})
	}
}

// appendText adds s to the last line (for multi-line expressions).
func (w *writer) appendText(s string) {
	w.lines[len(w.lines)-1].text += s
}

func (w *writer) doc(src int, d string) {
	if d == "" {
		return
	}
	for _, l := range strings.Split(d, "\n") {
		w.line(src, "/// "+l)
	}
}

func vis(v ast.Visibility) string {
	switch v {
	case ast.Private:
		return ""
	case ast.PubModule:
		return "pub(module) "
	}
	return "pub "
}

func typeParams(ps []ast.TypeParam) string {
	if len(ps) == 0 {
		return ""
	}
	var xs []string
	for _, p := range ps {
		if len(p.Bounds) > 0 {
			xs = append(xs, p.Name+": "+strings.Join(p.Bounds, " + "))
		} else {
			xs = append(xs, p.Name)
		}
	}
	return "<" + strings.Join(xs, ", ") + ">"
}

func fieldList(fs []ast.Field) string {
	if len(fs) == 0 {
		return "{}"
	}
	var xs []string
	for _, f := range fs {
		xs = append(xs, f.Name+": "+Type(f.Type))
	}
	return "{ " + strings.Join(xs, ", ") + " }"
}

// FnHeader prints `fn name<T>(params) -> R with {…}` (no visibility).
func FnHeader(d *ast.FnDecl) string {
	var ps []string
	for _, p := range d.Params {
		switch {
		case p.Self:
			ps = append(ps, "self")
		case p.Type != nil:
			ps = append(ps, p.Name+": "+Type(p.Type))
		default:
			ps = append(ps, p.Name)
		}
	}
	s := "fn " + d.Name + typeParams(d.TypeParams) + "(" + strings.Join(ps, ", ") + ")"
	if d.Ret != nil {
		s += " -> " + Type(d.Ret)
	}
	if d.HasWith {
		s += " with " + Effects(d.Effects)
	}
	return s
}

func (w *writer) item(it ast.Item) {
	switch d := it.(type) {
	case *ast.TypeDecl:
		w.doc(d.Pos.Line, d.Doc)
		head := vis(d.Vis)
		if d.Opaque {
			head += "opaque "
		}
		head += "type " + d.Name + typeParams(d.Params)
		switch d.Kind {
		case ast.RecordKind:
			head += " " + fieldList(d.Fields)
		case ast.NewtypeKind:
			head += "(" + Type(d.Inner) + ")"
		case ast.AliasKind:
			head += " = " + Type(d.Inner)
		case ast.ExternKind:
			head = vis(d.Vis) + "extern type " + d.Name + typeParams(d.Params) + " = go " + strconv.Quote(d.Extern.Pkg) + "." + d.Extern.Ident
		}
		w.line(d.Pos.Line, head)
		w.contracts("inv", d.Inv)
	case *ast.EnumDecl:
		w.doc(d.Pos.Line, d.Doc)
		var vs []string
		for _, v := range d.Variants {
			s := v.Name
			if len(v.Positional) > 0 {
				var ts []string
				for _, t := range v.Positional {
					ts = append(ts, Type(t))
				}
				s += "(" + strings.Join(ts, ", ") + ")"
			} else if len(v.Named) > 0 {
				s += " " + fieldList(v.Named)
			}
			vs = append(vs, s)
		}
		w.line(d.Pos.Line, vis(d.Vis)+"enum "+d.Name+typeParams(d.Params)+" { "+strings.Join(vs, ", ")+" }")
	case *ast.ConstDecl:
		w.doc(d.Pos.Line, d.Doc)
		w.constDecl(d, vis(d.Vis))
	case *ast.FnDecl:
		w.fn(d, vis(d.Vis))
	case *ast.BehaviourDecl:
		w.doc(d.Pos.Line, d.Doc)
		w.line(d.Pos.Line, vis(d.Vis)+"behaviour "+d.Name+typeParams(d.Params)+" {")
		w.indent++
		for _, a := range d.Assoc {
			w.line(0, "type "+a)
		}
		for _, c := range d.Consts {
			w.doc(c.Pos.Line, c.Doc)
			w.constDecl(c, "")
		}
		for _, m := range d.Methods {
			w.fn(m, "")
		}
		w.contracts("inv", d.Inv)
		w.indent--
		w.line(0, "}")
	case *ast.ImplDecl:
		w.doc(d.Pos.Line, d.Doc)
		head := "impl "
		if d.Behaviour != "" {
			head += d.Behaviour
			if len(d.BehaviourArgs) > 0 {
				var ts []string
				for _, t := range d.BehaviourArgs {
					ts = append(ts, Type(t))
				}
				head += "<" + strings.Join(ts, ", ") + ">"
			}
			head += " for "
		}
		w.line(d.Pos.Line, head+Type(d.Target)+" {")
		w.indent++
		for _, a := range d.Assoc {
			w.line(0, "type "+a.Name+" = "+Type(a.Type))
		}
		for _, c := range d.Consts {
			w.doc(c.Pos.Line, c.Doc)
			w.constDecl(c, vis(c.Vis))
		}
		for _, m := range d.Methods {
			w.fn(m, vis(m.Vis))
		}
		w.contracts("inv", d.Inv)
		w.indent--
		w.line(0, "}")
	}
}

func (w *writer) constDecl(c *ast.ConstDecl, v string) {
	s := v + "const " + c.Name
	if c.Type != nil {
		s += ": " + Type(c.Type)
	}
	if c.Value != nil {
		s += " = " + Expr(c.Value)
	}
	w.line(c.Pos.Line, s)
}

func (w *writer) contracts(kind string, es []ast.Expr) {
	w.indent++
	for _, e := range es {
		w.line(e.ExprPos().Line, padKw(kind)+Expr(e))
	}
	w.indent--
}

func padKw(k string) string { return k + strings.Repeat(" ", 5-len(k)) }

func (w *writer) fn(d *ast.FnDecl, v string) {
	w.doc(d.Pos.Line, d.Doc)
	head := v + FnHeader(d)
	if d.Unsafe {
		head = "unsafe pure " + head
	}
	if d.Extern != nil {
		head = strings.Replace(head, "fn ", "extern fn ", 1) + " = go " + strconv.Quote(d.Extern.Pkg) + "." + d.Extern.Ident
		if d.UnsafeWhy != "" {
			w.line(d.Pos.Line, "/// why: "+d.UnsafeWhy)
		}
		w.line(d.Pos.Line, head)
		return
	}
	if d.Body == nil {
		w.line(d.Pos.Line, head)
		return
	}
	if len(d.Pre) == 0 && len(d.Post) == 0 {
		if s, ok := inlineBlock(d.Body); ok && oneLine(d.Body) && len(w.pad())+len(head)+len(s) < 100 {
			w.line(d.Pos.Line, head+" "+s)
			return
		}
		w.line(d.Pos.Line, head+" {")
	} else {
		w.line(d.Pos.Line, head)
		w.contracts("pre", d.Pre)
		w.contracts("post", d.Post)
		w.line(0, "{")
	}
	w.indent++
	w.blockBody(d.Body)
	w.indent--
	w.line(0, "}")
}

// oneLine says whether the author wrote a block's tail on the line of its
// opening brace; only such blocks stay inline.
func oneLine(b *ast.Block) bool {
	return b.Tail == nil && len(b.Stmts) == 0 || b.Tail != nil && b.Tail.ExprPos().Line == b.Pos.Line
}

// inlineBlock prints a block on one line when it has no statements and a
// simple tail.
func inlineBlock(b *ast.Block) (string, bool) {
	if len(b.Stmts) == 0 && b.Tail == nil {
		return "{}", true
	}
	if len(b.Stmts) == 0 && simple(b.Tail) {
		return "{ " + Expr(b.Tail) + " }", true
	}
	return "", false
}

// authorInline says whether every block inside e was written on one line.
func authorInline(e ast.Expr) bool {
	ok := true
	ast.WalkExpr(e, func(x ast.Expr) {
		if b, isBlock := x.(*ast.Block); isBlock && !oneLine(b) {
			ok = false
		}
	})
	return ok
}

// simple says whether e prints well on one line: no statement blocks, no
// match, no loop, and short.
func simple(e ast.Expr) bool {
	ok := true
	ast.WalkExpr(e, func(x ast.Expr) {
		switch y := x.(type) {
		case *ast.Block:
			if len(y.Stmts) > 0 {
				ok = false
			}
		case *ast.MatchExpr, *ast.WhileExpr, *ast.ForExpr, *ast.SpawnExpr:
			ok = false
		}
	})
	return ok && len(Expr(e)) <= 80
}

func (w *writer) blockBody(b *ast.Block) {
	for _, s := range b.Stmts {
		w.stmt(s)
	}
	if b.Tail != nil {
		w.line(b.Tail.ExprPos().Line, "")
		w.expr(b.Tail)
	}
}

func (w *writer) stmt(s ast.Stmt) {
	src := s.StmtPos().Line
	switch x := s.(type) {
	case *ast.LetStmt:
		t := ""
		if x.Type != nil {
			t = ": " + Type(x.Type)
		}
		w.line(src, "let "+Pattern(x.Pat)+t+" = ")
		w.expr(x.Value)
	case *ast.VarStmt:
		t := ""
		if x.Type != nil {
			t = ": " + Type(x.Type)
		}
		w.line(src, "var "+x.Name+t+" = ")
		w.expr(x.Value)
	case *ast.AssignStmt:
		w.line(src, x.Target.Name+" "+x.Op+" ")
		w.expr(x.Value)
	case *ast.ExprStmt:
		w.line(src, "")
		w.expr(x.X)
	}
}

// expr appends e to the current line, breaking lines for blocks, matches
// and loops.
func (w *writer) expr(e ast.Expr) {
	if simple(e) && authorInline(e) {
		w.appendText(Expr(e))
		return
	}
	switch x := e.(type) {
	case *ast.Block:
		w.block(x)
	case *ast.IfExpr:
		w.appendText("if " + Expr(x.Cond) + " ")
		w.block(x.Then)
		if x.Else != nil {
			w.appendText(" else ")
			w.expr(x.Else)
		}
	case *ast.MatchExpr:
		w.appendText("match " + Expr(x.X) + " {")
		w.indent++
		for _, a := range x.Arms {
			p := Pattern(a.Pat)
			if a.Guard != nil {
				p += " if " + Expr(a.Guard)
			}
			w.line(a.Pos.Line, p+" => ")
			w.expr(a.Body)
			w.appendText(",")
		}
		w.indent--
		w.line(0, "}")
	case *ast.WhileExpr:
		w.appendText("while " + Expr(x.Cond) + " ")
		w.block(x.Body)
	case *ast.ForExpr:
		w.appendText("for " + Pattern(x.Pat) + " in " + Expr(x.Iter) + " ")
		w.block(x.Body)
	case *ast.SpawnExpr:
		w.appendText("spawn ")
		w.block(x.Body)
	case *ast.Closure:
		var ps []string
		for _, p := range x.Params {
			if p.Type != nil {
				ps = append(ps, p.Name+": "+Type(p.Type))
			} else {
				ps = append(ps, p.Name)
			}
		}
		s := "fn(" + strings.Join(ps, ", ") + ")"
		if x.Ret != nil {
			s += " -> " + Type(x.Ret)
		}
		w.appendText(s + " => ")
		w.expr(x.Body)
	case *ast.CallExpr:
		w.expr(x.Fn)
		if len(x.TypeArgs) > 0 {
			var ts []string
			for _, t := range x.TypeArgs {
				ts = append(ts, Type(t))
			}
			w.appendText("::<" + strings.Join(ts, ", ") + ">")
		}
		w.appendText("(")
		for i, a := range x.Args {
			if i > 0 {
				w.appendText(", ")
			}
			w.expr(a)
		}
		w.appendText(")")
	case *ast.FieldExpr:
		w.operand(x.X, precPost)
		w.appendText("." + x.Name)
	case *ast.TryExpr:
		w.operand(x.X, precPost)
		w.appendText("?")
	case *ast.ReturnExpr:
		w.appendText("return ")
		w.expr(x.X)
	case *ast.TaskExpr:
		w.appendText("task ")
		w.expr(x.X)
	case *ast.BinaryExpr:
		pr := prec(x)
		l := pr
		if pr == precCmp {
			l = pr + 1
		}
		w.operand(x.L, l)
		w.appendText(" " + x.Op + " ")
		w.operand(x.R, pr+1)
	case *ast.UnaryExpr:
		w.appendText(x.Op)
		w.operand(x.X, precUnary)
	case *ast.RecordLit:
		w.appendText(strings.Join(x.Path, ".") + " { ")
		first := true
		sep := func() {
			if !first {
				w.appendText(", ")
			}
			first = false
		}
		if x.Base != nil {
			sep()
			w.appendText("..")
			w.expr(x.Base)
		}
		for _, f := range x.Fields {
			sep()
			if id, ok := f.Value.(*ast.Ident); ok && id.Name == f.Name {
				w.appendText(f.Name)
				continue
			}
			w.appendText(f.Name + ": ")
			w.expr(f.Value)
		}
		w.appendText(" }")
	case *ast.TupleLit:
		w.appendText("(")
		for i, el := range x.Elems {
			if i > 0 {
				w.appendText(", ")
			}
			w.expr(el)
		}
		if len(x.Elems) == 1 {
			w.appendText(",")
		}
		w.appendText(")")
	case *ast.ListLit:
		w.appendText("[")
		for i, el := range x.Elems {
			if i > 0 {
				w.appendText(", ")
			}
			w.expr(el)
		}
		w.appendText("]")
	default:
		w.appendText(Expr(e))
	}
}

func (w *writer) operand(e ast.Expr, min int) {
	if prec(e) < min {
		w.appendText("(")
		w.expr(e)
		w.appendText(")")
		return
	}
	w.expr(e)
}

// block appends `{`, the statements on their own lines, and `}`; or the
// inline form when the block is simple.
func (w *writer) block(b *ast.Block) {
	if s, ok := inlineBlock(b); ok && oneLine(b) {
		w.appendText(s)
		return
	}
	w.appendText("{")
	w.indent++
	w.blockBody(b)
	w.indent--
	w.line(0, "}")
}

func (w *writer) scenario(s *ast.Scenario) {
	for _, t := range s.Tags {
		tag := "@" + t.Name
		if t.Arg != "" {
			tag += "(\"" + t.Arg + "\")"
		}
		w.line(s.Pos.Line, tag)
	}
	w.line(s.Pos.Line, "scenario \""+s.Name+"\" {")
	w.indent++
	for _, g := range s.Givens {
		switch g.Kind {
		case ast.GivenExpr:
			w.line(g.Pos.Line, "given "+g.Name+" = ")
			w.expr(g.Expr)
		case ast.GivenAny:
			w.line(g.Pos.Line, "given "+g.Name+" = "+Expr(g.Any))
		case ast.GivenTable:
			w.line(g.Pos.Line, "given "+Pattern(g.Pat)+" in ")
			w.expr(g.Rows)
		case ast.GivenRealTime:
			w.line(g.Pos.Line, "given real_time")
		}
	}
	for _, wh := range s.Whens {
		w.line(wh.Pos.Line, "when  "+wh.Name+" = ")
		w.expr(wh.Expr)
	}
	for _, t := range s.Thens {
		w.line(t.ExprPos().Line, "then  ")
		w.expr(t)
	}
	w.indent--
	w.line(0, "}")
}

// ---- app.jip ----------------------------------------------------------------

func (w *writer) appBlock(b *ast.AppBlock, src int) {
	if src == 0 {
		src = b.Pos.Line
	}
	if len(b.Entries) == 0 {
		w.line(src, b.Head)
		return
	}
	w.line(src, b.Head+" {")
	w.indent++
	sep := ": "
	if b.Head == "deps" || b.Head == "models" || strings.HasPrefix(b.Head, "env ") {
		sep = " = "
	}
	for _, en := range b.Entries {
		if blk, ok := en.Value.(*ast.AppBlock); ok && blk.Head != "" {
			w.appBlock(blk, en.Pos.Line)
			continue
		}
		def := ""
		if en.Default {
			def = "default "
		}
		w.line(en.Pos.Line, en.Key+sep+def+appValue(en.Value))
	}
	w.indent--
	w.line(0, "}")
}

func appValue(v ast.AppValue) string {
	switch x := v.(type) {
	case *ast.AppLit:
		return Expr(x.X)
	case *ast.AppPath:
		return strings.Join(x.Path, ".")
	case *ast.AppType:
		return Type(x.T)
	case *ast.AppList:
		var xs []string
		for _, el := range x.Elems {
			xs = append(xs, appValue(el))
		}
		return "[" + strings.Join(xs, ", ") + "]"
	case *ast.AppCall:
		var xs []string
		for _, a := range x.Args {
			xs = append(xs, appValue(a))
		}
		for _, kw := range x.Kw {
			xs = append(xs, kw.Key+": "+appValue(kw.Value))
		}
		return x.Name + "(" + strings.Join(xs, ", ") + ")"
	case *ast.AppBlock:
		var xs []string
		for _, en := range x.Entries {
			xs = append(xs, en.Key+": "+appValue(en.Value))
		}
		if len(xs) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(xs, ", ") + " }"
	}
	return ""
}

// ---- comments -----------------------------------------------------------------

type comment struct {
	line     int
	text     string // from `//` to end of line
	trailing bool   // code precedes it on its line
}

// scanComments finds `//` comments outside string and char literals. `///`
// doc lines belong to items and are skipped.
func scanComments(src string) []comment {
	var out []comment
	line, lineStart := 1, true
	inStr, inChar := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			line++
			lineStart = true
			inChar = false
			continue
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case inChar:
			if c == '\\' {
				i++
			} else if c == '\'' {
				inChar = false
			}
		case c == '"':
			inStr = true
		case c == '\'':
			inChar = true
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			text := strings.TrimRight(src[i:i+end], " \t\r")
			if !strings.HasPrefix(text, "///") {
				out = append(out, comment{line: line, text: text, trailing: !lineStart})
			} else if !lineStart {
				out = append(out, comment{line: line, text: "// " + strings.TrimSpace(text[3:]), trailing: true})
			}
			i += end - 1
			continue
		}
		if c != ' ' && c != '\t' {
			lineStart = false
		}
	}
	return out
}

// attachComments merges comments into the printed lines.
func attachComments(lines []outLine, cs []comment) string {
	// trailing comments: the last printed line for that source line
	lastFor := map[int]int{}
	for i, l := range lines {
		if l.src > 0 {
			lastFor[l.src] = i
		}
	}
	trail := map[int][]string{}
	var full []comment
	for _, c := range cs {
		if i, ok := lastFor[c.line]; ok && c.trailing {
			trail[i] = append(trail[i], c.text)
			continue
		}
		full = append(full, c)
	}
	// Align the trailing comments of consecutive lines, as gofmt does.
	col := map[int]int{}
	for i := 0; i < len(lines); {
		if len(trail[i]) == 0 {
			i++
			continue
		}
		j, w := i, 0
		for j < len(lines) && len(trail[j]) > 0 && lines[j].text != "" {
			if n := len(strings.TrimRight(lines[j].text, " ")); n > w {
				w = n
			}
			j++
		}
		for x := i; x < j; x++ {
			col[x] = w
		}
		i = j
	}
	var b strings.Builder
	k := 0
	for i, l := range lines {
		for k < len(full) && l.src > 0 && full[k].line <= l.src {
			indent := l.text[:len(l.text)-len(strings.TrimLeft(l.text, " "))]
			b.WriteString(indent + full[k].text + "\n")
			k++
		}
		text := strings.TrimRight(l.text, " ")
		if len(trail[i]) > 0 {
			text += strings.Repeat(" ", col[i]-len(text)+2) + strings.Join(trail[i], " ")
		}
		b.WriteString(text + "\n")
	}
	for ; k < len(full); k++ {
		b.WriteString(full[k].text + "\n")
	}
	return b.String()
}
