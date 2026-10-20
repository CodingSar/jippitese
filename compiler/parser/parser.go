// Package parser builds the AST per spec sections 01-12 and grammar.md.
package parser

import (
	"fmt"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/lexer"
)

// Error is a syntax error with a diagnostic code.
type Error struct {
	Code string
	Pos  ast.Pos
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: error[%s]: %s", e.Pos, e.Code, e.Msg) }

type parser struct {
	toks []lexer.Token
	i    int
	file string
}

type bailout struct{ err *Error }

// ParseFile parses one .jip source file (module file, scenarios file or app.jip).
func ParseFile(path, src string) (f *ast.File, err error) {
	toks, lerr := lexer.Lex(path, src)
	if lerr != nil {
		le := lerr.(*lexer.Error)
		return nil, &Error{"E0001", le.Pos, le.Msg}
	}
	p := &parser{toks: toks, file: path}
	defer func() {
		if r := recover(); r != nil {
			if b, ok := r.(bailout); ok {
				err = b.err
				return
			}
			panic(r)
		}
	}()
	return p.parseFile(), nil
}

// ParseExpr parses a standalone expression (used for interpolation).
func ParseExpr(pos ast.Pos, src string) (e ast.Expr, err error) {
	toks, lerr := lexer.Lex(pos.File, src)
	if lerr != nil {
		le := lerr.(*lexer.Error)
		return nil, &Error{"E0001", le.Pos, le.Msg}
	}
	for k := range toks {
		toks[k].Pos.Line += pos.Line - 1
		if toks[k].Pos.Line == pos.Line {
			toks[k].Pos.Col += pos.Col - 1
		}
	}
	p := &parser{toks: toks, file: pos.File}
	defer func() {
		if r := recover(); r != nil {
			if b, ok := r.(bailout); ok {
				err = b.err
				return
			}
			panic(r)
		}
	}()
	e = p.expr()
	p.skipTerms()
	if p.peek().Kind != lexer.EOF {
		p.fail("E0002", "unexpected %s in interpolation", p.peek())
	}
	return e, nil
}

func (p *parser) fail(code, format string, args ...interface{}) {
	panic(bailout{&Error{code, p.peek().Pos, fmt.Sprintf(format, args...)}})
}

func (p *parser) peek() lexer.Token { return p.toks[p.i] }
func (p *parser) peekN(n int) lexer.Token {
	if p.i+n < len(p.toks) {
		return p.toks[p.i+n]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) next() lexer.Token {
	t := p.toks[p.i]
	if t.Kind != lexer.EOF {
		p.i++
	}
	return t
}

func (p *parser) is(kind lexer.Kind, text string) bool {
	t := p.peek()
	return t.Kind == kind && (text == "" || t.Text == text)
}
func (p *parser) isOp(s string) bool { return p.is(lexer.Op, s) }
func (p *parser) isKw(s string) bool { return p.is(lexer.Keyword, s) }

func (p *parser) accept(kind lexer.Kind, text string) bool {
	if p.is(kind, text) {
		p.next()
		return true
	}
	return false
}
func (p *parser) acceptOp(s string) bool { return p.accept(lexer.Op, s) }
func (p *parser) acceptKw(s string) bool { return p.accept(lexer.Keyword, s) }

func (p *parser) expect(kind lexer.Kind, text string) lexer.Token {
	if !p.is(kind, text) {
		want := text
		if want == "" {
			want = map[lexer.Kind]string{lexer.Ident: "identifier", lexer.Str: "string literal", lexer.Int: "integer"}[kind]
		} else {
			want = "`" + want + "`"
		}
		p.fail("E0002", "expected %s, found %s", want, p.peek())
	}
	return p.next()
}
func (p *parser) expectOp(s string) lexer.Token { return p.expect(lexer.Op, s) }
func (p *parser) expectKw(s string) lexer.Token { return p.expect(lexer.Keyword, s) }
func (p *parser) ident() string                 { return p.expect(lexer.Ident, "").Text }

func (p *parser) skipTerms() {
	for p.peek().Kind == lexer.Term {
		p.next()
	}
}

// term requires a statement terminator unless the next token closes a block
// or, after a type ending in `>` (which emits no newline), starts an item.
func (p *parser) term() {
	if p.peek().Kind == lexer.Term {
		p.skipTerms()
		return
	}
	if p.isOp("}") || p.peek().Kind == lexer.EOF {
		return
	}
	if p.i > 0 && p.toks[p.i-1].Kind == lexer.Op && p.toks[p.i-1].Text == ">" && p.toks[p.i-1].Pos.Line < p.peek().Pos.Line {
		if t := p.peek(); t.Kind == lexer.Doc || t.Kind == lexer.Keyword && (t.Text == "const" || t.Text == "fn" || t.Text == "type" || t.Text == "pub") {
			return
		}
	}
	p.fail("E0002", "expected end of line, found %s", p.peek())
}

// ---------------------------------------------------------------- file

func (p *parser) parseFile() *ast.File {
	f := &ast.File{Path: p.file}
	p.skipTerms()
	if p.isKw("app") {
		f.App = p.appBlock()
		p.skipTerms()
		if p.peek().Kind != lexer.EOF {
			p.fail("E0002", "app.jip holds one `app { }` block only")
		}
		return f
	}
	if !p.isKw("module") {
		p.fail("E0101", "first line of every file is `module <path>`")
	}
	p.next()
	f.Module = p.dottedPath()
	p.term()
	for {
		p.skipTerms()
		if !p.isKw("use") {
			break
		}
		u := &ast.Use{Pos: p.next().Pos}
		u.Path = p.dottedPath()
		if p.accept(lexer.Ident, "as") { // contextual: `as` is not reserved (spec 01)
			u.Alias = p.ident()
		}
		f.Uses = append(f.Uses, u)
		p.term()
	}
	for {
		p.skipTerms()
		if p.peek().Kind == lexer.EOF {
			break
		}
		if p.is(lexer.Tag, "") || p.isKw("scenario") {
			f.Scenarios = append(f.Scenarios, p.scenario())
			continue
		}
		f.Items = append(f.Items, p.item())
	}
	return f
}

func (p *parser) dottedPath() []string {
	path := []string{p.ident()}
	// a keyword may follow a dot: `std.approve.http`
	for p.isOp(".") && (p.peekN(1).Kind == lexer.Ident || p.peekN(1).Kind == lexer.Keyword) {
		p.next()
		path = append(path, p.next().Text)
	}
	return path
}

func (p *parser) doc() string {
	var lines []string
	for p.is(lexer.Doc, "") {
		lines = append(lines, p.next().Text)
		p.skipTerms()
	}
	return strings.Join(lines, "\n")
}

func (p *parser) visibility() ast.Visibility {
	if p.acceptKw("pub") {
		if p.isOp("(") {
			p.next()
			p.expectKw("module")
			p.expectOp(")")
			return ast.PubModule
		}
		return ast.Pub
	}
	return ast.Private
}

func (p *parser) item() ast.Item {
	doc := p.doc()
	pos := p.peek().Pos
	vis := p.visibility()
	switch {
	case p.isKw("type") || (p.isKw("opaque") || (p.is(lexer.Ident, "opaque") && p.peekN(1).Kind == lexer.Keyword && p.peekN(1).Text == "type")):
		return p.typeDecl(pos, doc, vis)
	case p.isKw("enum"):
		return p.enumDecl(pos, doc, vis)
	case p.isKw("extern") && p.peekN(1).Kind == lexer.Keyword && p.peekN(1).Text == "type":
		return p.externTypeDecl(pos, doc, vis)
	case p.isKw("fn") || p.isKw("extern") || p.isKw("unsafe"):
		return p.fnDecl(pos, doc, vis, true)
	case p.isKw("impl"):
		return p.implDecl(pos, doc)
	case p.isKw("behaviour"):
		return p.behaviourDecl(pos, doc, vis)
	case p.isKw("const"):
		return p.constDecl(pos, doc, vis)
	}
	p.fail("E0002", "expected a declaration, found %s", p.peek())
	return nil
}

func (p *parser) typeParams() []ast.TypeParam {
	var ps []ast.TypeParam
	if !p.isOp("<") {
		return nil
	}
	p.next()
	for !p.isOp(">") {
		tp := ast.TypeParam{Name: p.ident()}
		if p.acceptOp(":") {
			tp.Bounds = append(tp.Bounds, p.ident())
			for p.acceptOp("+") {
				tp.Bounds = append(tp.Bounds, p.ident())
			}
		}
		ps = append(ps, tp)
		if !p.acceptOp(",") {
			break
		}
	}
	p.expectOp(">")
	return ps
}

func (p *parser) typeDecl(pos ast.Pos, doc string, vis ast.Visibility) ast.Item {
	d := &ast.TypeDecl{Pos: pos, Doc: doc, Vis: vis}
	if p.accept(lexer.Ident, "opaque") {
		d.Opaque = true
	}
	p.expectKw("type")
	d.Name = p.ident()
	d.Params = p.typeParams()
	switch {
	case p.isOp("{"):
		d.Kind = ast.RecordKind
		d.Fields = p.fieldList()
	case p.isOp("("):
		d.Kind = ast.NewtypeKind
		p.next()
		d.Inner = p.typeExpr()
		p.expectOp(")")
	case p.isOp("="):
		d.Kind = ast.AliasKind
		p.next()
		d.Inner = p.typeExpr()
	default:
		p.fail("E0002", "expected `{`, `(` or `=` after type name")
	}
	p.skipTerms()
	for p.is(lexer.Ident, "inv") {
		p.next()
		d.Inv = append(d.Inv, p.expr())
		p.skipTerms()
	}
	return d
}

func (p *parser) fieldList() []ast.Field {
	p.expectOp("{")
	var fs []ast.Field
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		f := ast.Field{Pos: p.peek().Pos, Name: p.ident()}
		p.expectOp(":")
		f.Type = p.typeExpr()
		fs = append(fs, f)
		p.skipTerms()
		if !p.acceptOp(",") {
			p.skipTerms()
			break
		}
	}
	p.skipTerms()
	p.expectOp("}")
	return fs
}

func (p *parser) enumDecl(pos ast.Pos, doc string, vis ast.Visibility) ast.Item {
	d := &ast.EnumDecl{Pos: pos, Doc: doc, Vis: vis}
	p.expectKw("enum")
	d.Name = p.ident()
	d.Params = p.typeParams()
	p.expectOp("{")
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		v := ast.Variant{Pos: p.peek().Pos, Name: p.ident()}
		if p.isOp("(") {
			p.next()
			for !p.isOp(")") {
				v.Positional = append(v.Positional, p.typeExpr())
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(")")
		} else if p.isOp("{") {
			v.Named = p.fieldList()
		}
		d.Variants = append(d.Variants, v)
		p.skipTerms()
		if !p.acceptOp(",") {
			break
		}
	}
	p.skipTerms()
	p.expectOp("}")
	p.skipTerms()
	return d
}

func (p *parser) constDecl(pos ast.Pos, doc string, vis ast.Visibility) *ast.ConstDecl {
	d := &ast.ConstDecl{Pos: pos, Doc: doc, Vis: vis}
	p.expectKw("const")
	d.Name = p.ident()
	if p.acceptOp(":") {
		d.Type = p.typeExpr()
	}
	if p.acceptOp("=") {
		d.Value = p.constExpr()
	}
	p.term()
	return d
}

// constExpr desugars the schedule literals of spec 06 (`every 5m`, `cron "…"`)
// into calls of the prelude `Schedule` constructors; anything else is an Expr.
func (p *parser) constExpr() ast.Expr {
	t := p.peek()
	if t.Kind == lexer.Ident && t.Text == "every" && p.peekN(1).Kind == lexer.Duration {
		p.next()
		d := p.next()
		fn := &ast.FieldExpr{Pos: t.Pos, X: &ast.Ident{Pos: t.Pos, Name: "Schedule"}, Name: "Every"}
		return &ast.CallExpr{Pos: t.Pos, Fn: fn, Args: []ast.Expr{&ast.DurationLit{Pos: d.Pos, Ns: d.Num, Raw: d.Text}}, Sugar: "every"}
	}
	if t.Kind == lexer.Ident && t.Text == "cron" && p.peekN(1).Kind == lexer.Str {
		p.next()
		s := p.next()
		lit, err := lexer.DecodeStr(s.Text, s.Pos)
		if err != nil {
			le := err.(*lexer.Error)
			panic(bailout{&Error{"E0001", le.Pos, le.Msg}})
		}
		fn := &ast.FieldExpr{Pos: t.Pos, X: &ast.Ident{Pos: t.Pos, Name: "Schedule"}, Name: "Cron"}
		return &ast.CallExpr{Pos: t.Pos, Fn: fn, Args: []ast.Expr{&ast.StrLit{Pos: s.Pos, Parts: []ast.StrPart{{Lit: lit}}}}, Sugar: "cron"}
	}
	return p.expr()
}

// externTypeDecl parses `extern type Name<Params> = go "pkg".Ident`, the
// stdlib-only form that binds a jippitese type to a runtime Go type.
func (p *parser) externTypeDecl(pos ast.Pos, doc string, vis ast.Visibility) ast.Item {
	d := &ast.TypeDecl{Pos: pos, Doc: doc, Vis: vis, Kind: ast.ExternKind}
	p.expectKw("extern")
	p.expectKw("type")
	d.Name = p.ident()
	d.Params = p.typeParams()
	p.expectOp("=")
	p.expectKw("go")
	pkg := p.expect(lexer.Str, "").Text
	p.expectOp(".")
	d.Extern = &ast.ExternRef{Pkg: pkg, Ident: p.ident()}
	p.term()
	return d
}

func (p *parser) effects() []ast.Effect {
	p.expectOp("{")
	var es []ast.Effect
	for !p.isOp("}") {
		t := p.next()
		if t.Kind != lexer.Ident && t.Kind != lexer.Keyword {
			p.fail("E0002", "expected effect name, found %s", t)
		}
		e := ast.Effect{Pos: t.Pos, Name: t.Text}
		if p.acceptOp("<") {
			e.Role = p.ident()
			p.expectOp(">")
		}
		es = append(es, e)
		if !p.acceptOp(",") {
			break
		}
	}
	p.expectOp("}")
	return es
}

// fnDecl parses `fn`, `extern fn` and `unsafe pure extern fn`; bodies are optional
// when allowBodyless (behaviour declarations and builtin impls).
func (p *parser) fnDecl(pos ast.Pos, doc string, vis ast.Visibility, top bool) *ast.FnDecl {
	d := &ast.FnDecl{Pos: pos, Doc: doc, Vis: vis}
	if p.acceptKw("unsafe") {
		d.Unsafe = true
		p.expectKw("pure")
		for _, l := range strings.Split(doc, "\n") {
			if strings.HasPrefix(l, "why:") {
				d.UnsafeWhy = strings.TrimSpace(strings.TrimPrefix(l, "why:"))
			}
		}
	}
	isExtern := p.acceptKw("extern")
	p.expectKw("fn")
	d.Name = p.ident()
	d.TypeParams = p.typeParams()
	p.expectOp("(")
	for !p.isOp(")") {
		p.skipTerms()
		prm := ast.Param{Pos: p.peek().Pos}
		if p.acceptKw("self") {
			prm.Name, prm.Self = "self", true
		} else {
			prm.Name = p.ident()
			p.expectOp(":")
			prm.Type = p.typeExpr()
		}
		d.Params = append(d.Params, prm)
		p.skipTerms()
		if !p.acceptOp(",") {
			break
		}
	}
	p.skipTerms()
	p.expectOp(")")
	if p.acceptOp("->") {
		d.Ret = p.typeExpr()
	}
	if p.acceptKw("with") {
		d.HasWith = true
		d.Effects = p.effects()
	}
	if isExtern {
		p.expectOp("=")
		p.expectKw("go")
		pkg := p.expect(lexer.Str, "").Text
		p.expectOp(".")
		ident := p.ident()
		// `go "pkg".Type.Method` binds a method (spec 11 rule 1); a longer
		// path parses so the extern check reports it instead of a syntax
		// error.
		for p.acceptOp(".") {
			ident += "." + p.ident()
		}
		d.Extern = &ast.ExternRef{Pkg: pkg, Ident: ident}
		p.term()
		return d
	}
	p.skipTerms()
	for p.is(lexer.Ident, "pre") || p.is(lexer.Ident, "post") {
		if p.next().Text == "pre" {
			d.Pre = append(d.Pre, p.expr())
		} else {
			d.Post = append(d.Post, p.expr())
		}
		p.skipTerms()
	}
	if p.isOp("{") {
		d.Body = p.block()
		p.skipTerms()
	} else if top {
		p.fail("E0002", "expected `{` to start the function body, found %s", p.peek())
	}
	return d
}

func (p *parser) implDecl(pos ast.Pos, doc string) ast.Item {
	d := &ast.ImplDecl{Pos: pos, Doc: doc}
	p.expectKw("impl")
	first := p.typeExpr()
	if p.acceptKw("for") {
		tp, ok := first.(*ast.TypePath)
		if !ok || len(tp.Path) != 1 {
			p.fail("E0002", "expected a behaviour name before `for`")
		}
		d.Behaviour = tp.Path[0]
		d.BehaviourArgs = tp.Args
		d.Target = p.typeExpr()
	} else {
		d.Target = first
	}
	p.expectOp("{")
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		switch {
		case p.isKw("type"):
			p.next()
			a := ast.AssocType{Name: p.ident()}
			p.expectOp("=")
			a.Type = p.typeExpr()
			d.Assoc = append(d.Assoc, a)
			p.term()
		case p.isKw("const"):
			d.Consts = append(d.Consts, p.constDecl(p.peek().Pos, "", ast.Private))
		case p.is(lexer.Ident, "inv"):
			p.next()
			d.Inv = append(d.Inv, p.expr())
			p.term()
		default:
			mdoc := p.doc()
			mpos := p.peek().Pos
			vis := p.visibility()
			d.Methods = append(d.Methods, p.fnDecl(mpos, mdoc, vis, false))
		}
	}
	p.expectOp("}")
	p.skipTerms()
	return d
}

