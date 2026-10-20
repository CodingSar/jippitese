package lsp

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/format"
	"github.com/codingsar/jippitese/compiler/parser"
)

// LSP SymbolKind values used here.
const (
	kindClass      = 5
	kindMethod     = 6
	kindField      = 8
	kindEnum       = 10
	kindInterface  = 11
	kindFunction   = 12
	kindConstant   = 14
	kindEnumMember = 22
	kindStruct     = 23
	kindEvent      = 24
)

type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

// span is a symbol in byte positions before its extent is known: it starts
// at from, and its name is the first whole-word word at or after from.
type span struct {
	name, detail string
	kind         int
	from         ast.Pos
	word         string // text to select, searched for from `from`
	children     []span
}

// documentSymbols outlines the buffer: top-level items and scenarios, with
// fields, variants, methods and consts as children. A buffer that does not
// parse has no outline.
func (s *Server) documentSymbols(uri string) []documentSymbol {
	t, ok := s.text(uri)
	if !ok {
		return nil
	}
	f, err := parser.ParseFile(filepath.Base(uriPath(uri)), t)
	if err != nil {
		return []documentSymbol{}
	}
	var top []span
	for _, it := range f.Items {
		top = append(top, itemSpan(it))
	}
	for _, sc := range f.Scenarios {
		top = append(top, span{name: sc.Name, kind: kindEvent, from: sc.Pos, word: "scenario"})
	}
	sortSpans(top)
	lines := strings.Split(t, "\n")
	return s.outline(lines, top, len(lines))
}

func itemSpan(it ast.Item) span {
	switch d := it.(type) {
	case *ast.FnDecl:
		return span{name: d.Name, detail: format.FnHeader(d), kind: kindFunction, from: d.Pos, word: d.Name}
	case *ast.ConstDecl:
		return constSpan(d)
	case *ast.TypeDecl:
		sp := span{name: d.Name, kind: kindStruct, from: d.Pos, word: d.Name}
		switch d.Kind {
		case ast.NewtypeKind, ast.AliasKind:
			sp.kind = kindClass
			if d.Inner != nil {
				sp.detail = format.Type(d.Inner)
			}
		case ast.ExternKind:
			sp.kind = kindClass
		}
		for _, fd := range d.Fields {
			sp.children = append(sp.children, span{name: fd.Name, detail: format.Type(fd.Type), kind: kindField, from: fd.Pos, word: fd.Name})
		}
		return sp
	case *ast.EnumDecl:
		sp := span{name: d.Name, kind: kindEnum, from: d.Pos, word: d.Name}
		for _, v := range d.Variants {
			sp.children = append(sp.children, span{name: v.Name, kind: kindEnumMember, from: v.Pos, word: v.Name})
		}
		return sp
	case *ast.ImplDecl:
		name := "impl " + format.Type(d.Target)
		if d.Behaviour != "" {
			name = "impl " + d.Behaviour + " for " + format.Type(d.Target)
		}
		sp := span{name: name, kind: kindClass, from: d.Pos, word: "impl"}
		sp.children = members(d.Consts, d.Methods)
		return sp
	case *ast.BehaviourDecl:
		sp := span{name: d.Name, kind: kindInterface, from: d.Pos, word: d.Name}
		sp.children = members(d.Consts, d.Methods)
		return sp
	}
	return span{name: "?", kind: kindFunction, from: it.ItemPos()}
}

func constSpan(d *ast.ConstDecl) span {
	sp := span{name: d.Name, kind: kindConstant, from: d.Pos, word: d.Name}
	if d.Type != nil {
		sp.detail = format.Type(d.Type)
	}
	return sp
}

func members(consts []*ast.ConstDecl, methods []*ast.FnDecl) []span {
	var out []span
	for _, c := range consts {
		out = append(out, constSpan(c))
	}
	for _, m := range methods {
		out = append(out, span{name: m.Name, detail: format.FnHeader(m), kind: kindMethod, from: m.Pos, word: m.Name})
	}
	sortSpans(out)
	return out
}

func sortSpans(ss []span) {
	slices.SortStableFunc(ss, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.from.Line, b.from.Line), cmp.Compare(a.from.Col, b.from.Col))
	})
}

// outline turns sibling spans into symbols. Each span runs to the last
// non-blank, non-comment line before the next sibling, or before limit (a
// 1-based line past the parent's end) for the last one.
func (s *Server) outline(lines []string, ss []span, limit int) []documentSymbol {
	out := make([]documentSymbol, 0, len(ss))
	for i, sp := range ss {
		next := limit
		if i+1 < len(ss) {
			next = ss[i+1].from.Line
		}
		end := next - 1
		for end > sp.from.Line && skippable(line(lines, end)) {
			end--
		}
		if end < sp.from.Line {
			end = sp.from.Line
		}
		last := line(lines, end)
		rng := Range{s.at(lines, sp.from.Line, sp.from.Col), s.at(lines, end, len(last)+1)}
		if end == sp.from.Line && next == sp.from.Line {
			// siblings on one line: stop at the next one
			rng.End = s.at(lines, end, ss[i+1].from.Col)
		}
		inner := end + 1
		if strings.TrimSpace(last) == "}" {
			inner = end // the last member stops before the closing brace
		}
		out = append(out, documentSymbol{
			Name: sp.name, Detail: sp.detail, Kind: sp.kind,
			Range: rng, SelectionRange: s.selection(lines, sp, end),
			Children: s.outline(lines, sp.children, inner),
		})
	}
	return out
}

// selection is the first whole-word occurrence of sp.word between sp.from
// and line end, or the start position when it is not found.
func (s *Server) selection(lines []string, sp span, end int) Range {
	col := sp.from.Col
	for n := sp.from.Line; n <= end && sp.word != ""; n++ {
		l := line(lines, n)
		for off := col - 1; off >= 0 && off <= len(l); {
			k := strings.Index(l[off:], sp.word)
			if k < 0 {
				break
			}
			a, b := off+k, off+k+len(sp.word)
			if (a == 0 || !wordByte(l[a-1])) && (b == len(l) || !wordByte(l[b])) {
				return Range{s.at(lines, n, a+1), s.at(lines, n, b+1)}
			}
			off = b
		}
		col = 1
	}
	p := s.at(lines, sp.from.Line, sp.from.Col)
	return Range{p, p}
}

func wordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func skippable(l string) bool {
	l = strings.TrimSpace(l)
	return l == "" || strings.HasPrefix(l, "//")
}

func line(lines []string, n int) string {
	if n < 1 || n > len(lines) {
		return ""
	}
	return strings.TrimSuffix(lines[n-1], "\r")
}

func (s *Server) at(lines []string, n, col int) Position {
	return Position{Line: max(n-1, 0), Character: s.character(line(lines, n), col)}
}
