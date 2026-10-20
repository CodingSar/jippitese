package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/format"
	"github.com/codingsar/jippitese/compiler/modules"
)

// target is what a name in the source refers to; one field is set, or only
// expr when the name is a tuple index or something without a declaration.
type target struct {
	local   *checker.Local
	fn      *checker.FnInfo
	cnst    *checker.ConstInfo
	typ     *checker.TypeInfo
	variant *checker.VariantInfo
	field   *checker.FieldInfo
	module  *checker.Module
	expr    ast.Expr
}

// hit is a resolved name under the cursor: its span in the source and what
// it refers to.
type hit struct {
	root  string
	lines []string // the source holding the name
	from  ast.Pos
	width int
	to    target
}

// resolve finds the name at an LSP position of uri in the last check of its
// app. Names in expressions and local bindings resolve; names in type
// signatures do not.
func (s *Server) resolve(uri string, pos Position) (*hit, bool) {
	path := uriPath(uri)
	if path == "" {
		return nil, false
	}
	root, err := modules.FindRoot(filepath.Dir(path))
	if err != nil {
		return nil, false
	}
	prog := s.progs[root]
	if prog == nil {
		return nil, false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, false
	}
	rel = filepath.ToSlash(rel)
	text, ok := s.text(uri)
	if !ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		text = string(b)
	}
	lines := strings.Split(text, "\n")
	ln := pos.Line + 1
	col := s.column(line(lines, ln), pos.Character)
	at := func(p ast.Pos, w int) bool {
		return p.File == rel && p.Line == ln && col >= p.Col && col <= p.Col+w
	}
	h := &hit{root: root, lines: lines}
	set := func(p ast.Pos, w int, t target) bool {
		if !at(p, w) {
			return false
		}
		h.from, h.width, h.to = p, w, t
		return true
	}
	for id, r := range prog.Refs {
		if set(id.Pos, len(id.Name), refTarget(r, id)) {
			return h, true
		}
	}
	for fe, fr := range prog.Fields {
		if set(namePos(fe), len(fe.Name), fieldTarget(fr, fe)) {
			return h, true
		}
	}
	for call, ci := range prog.Calls {
		if fe, ok := call.Fn.(*ast.FieldExpr); ok {
			t := target{fn: ci.Fn, variant: ci.Variant, typ: ci.Newtype, expr: fe}
			if set(namePos(fe), len(fe.Name), t) {
				return h, true
			}
		}
	}
	// binding sites: the name is the first occurrence of it at or after the
	// binding's position (a `let` statement starts at the keyword)
	var locals []*checker.Local
	for _, l := range prog.Params {
		locals = append(locals, l)
	}
	for _, l := range prog.VarDefs {
		locals = append(locals, l)
	}
	for _, l := range prog.PatBinds {
		locals = append(locals, l)
	}
	for _, l := range prog.ForLocals {
		locals = append(locals, l)
	}
	for _, l := range locals {
		if l.Pos.File != rel || l.Pos.Line != ln {
			continue
		}
		if c := wordCol(line(lines, ln), l.Pos.Col, l.Name); c > 0 && set(ast.Pos{File: rel, Line: ln, Col: c}, len(l.Name), target{local: l}) {
			return h, true
		}
	}
	return nil, false
}

// namePos is where the name of a field access starts: just after the dot.
func namePos(fe *ast.FieldExpr) ast.Pos {
	p := fe.Pos
	p.Col++
	return p
}

func refTarget(r *checker.Ref, id *ast.Ident) target {
	switch r.Kind {
	case checker.RefLocal:
		return target{local: r.Local}
	case checker.RefFn:
		return target{fn: r.Fn}
	case checker.RefConst:
		return target{cnst: r.Const}
	case checker.RefType:
		return target{typ: r.Type}
	case checker.RefVariant:
		return target{variant: r.Variant}
	case checker.RefModule:
		return target{module: r.Module}
	}
	return target{expr: id}
}

func fieldTarget(fr *checker.FieldRef, fe *ast.FieldExpr) target {
	switch fr.Kind {
	case checker.FieldRecord:
		return target{field: fr.Field}
	case checker.FieldVariant:
		return target{variant: fr.Variant}
	case checker.FieldStaticFn:
		return target{fn: fr.Fn}
	case checker.FieldModuleItem:
		return target{fn: fr.Fn, cnst: fr.Const, typ: fr.Type, variant: fr.Variant, expr: fe}
	}
	return target{expr: fe}
}

// wordCol is the 1-based column of the first whole-word word in l at or
// after col, or 0.
func wordCol(l string, col int, word string) int {
	for off := col - 1; off >= 0 && off <= len(l); {
		k := strings.Index(l[off:], word)
		if k < 0 {
			return 0
		}
		a, b := off+k, off+k+len(word)
		if (a == 0 || !wordByte(l[a-1])) && (b == len(l) || !wordByte(l[b])) {
			return a + 1
		}
		off = b
	}
	return 0
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    Range         `json:"range"`
}