func (p *parser) behaviourDecl(pos ast.Pos, doc string, vis ast.Visibility) ast.Item {
	d := &ast.BehaviourDecl{Pos: pos, Doc: doc, Vis: vis}
	p.expectKw("behaviour")
	d.Name = p.ident()
	d.Params = p.typeParams()
	p.expectOp("{")
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		switch {
		case p.isKw("type"):
			p.next()
			d.Assoc = append(d.Assoc, p.ident())
			p.term()
		case p.isKw("const"):
			d.Consts = append(d.Consts, p.constDecl(p.peek().Pos, "", ast.Private))
		case p.is(lexer.Ident, "inv"):
			p.next()
			d.Inv = append(d.Inv, p.expr())
			p.term()
		default:
			mdoc := p.doc()
			mpos := p.peek().Pos
			d.Methods = append(d.Methods, p.fnDecl(mpos, mdoc, ast.Pub, false))
			p.skipTerms()
		}
	}
	p.expectOp("}")
	p.skipTerms()
	return d
}

// ---------------------------------------------------------------- types

func (p *parser) typeExpr() ast.TypeExpr {
	pos := p.peek().Pos
	switch {
	case p.isOp("("):
		p.next()
		var elems []ast.TypeExpr
		for !p.isOp(")") {
			elems = append(elems, p.typeExpr())
			if !p.acceptOp(",") {
				break
			}
		}
		p.expectOp(")")
		if len(elems) == 1 {
			return elems[0]
		}
		return &ast.TupleType{Pos: pos, Elems: elems}
	case p.isKw("fn"):
		p.next()
		ft := &ast.FnType{Pos: pos}
		p.expectOp("(")
		for !p.isOp(")") {
			ft.Params = append(ft.Params, p.typeExpr())
			if !p.acceptOp(",") {
				break
			}
		}
		p.expectOp(")")
		if p.acceptOp("->") {
			ft.Ret = p.typeExpr()
		}
		if p.acceptKw("with") {
			ft.Effects = p.effects()
		}
		return ft
	case p.isKw("Self"):
		p.next()
		return &ast.TypePath{Pos: pos, Path: []string{"Self"}}
	case p.is(lexer.Ident, ""):
		tp := &ast.TypePath{Pos: pos, Path: p.dottedPath()}
		if p.isOp("<") {
			p.next()
			for !p.isOp(">") {
				tp.Args = append(tp.Args, p.typeExpr())
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(">")
		}
		return tp
	}
	p.fail("E0002", "expected a type, found %s", p.peek())
	return nil
}

// ---------------------------------------------------------------- statements

func (p *parser) block() *ast.Block {
	b := &ast.Block{Pos: p.expectOp("{").Pos}
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		pos := p.peek().Pos
		switch {
		case p.isKw("let"):
			p.next()
			s := &ast.LetStmt{Pos: pos, Pat: p.pattern()}
			if p.acceptOp(":") {
				s.Type = p.typeExpr()
			}
			p.expectOp("=")
			s.Value = p.expr()
			b.Stmts = append(b.Stmts, s)
			p.term()
			continue
		case p.isKw("var"):
			p.next()
			s := &ast.VarStmt{Pos: pos, Name: p.ident()}
			if p.acceptOp(":") {
				s.Type = p.typeExpr()
			}
			p.expectOp("=")
			s.Value = p.expr()
			b.Stmts = append(b.Stmts, s)
			p.term()
			continue
		case p.is(lexer.Ident, "") && p.peekN(1).Kind == lexer.Op && isAssignOp(p.peekN(1).Text):
			id := &ast.Ident{Pos: pos, Name: p.next().Text}
			op := p.next().Text
			s := &ast.AssignStmt{Pos: pos, Target: id, Op: op, Value: p.expr()}
			b.Stmts = append(b.Stmts, s)
			p.term()
			continue
		}
		e := p.expr()
		if p.peek().Kind == lexer.Term {
			p.skipTerms()
			if p.isOp("}") {
				b.Tail = e
				break
			}
			b.Stmts = append(b.Stmts, &ast.ExprStmt{Pos: pos, X: e})
			continue
		}
		if p.isOp("}") {
			b.Tail = e
			break
		}
		p.fail("E0002", "expected end of line, found %s", p.peek())
	}
	p.expectOp("}")
	return b
}