// hover shows the declaration of the name under the cursor, its type and
// effects, and its doc comment.
func (s *Server) hover(uri string, pos Position) *hoverResult {
	h, ok := s.resolve(uri, pos)
	if !ok {
		return nil
	}
	code, doc := describe(s.progs[h.root], h.to)
	if code == "" {
		return nil
	}
	v := "```jip\n" + code + "\n```"
	if doc != "" {
		v += "\n\n" + doc
	}
	rng := Range{s.at(h.lines, h.from.Line, h.from.Col), s.at(h.lines, h.from.Line, h.from.Col+h.width)}
	return &hoverResult{markupContent{"markdown", v}, rng}
}

// describe is the code line and doc comment shown for t.
func describe(prog *checker.Program, t target) (code, doc string) {
	switch {
	case t.local != nil:
		kw := "let"
		if t.local.Var {
			kw = "var"
		}
		return kw + " " + t.local.Name + ": " + checker.TypeString(t.local.Type), ""
	case t.fn != nil:
		f := t.fn
		code = f.Name + ": " + checker.TypeString(f.Type)
		if f.Decl != nil {
			code = format.FnHeader(f.Decl)
			if f.Impl != nil && f.Impl.Name != "" {
				code = "fn " + f.Impl.Name + "." + strings.TrimPrefix(code, "fn ")
			}
		}
		if used := prog.FnEffects[f.Decl]; f.Decl != nil && !f.Decl.HasWith && len(used) > 0 {
			code += " with " + ast.EffectSetString(used)
		}
		return code, f.Doc
	case t.cnst != nil:
		return "const " + t.cnst.Name + ": " + checker.TypeString(t.cnst.Type), t.cnst.Doc
	case t.typ != nil:
		return typeHeader(t.typ), t.typ.Doc
	case t.variant != nil:
		v := t.variant
		code = v.Enum.Name + "." + v.Name
		if len(v.Positional) > 0 {
			var ts []string
			for _, p := range v.Positional {
				ts = append(ts, checker.TypeString(p))
			}
			code += "(" + strings.Join(ts, ", ") + ")"
		}
		if len(v.Named) > 0 {
			code += " " + fieldList(v.Named)
		}
		return code, v.Enum.Doc
	case t.field != nil:
		return t.field.Name + ": " + checker.TypeString(t.field.Type), ""
	case t.module != nil:
		return "module " + t.module.Path, ""
	case t.expr != nil:
		if ty := prog.Types[t.expr]; ty != nil {
			return checker.TypeString(ty), ""
		}
	}
	return "", ""
}

func typeHeader(t *checker.TypeInfo) string {
	switch t.Kind {
	case checker.Record:
		return "type " + t.Name + " " + fieldList(t.Fields)
	case checker.Newtype:
		return "type " + t.Name + "(" + checker.TypeString(t.Inner) + ")"
	case checker.Alias:
		return "type " + t.Name + " = " + checker.TypeString(t.Inner)
	case checker.Enum:
		var vs []string
		for _, v := range t.Variants {
			vs = append(vs, v.Name)
		}
		return "enum " + t.Name + " { " + strings.Join(vs, ", ") + " }"
	}
	return "type " + t.Name
}

func fieldList(fs []*checker.FieldInfo) string {
	var xs []string
	for _, f := range fs {
		xs = append(xs, f.Name+": "+checker.TypeString(f.Type))
	}
	return "{ " + strings.Join(xs, ", ") + " }"
}

type location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// definition is where the name under the cursor is declared. Declarations
// in the standard library have no file to open.
func (s *Server) definition(uri string, pos Position) *location {
	h, ok := s.resolve(uri, pos)
	if !ok {
		return nil
	}
	var at ast.Pos
	var name string
	switch t := h.to; {
	case t.local != nil:
		at, name = t.local.Pos, t.local.Name
	case t.fn != nil:
		at, name = t.fn.Pos, t.fn.Name
	case t.cnst != nil:
		at, name = t.cnst.Pos, t.cnst.Name
	case t.typ != nil:
		at, name = t.typ.Pos, t.typ.Name
	case t.variant != nil:
		at, name = t.variant.Pos, t.variant.Name
	case t.field != nil:
		at, name = t.field.Pos, t.field.Name
	default:
		return nil
	}
	if at.Line == 0 || at.File == "" || strings.HasPrefix(at.File, "<std>") {
		return nil
	}
	path := filepath.Join(h.root, filepath.FromSlash(at.File))
	u := pathURI(path)
	text, ok := s.text(u)
	if !ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text = string(b)
	}
	lines := strings.Split(text, "\n")
	sel := s.selection(lines, span{from: at, word: name}, min(at.Line+3, len(lines)))
	return &location{u, sel}
}