func isAssignOp(s string) bool {
	return s == "=" || s == "+=" || s == "-=" || s == "*=" || s == "/="
}

// ---------------------------------------------------------------- expressions

// expr parses with the full precedence table of section 04 rule 10.
func (p *parser) expr() ast.Expr { return p.exprNoRecord(false) }

// exprNoRecord parses an expression; when noRecord is set a bare record
// literal is rejected at the head of a control construct (E0451).
func (p *parser) exprNoRecord(noRecord bool) ast.Expr {
	return p.rangeExpr(noRecord)
}

func (p *parser) rangeExpr(nr bool) ast.Expr {
	l := p.orExpr(nr)
	if p.isOp("..") || p.isOp("..=") {
		pos := p.peek().Pos
		incl := p.next().Text == "..="
		r := p.orExpr(nr)
		return &ast.RangeExpr{Pos: pos, Lo: l, Hi: r, Incl: incl}
	}
	return l
}

func (p *parser) orExpr(nr bool) ast.Expr {
	l := p.andExpr(nr)
	for p.isOp("||") {
		pos := p.next().Pos
		l = &ast.BinaryExpr{Pos: pos, Op: "||", L: l, R: p.andExpr(nr)}
	}
	return l
}

func (p *parser) andExpr(nr bool) ast.Expr {
	l := p.cmpExpr(nr)
	for p.isOp("&&") {
		pos := p.next().Pos
		l = &ast.BinaryExpr{Pos: pos, Op: "&&", L: l, R: p.cmpExpr(nr)}
	}
	return l
}

func (p *parser) cmpExpr(nr bool) ast.Expr {
	l := p.addExpr(nr)
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.isOp(op) {
			pos := p.next().Pos
			r := p.addExpr(nr)
			for _, op2 := range []string{"==", "!=", "<=", ">=", "<", ">"} {
				if p.isOp(op2) {
					p.fail("E0002", "comparison operators do not chain; parenthesise")
				}
			}
			return &ast.BinaryExpr{Pos: pos, Op: op, L: l, R: r}
		}
	}
	return l
}

func (p *parser) addExpr(nr bool) ast.Expr {
	l := p.mulExpr(nr)
	for p.isOp("+") || p.isOp("-") {
		t := p.next()
		l = &ast.BinaryExpr{Pos: t.Pos, Op: t.Text, L: l, R: p.mulExpr(nr)}
	}
	return l
}

func (p *parser) mulExpr(nr bool) ast.Expr {
	l := p.unaryExpr(nr)
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		t := p.next()
		l = &ast.BinaryExpr{Pos: t.Pos, Op: t.Text, L: l, R: p.unaryExpr(nr)}
	}
	return l
}

func (p *parser) unaryExpr(nr bool) ast.Expr {
	if p.isOp("!") || p.isOp("-") {
		t := p.next()
		return &ast.UnaryExpr{Pos: t.Pos, Op: t.Text, X: p.unaryExpr(nr)}
	}
	return p.postfixExpr(nr)
}

func (p *parser) postfixExpr(nr bool) ast.Expr {
	e := p.primary(nr)
	for {
		switch {
		case p.isOp("."):
			pos := p.next().Pos
			t := p.next()
			switch t.Kind {
			case lexer.Ident, lexer.Keyword:
				e = &ast.FieldExpr{Pos: pos, X: e, Name: t.Text}
			case lexer.Int:
				e = &ast.FieldExpr{Pos: pos, X: e, Name: t.Text}
			case lexer.Float:
				// `x.0.1` lexes as x . 0.1 ; split it
				parts := strings.SplitN(t.Text, ".", 2)
				e = &ast.FieldExpr{Pos: pos, X: e, Name: parts[0]}
				e = &ast.FieldExpr{Pos: pos, X: e, Name: parts[1]}
			default:
				p.fail("E0002", "expected a field or method name after `.`")
			}
		case p.isOp("::<"):
			p.next()
			var targs []ast.TypeExpr
			for !p.isOp(">") {
				targs = append(targs, p.typeExpr())
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(">")
			args := p.callArgs()
			e = &ast.CallExpr{Pos: e.ExprPos(), Fn: e, TypeArgs: targs, Args: args}
		case p.isOp("("):
			args := p.callArgs()
			e = &ast.CallExpr{Pos: e.ExprPos(), Fn: e, Args: args}
		case p.isOp("?"):
			pos := p.next().Pos
			e = &ast.TryExpr{Pos: pos, X: e}
		case p.isOp("{") && !nr:
			path := exprPath(e)
			if path != nil && isTypeName(path[len(path)-1]) {
				e = p.recordLit(path, e.ExprPos())
				continue
			}
			return e
		case p.isOp("{") && nr:
			path := exprPath(e)
			if path != nil && isTypeName(path[len(path)-1]) && p.looksLikeRecordBody() {
				p.fail("E0451", "record literal in the head of a control construct must be parenthesised")
			}
			return e
		default:
			return e
		}
	}
}

// looksLikeRecordBody reports whether `{` starts `{ ..`, `{ name:` or `{ }`.
func (p *parser) looksLikeRecordBody() bool {
	t1, t2 := p.peekN(1), p.peekN(2)
	if t1.Kind == lexer.Op && (t1.Text == ".." || t1.Text == "}") {
		return true
	}
	return t1.Kind == lexer.Ident && t2.Kind == lexer.Op && t2.Text == ":"
}

func isTypeName(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

func exprPath(e ast.Expr) []string {
	switch x := e.(type) {
	case *ast.Ident:
		return []string{x.Name}
	case *ast.FieldExpr:
		base := exprPath(x.X)
		if base == nil {
			return nil
		}
		return append(base, x.Name)
	}
	return nil
}

func (p *parser) callArgs() []ast.Expr {
	p.expectOp("(")
	var args []ast.Expr
	for {
		p.skipTerms()
		if p.isOp(")") {
			break
		}
		args = append(args, p.expr())
		p.skipTerms()
		if !p.acceptOp(",") {
			break
		}
	}
	p.skipTerms()
	p.expectOp(")")
	return args
}

func (p *parser) recordLit(path []string, pos ast.Pos) ast.Expr {
	r := &ast.RecordLit{Pos: pos, Path: path}
	p.expectOp("{")
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		if p.acceptOp("..") {
			r.Base = p.expr()
		} else {
			name := p.ident()
			fi := ast.FieldInit{Name: name}
			if p.acceptOp(":") {
				fi.Value = p.expr()
			} else {
				fi.Value = &ast.Ident{Pos: pos, Name: name}
			}
			r.Fields = append(r.Fields, fi)
		}
		p.skipTerms()
		if !p.acceptOp(",") {
			break
		}
	}
	p.skipTerms()
	p.expectOp("}")
	return r
}

func (p *parser) primary(nr bool) ast.Expr {
	t := p.peek()
	pos := t.Pos
	switch t.Kind {
	case lexer.Int:
		p.next()
		return &ast.IntLit{Pos: pos, V: t.Num, Raw: t.Text}
	case lexer.Float:
		p.next()
		return &ast.FloatLit{Pos: pos, V: t.F, Raw: t.Text}
	case lexer.Char:
		p.next()
		return &ast.CharLit{Pos: pos, V: rune(t.Num)}
	case lexer.Duration:
		p.next()
		return &ast.DurationLit{Pos: pos, Ns: t.Num, Raw: t.Text}
	case lexer.RawStr:
		p.next()
		return &ast.StrLit{Pos: pos, Parts: []ast.StrPart{{Lit: t.Text}}}
	case lexer.Bytes:
		p.next()
		decoded, err := lexer.DecodeStr(t.Text, pos)
		if err != nil {
			le := err.(*lexer.Error)
			panic(bailout{&Error{"E0001", le.Pos, le.Msg}})
		}
		return &ast.BytesLit{Pos: pos, V: []byte(decoded)}
	case lexer.Str:
		p.next()
		parts, err := lexer.SplitStr(t.Text, pos)
		if err != nil {
			le := err.(*lexer.Error)
			panic(bailout{&Error{"E0001", le.Pos, le.Msg}})
		}
		s := &ast.StrLit{Pos: pos}
		for _, pt := range parts {
			if pt.IsExpr {
				e, err := ParseExpr(pt.Pos, pt.Expr)
				if err != nil {
					panic(bailout{err.(*Error)})
				}
				s.Parts = append(s.Parts, ast.StrPart{Expr: e})
			} else {
				s.Parts = append(s.Parts, ast.StrPart{Lit: pt.Lit})
			}
		}
		return s
	case lexer.Ident:
		p.next()
		if t.Text == "tools" && p.isOp("[") {
			return p.toolsExpr(pos)
		}
		return &ast.Ident{Pos: pos, Name: t.Text}
	case lexer.Op:
		switch t.Text {
		case "(":
			p.next()
			var elems []ast.Expr
			for {
				p.skipTerms()
				if p.isOp(")") {
					break
				}
				elems = append(elems, p.expr())
				p.skipTerms()
				if !p.acceptOp(",") {
					break
				}
			}
			p.skipTerms()
			p.expectOp(")")
			if len(elems) == 1 {
				return elems[0]
			}
			return &ast.TupleLit{Pos: pos, Elems: elems}
		case "[":
			p.next()
			l := &ast.ListLit{Pos: pos}
			for {
				p.skipTerms()
				if p.isOp("]") {
					break
				}
				l.Elems = append(l.Elems, p.expr())
				p.skipTerms()
				if !p.acceptOp(",") {
					break
				}
			}
			p.skipTerms()
			p.expectOp("]")
			return l
		case "{":
			return p.block()
		}
	case lexer.Keyword:
		switch t.Text {
		case "true", "false":
			p.next()
			return &ast.BoolLit{Pos: pos, V: t.Text == "true"}
		case "self":
			p.next()
			return &ast.SelfExpr{Pos: pos}
		case "ret":
			p.next()
			return &ast.RetExpr{Pos: pos}
		case "old":
			p.next()
			p.expectOp("(")
			e := p.expr()
			p.expectOp(")")
			return &ast.OldExpr{Pos: pos, X: e}
		case "if":
			return p.ifExpr()
		case "match":
			p.next()
			m := &ast.MatchExpr{Pos: pos, X: p.exprNoRecord(true)}
			p.expectOp("{")
			for {
				p.skipTerms()
				if p.isOp("}") {
					break
				}
				arm := ast.Arm{Pos: p.peek().Pos, Pat: p.pattern()}
				if p.acceptKw("if") {
					arm.Guard = p.expr()
				}
				p.expectOp("=>")
				arm.Body = p.expr()
				m.Arms = append(m.Arms, arm)
				p.skipTerms()
				if !p.acceptOp(",") {
					break
				}
			}
			p.skipTerms()
			m.End = p.expectOp("}").Pos
			return m
		case "while":
			p.next()
			w := &ast.WhileExpr{Pos: pos, Cond: p.exprNoRecord(true)}
			w.Body = p.block()
			return w
		case "for":
			p.next()
			f := &ast.ForExpr{Pos: pos, Pat: p.pattern()}
			p.expectKw("in")
			f.Iter = p.exprNoRecord(true)
			f.Body = p.block()
			return f
		case "return":
			p.next()
			r := &ast.ReturnExpr{Pos: pos}
			if !(p.peek().Kind == lexer.Term || p.isOp("}") || p.isOp(")") || p.isOp(",")) {
				r.X = p.expr()
			}
			return r
		case "break":
			p.next()
			return &ast.BreakExpr{Pos: pos}
		case "continue":
			p.next()
			return &ast.ContinueExpr{Pos: pos}
		case "fn":
			p.next()
			c := &ast.Closure{Pos: pos}
			p.expectOp("(")
			for !p.isOp(")") {
				prm := ast.Param{Pos: p.peek().Pos, Name: p.ident()}
				if p.acceptOp(":") {
					prm.Type = p.typeExpr()
				}
				c.Params = append(c.Params, prm)
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(")")
			if p.acceptOp("->") {
				c.Ret = p.typeExpr()
			}
			p.expectOp("=>")
			c.Body = p.expr()
			return c
		case "spawn":
			p.next()
			return &ast.SpawnExpr{Pos: pos, Body: p.block()}
		case "task":
			p.next()
			return &ast.TaskExpr{Pos: pos, X: p.expr()}
		case "llm", "decide":
			p.next()
			p.expectOp("<")
			role := p.ident()
			p.expectOp(">")
			args := p.callArgs()
			if t.Text == "llm" {
				if len(args) != 2 {
					p.fail("E0002", "llm<role> takes (prompt, input)")
				}
				return &ast.LlmExpr{Pos: pos, Role: role, Prompt: args[0], Input: args[1]}
			}
			if len(args) != 1 {
				p.fail("E0002", "decide<role> takes (input)")
			}
			return &ast.DecideExpr{Pos: pos, Role: role, Input: args[0]}
		case "approve":
			p.next()
			p.expectOp("::<")
			ty := p.typeExpr()
			p.expectOp(">")
			args := p.callArgs()
			if len(args) != 1 {
				p.fail("E0002", "approve::<T> takes (review)")
			}
			return &ast.ApproveExpr{Pos: pos, Type: ty, Review: args[0]}
		case "forall", "exists":
			p.next()
			q := &ast.QuantExpr{Pos: pos, Forall: t.Text == "forall", Var: p.ident()}
			p.expectKw("in")
			q.Coll = p.postfixExpr(true)
			p.expectOp(":")
			q.Body = p.expr()
			return q
		case "any":
			p.next()
			a := &ast.AnyExpr{Pos: pos, Type: p.typeExpr()}
			if p.acceptKw("via") {
				a.Via = p.expr()
			}
			if p.acceptKw("where") {
				a.Where = p.expr()
			}
			return a
		}
	}
	p.fail("E0002", "expected an expression, found %s", p.peek())
	return nil
}

func (p *parser) ifExpr() ast.Expr {
	pos := p.expectKw("if").Pos
	e := &ast.IfExpr{Pos: pos, Cond: p.exprNoRecord(true)}
	e.Then = p.block()
	if p.acceptKw("else") {
		if p.isKw("if") {
			e.Else = p.ifExpr()
		} else {
			e.Else = p.block()
		}
	}
	return e
}

// ---------------------------------------------------------------- patterns

func (p *parser) pattern() ast.Pattern {
	first := p.patternAtom()
	if p.isOp("|") {
		or := &ast.OrPat{Pos: first.PatPos(), Alts: []ast.Pattern{first}}
		for p.acceptOp("|") {
			or.Alts = append(or.Alts, p.patternAtom())
		}
		return or
	}
	return first
}

func (p *parser) patternAtom() ast.Pattern {
	t := p.peek()
	pos := t.Pos
	switch t.Kind {
	case lexer.Int, lexer.Float, lexer.Str, lexer.Char, lexer.Bytes, lexer.Duration:
		return &ast.LitPat{Pos: pos, Lit: p.primary(true)}
	case lexer.Keyword:
		if t.Text == "true" || t.Text == "false" {
			p.next()
			return &ast.LitPat{Pos: pos, Lit: &ast.BoolLit{Pos: pos, V: t.Text == "true"}}
		}
	case lexer.Op:
		switch t.Text {
		case "-":
			return &ast.LitPat{Pos: pos, Lit: p.unaryExpr(true)}
		case "(":
			p.next()
			tp := &ast.TuplePat{Pos: pos}
			for !p.isOp(")") {
				tp.Elems = append(tp.Elems, p.pattern())
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(")")
			if len(tp.Elems) == 1 {
				return tp.Elems[0]
			}
			return tp
		}
	case lexer.Ident:
		if t.Text == "_" {
			p.next()
			return &ast.WildPat{Pos: pos}
		}
		if !isTypeName(t.Text) {
			p.next()
			if p.acceptOp("@") {
				return &ast.AtPat{Pos: pos, Name: t.Text, Pat: p.patternAtom()}
			}
			return &ast.BindPat{Pos: pos, Name: t.Text}
		}
		path := p.dottedPath()
		vp := &ast.VariantPat{Pos: pos, Path: path}
		if p.isOp("(") {
			vp.Parens = true
			p.next()
			for !p.isOp(")") {
				vp.Positional = append(vp.Positional, p.pattern())
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(")")
		} else if p.isOp("{") {
			vp.Braces = true
			p.next()
			for {
				p.skipTerms()
				if p.isOp("}") {
					break
				}
				if p.acceptOp("..") {
					vp.Named = append(vp.Named, ast.FieldPat{Name: ".."})
					p.skipTerms()
					break
				}
				fp := ast.FieldPat{Name: p.ident()}
				if p.acceptOp(":") {
					fp.Pat = p.pattern()
				} else {
					fp.Pat = &ast.BindPat{Pos: pos, Name: fp.Name}
				}
				vp.Named = append(vp.Named, fp)
				p.skipTerms()
				if !p.acceptOp(",") {
					break
				}
			}
			p.skipTerms()
			p.expectOp("}")
		}
		return vp
	}
	p.fail("E0002", "expected a pattern, found %s", p.peek())
	return nil
}

// ---------------------------------------------------------------- scenarios

func (p *parser) scenario() *ast.Scenario {
	s := &ast.Scenario{Pos: p.peek().Pos}
	for p.is(lexer.Tag, "") {
		tag := ast.Tag{Name: p.next().Text}
		if p.acceptOp("(") {
			tag.Arg = p.expect(lexer.Str, "").Text
			p.expectOp(")")
		}
		s.Tags = append(s.Tags, tag)
		p.skipTerms()
	}
	p.expectKw("scenario")
	s.Name = p.expect(lexer.Str, "").Text
	p.expectOp("{")
	stage := 0
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		pos := p.peek().Pos
		switch {
		case p.isKw("given"):
			if stage > 0 {
				p.fail("E0802", "`given` must come before `when` and `then`")
			}
			p.next()
			g := ast.Given{Pos: pos}
			if p.is(lexer.Ident, "real_time") && p.peekN(1).Kind == lexer.Term {
				p.next()
				g.Kind = ast.GivenRealTime
			} else if p.isOp("(") {
				g.Kind = ast.GivenTable
				g.Pat = p.pattern()
				p.expectKw("in")
				g.Rows = p.expr()
			} else {
				g.Name = p.ident()
				p.expectOp("=")
				if p.isKw("any") {
					g.Kind = ast.GivenAny
					g.Any = p.primary(false).(*ast.AnyExpr)
					g.Any.Name = g.Name
				} else {
					g.Kind = ast.GivenExpr
					g.Expr = p.expr()
				}
			}
			s.Givens = append(s.Givens, g)
		case p.isKw("when"):
			if stage > 1 {
				p.fail("E0802", "`when` must come before `then`")
			}
			stage = 1
			p.next()
			w := ast.When{Pos: pos, Name: p.ident()}
			p.expectOp("=")
			w.Expr = p.expr()
			s.Whens = append(s.Whens, w)
		case p.isKw("then"):
			stage = 2
			p.next()
			s.Thens = append(s.Thens, p.expr())
		default:
			p.fail("E0002", "expected `given`, `when` or `then`, found %s", p.peek())
		}
		p.term()
	}
	p.expectOp("}")
	if len(s.Thens) == 0 {
		p.fail("E0802", "scenario %q has no `then`", s.Name)
	}
	p.skipTerms()
	return s
}

// ---------------------------------------------------------------- app.jip

func (p *parser) appBlock() *ast.AppBlock {
	b := &ast.AppBlock{Pos: p.peek().Pos}
	var head []string
	for !p.isOp("{") {
		t := p.next()
		if t.Kind != lexer.Ident && t.Kind != lexer.Keyword {
			p.fail("E0002", "expected a block name, found %s", t)
		}
		head = append(head, t.Text)
	}
	b.Head = strings.Join(head, " ")
	p.expectOp("{")
	for {
		p.skipTerms()
		if p.isOp("}") {
			break
		}
		b.Entries = append(b.Entries, p.appEntry())
		p.skipTerms()
		p.acceptOp(",")
	}
	p.expectOp("}")
	return b
}

func (p *parser) appEntry() *ast.AppEntry {
	e := &ast.AppEntry{Pos: p.peek().Pos}
	// `run X { }`, `env prod { }`, `net { }`: a head of identifiers followed by `{`
	j := 0
	for p.peekN(j).Kind == lexer.Ident || p.peekN(j).Kind == lexer.Keyword {
		j++
	}
	if j >= 1 && p.peekN(j).Kind == lexer.Op && p.peekN(j).Text == "{" {
		blk := p.appBlock()
		e.Key = blk.Head
		e.Value = blk
		return e
	}
	if j >= 1 && p.peekN(j).Kind == lexer.Term && j == 2 && p.peekN(0).Text == "run" {
		// `run Billing` with defaults
		p.next()
		e.Key = "run " + p.next().Text
		e.Value = &ast.AppBlock{Pos: e.Pos, Head: e.Key}
		return e
	}
	e.Key = strings.Join(p.dottedPath(), ".")
	if !p.acceptOp(":") {
		p.expectOp("=")
	}
	if p.acceptKw("default") || p.accept(lexer.Ident, "default") {
		e.Default = true
	}
	e.Value = p.appValue()
	return e
}

func (p *parser) appValue() ast.AppValue {
	t := p.peek()
	pos := t.Pos
	switch {
	case t.Kind == lexer.Op && t.Text == "{":
		b := &ast.AppBlock{Pos: pos}
		p.next()
		for {
			p.skipTerms()
			if p.isOp("}") {
				break
			}
			b.Entries = append(b.Entries, p.appEntry())
			p.skipTerms()
			p.acceptOp(",")
		}
		p.expectOp("}")
		return b
	case t.Kind == lexer.Op && t.Text == "[":
		p.next()
		l := &ast.AppList{Pos: pos}
		for {
			p.skipTerms()
			if p.isOp("]") {
				break
			}
			l.Elems = append(l.Elems, p.appValue())
			p.skipTerms()
			if !p.acceptOp(",") {
				break
			}
		}
		p.skipTerms()
		p.expectOp("]")
		return l
	case t.Kind == lexer.Ident && !(isTypeName(t.Text) && p.peekN(1).Kind == lexer.Op && p.peekN(1).Text == "("):
		// `Money(500)` falls through to a literal; `git(…)` is a call
		if isTypeName(t.Text) || (p.peekN(1).Kind == lexer.Op && p.peekN(1).Text == "<") {
			return &ast.AppType{Pos: pos, T: p.typeExpr()}
		}
		path := p.dottedPath()
		if p.isOp("(") {
			c := &ast.AppCall{Pos: pos, Name: strings.Join(path, ".")}
			p.next()
			for {
				p.skipTerms()
				if p.isOp(")") {
					break
				}
				if p.peek().Kind == lexer.Ident && p.peekN(1).Kind == lexer.Op && p.peekN(1).Text == ":" {
					kw := &ast.AppEntry{Pos: p.peek().Pos, Key: p.next().Text}
					p.next()
					kw.Value = p.appValue()
					c.Kw = append(c.Kw, kw)
				} else {
					c.Args = append(c.Args, p.appValue())
				}
				p.skipTerms()
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(")")
			return c
		}
		return &ast.AppPath{Pos: pos, Path: path}
	case t.Kind == lexer.Keyword && (t.Text == "true" || t.Text == "false"):
		p.next()
		return &ast.AppLit{Pos: pos, X: &ast.BoolLit{Pos: pos, V: t.Text == "true"}}
	case t.Kind == lexer.Keyword:
		// bare words such as `pre`, `post`, `all`, `none` are config paths, not keywords
		p.next()
		return &ast.AppPath{Pos: pos, Path: []string{t.Text}}
	}
	l := &ast.AppLit{Pos: pos, X: p.primary(true)}
	if _, isInt := l.X.(*ast.IntLit); isInt && p.peek().Kind == lexer.Ident && p.peek().Text == "tokens" {
		// `budget: 2_000_000 tokens` (spec 10 rule 3)
		l.Unit = p.next().Text
	}
	return l
}

// toolsExpr parses the list of `tools [f, g]` after `tools`.
func (p *parser) toolsExpr(pos ast.Pos) *ast.ToolsExpr {
	p.expectOp("[")
	e := &ast.ToolsExpr{Pos: pos}
	for {
		p.skipTerms()
		if p.isOp("]") {
			break
		}
		t := p.expect(lexer.Ident, "")
		e.Fns = append(e.Fns, &ast.Ident{Pos: t.Pos, Name: t.Text})
		p.skipTerms()
		if !p.acceptOp(",") {
			break
		}
	}
	p.skipTerms()
	p.expectOp("]")
	return e
}
