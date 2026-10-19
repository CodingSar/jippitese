// Package iface extracts, prints, parses and classifies interface files
// (spec 09 rules 7 and 8): the public surface of a module with effects and
// contracts, no bodies, digested so dependents rebuild only on change.
package iface

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/format"
)

// FileName is the interface file in every module directory.
const FileName = "interface.jipi"

// Kind orders entries: types, enums, behaviours, consts, functions, impls.
type Kind int

const (
	KType Kind = iota
	KEnum
	KBehaviour
	KConst
	KFn
	KImpl
)

// Entry is one exported item.
type Entry struct {
	Kind  Kind
	Name  string // sort key and identity: item name, or the impl header
	Doc   string
	Sig   string   // the signature line
	Lines []string // contract lines and impl members, without indentation
}

// Interface is one module's interface file.
type Interface struct {
	Path   string
	Public []Entry
	Module []Entry // pub(module) items
}

// Extract builds the interface of a checked module.
func Extract(m *checker.Module) *Interface { return ExtractFiles(m, nil) }

// ExtractFiles builds the interface from the files keep accepts (all when
// keep is nil); `jip context --for` narrows tier 1 with it.
func ExtractFiles(m *checker.Module, keep func(*ast.File) bool) *Interface {
	in := &Interface{Path: m.Path}
	for _, f := range m.Files {
		if keep != nil && !keep(f) {
			continue
		}
		for _, it := range f.Items {
			e, vis, ok := entry(m, it)
			if !ok {
				continue
			}
			switch vis {
			case ast.PubModule:
				in.Module = append(in.Module, e)
			case ast.Private:
			default:
				in.Public = append(in.Public, e)
			}
		}
	}
	sortEntries(in.Public)
	sortEntries(in.Module)
	return in
}

func sortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Kind != es[j].Kind {
			return es[i].Kind < es[j].Kind
		}
		return es[i].Name < es[j].Name
	})
}

func vis(v ast.Visibility) string {
	if v == ast.Private {
		return ""
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

func fields(fs []ast.Field) string {
	var xs []string
	for _, f := range fs {
		xs = append(xs, f.Name+": "+format.Type(f.Type))
	}
	if len(xs) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(xs, ", ") + " }"
}

// FnSig prints a function signature without body, as `fn name<T>(…) -> R
// with {…}` (no visibility).
func FnSig(d *ast.FnDecl) string {
	var ps []string
	for _, p := range d.Params {
		switch {
		case p.Self:
			ps = append(ps, "self")
		case p.Type != nil:
			ps = append(ps, p.Name+": "+format.Type(p.Type))
		default:
			ps = append(ps, p.Name)
		}
	}
	s := "fn " + d.Name + typeParams(d.TypeParams) + "(" + strings.Join(ps, ", ") + ")"
	if d.Ret != nil {
		s += " -> " + format.Type(d.Ret)
	}
	if d.HasWith {
		s += " with " + format.Effects(d.Effects)
	}
	return s
}

func contracts(kind string, es []ast.Expr) []string {
	var out []string
	for _, e := range es {
		out = append(out, fmt.Sprintf("%-4s %s", kind, format.Expr(e)))
	}
	return out
}

// entry renders one item; ok is false for items that never appear.
func entry(m *checker.Module, it ast.Item) (Entry, ast.Visibility, bool) {
	switch d := it.(type) {
	case *ast.TypeDecl:
		e := Entry{Kind: KType, Name: d.Name, Doc: d.Doc}
		head := vis(d.Vis) + "type " + d.Name + typeParams(d.Params)
		if d.Opaque {
			e.Sig = vis(d.Vis) + "opaque type " + d.Name + typeParams(d.Params)
			return e, d.Vis, true
		}
		switch d.Kind {
		case ast.RecordKind:
			e.Sig = head + " " + fields(d.Fields)
		case ast.NewtypeKind:
			e.Sig = head + "(" + format.Type(d.Inner) + ")"
		case ast.AliasKind:
			e.Sig = head + " = " + format.Type(d.Inner)
		case ast.ExternKind:
			e.Sig = vis(d.Vis) + "opaque type " + d.Name + typeParams(d.Params)
		}
		e.Lines = contracts("inv", d.Inv)
		return e, d.Vis, true
	case *ast.EnumDecl:
		var vs []string
		for _, v := range d.Variants {
			s := v.Name
			if len(v.Positional) > 0 {
				var ts []string
				for _, t := range v.Positional {
					ts = append(ts, format.Type(t))
				}
				s += "(" + strings.Join(ts, ", ") + ")"
			} else if len(v.Named) > 0 {
				s += " " + fields(v.Named)
			}
			vs = append(vs, s)
		}
		return Entry{Kind: KEnum, Name: d.Name, Doc: d.Doc, Sig: vis(d.Vis) + "enum " + d.Name + typeParams(d.Params) + " { " + strings.Join(vs, ", ") + " }"}, d.Vis, true
	case *ast.BehaviourDecl:
		e := Entry{Kind: KBehaviour, Name: d.Name, Doc: d.Doc, Sig: vis(d.Vis) + "behaviour " + d.Name + typeParams(d.Params)}
		for _, a := range d.Assoc {
			e.Lines = append(e.Lines, "type "+a)
		}
		for _, c := range d.Consts {
			e.Lines = append(e.Lines, "const "+c.Name+": "+format.Type(c.Type))
		}
		for _, fd := range d.Methods {
			s := FnSig(fd)
			if fd.Body != nil {
				s += " {…}"
			}
			e.Lines = append(e.Lines, s)
		}
		e.Lines = append(e.Lines, contracts("inv", d.Inv)...)
		return e, d.Vis, true
	case *ast.ConstDecl:
		s := vis(d.Vis) + "const " + d.Name
		if d.Type != nil {
			s += ": " + format.Type(d.Type)
		}
		if d.Value != nil {
			s += " = " + format.Expr(d.Value)
		}
		return Entry{Kind: KConst, Name: d.Name, Doc: d.Doc, Sig: s}, d.Vis, true
	case *ast.FnDecl:
		e := Entry{Kind: KFn, Name: d.Name, Doc: d.Doc, Sig: vis(d.Vis) + FnSig(d)}
		e.Lines = append(contracts("pre", d.Pre), contracts("post", d.Post)...)
		return e, d.Vis, true
	case *ast.ImplDecl:
		return implEntry(m, d)
	}
	return Entry{}, ast.Private, false
}

// implEntry exports an impl when its target type is exported.
func implEntry(m *checker.Module, d *ast.ImplDecl) (Entry, ast.Visibility, bool) {
	target := format.Type(d.Target)
	name := target
	if tp, ok := d.Target.(*ast.TypePath); ok && len(tp.Path) == 1 {
		name = tp.Path[0]
	}
	ti := m.Types[name]
	if ti == nil {
		return Entry{}, ast.Private, false
	}
	head := "impl " + target
	if d.Behaviour != "" {
		b := d.Behaviour
		if len(d.BehaviourArgs) > 0 {
			var ts []string
			for _, t := range d.BehaviourArgs {
				ts = append(ts, format.Type(t))
			}
			b += "<" + strings.Join(ts, ", ") + ">"
		}
		head = "impl " + b + " for " + target
	}
	e := Entry{Kind: KImpl, Name: head, Doc: d.Doc, Sig: head}
	for _, a := range d.Assoc {
		e.Lines = append(e.Lines, "type "+a.Name+" = "+format.Type(a.Type))
	}
	for _, c := range d.Consts {
		e.Lines = append(e.Lines, "const "+c.Name+" = "+format.Expr(c.Value))
	}
	for _, fd := range d.Methods {
		if d.Behaviour == "" {
			if fd.Vis == ast.Private {
				continue
			}
			if fd.Doc != "" {
				e.Lines = append(e.Lines, "/// "+fd.Doc)
			}
			e.Lines = append(e.Lines, vis(fd.Vis)+FnSig(fd))
		} else {
			e.Lines = append(e.Lines, FnSig(fd))
		}
		for _, c := range append(contracts("pre", fd.Pre), contracts("post", fd.Post)...) {
			e.Lines = append(e.Lines, "  "+c)
		}
	}
	e.Lines = append(e.Lines, contracts("inv", d.Inv)...)
	return e, ti.Vis, true
}

// ---- text ------------------------------------------------------------------

func entriesText(es []Entry) string {
	var b strings.Builder
	for _, e := range es {
		b.WriteString("\n")
		if e.Doc != "" {
			b.WriteString("/// " + e.Doc + "\n")
		}
		b.WriteString(e.Sig + "\n")
		for _, l := range e.Lines {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

// Digest is the sha256 of a section's entries.
func Digest(es []Entry) string {
	h := sha256.Sum256([]byte(entriesText(es)))
	return "sha256:" + hex.EncodeToString(h[:])
}

// Text is the interface file.
func (in *Interface) Text() string {
	var b strings.Builder
	b.WriteString("interface " + in.Path + " digest " + Digest(in.Public) + "\n")
	b.WriteString(entriesText(in.Public))
	if len(in.Module) > 0 {
		b.WriteString("\nmodule-visible digest " + Digest(in.Module) + "\n")
		b.WriteString(entriesText(in.Module))
	}
	return b.String()
}

// Parse reads an interface file back. It also returns the digests the
// file claims, which a hand edit can make disagree with the entries.
func Parse(text string) (in *Interface, pubDigest, modDigest string, err error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 0 {
		return nil, "", "", fmt.Errorf("empty interface file")
	}
	var path string
	if _, err := fmt.Sscanf(lines[0], "interface %s digest %s", &path, &pubDigest); err != nil {
		return nil, "", "", fmt.Errorf("line 1: expected `interface <path> digest sha256:<hex>`")
	}
	in = &Interface{Path: path}
	section := &in.Public
	var cur *Entry
	flush := func() {
		if cur != nil {
			*section = append(*section, *cur)
			cur = nil
		}
	}
	for i, l := range lines[1:] {
		switch {
		case l == "":
			flush()
		case strings.HasPrefix(l, "module-visible digest "):
			flush()
			modDigest = strings.TrimPrefix(l, "module-visible digest ")
			section = &in.Module
		case strings.HasPrefix(l, "  "):
			if cur == nil {
				return nil, "", "", fmt.Errorf("line %d: member line outside an entry", i+2)
			}
			cur.Lines = append(cur.Lines, strings.TrimPrefix(l, "  "))
		case strings.HasPrefix(l, "/// ") && cur == nil:
			cur = &Entry{Doc: strings.TrimPrefix(l, "/// ")}
		default:
			if cur == nil {
				cur = &Entry{}
			}
			if cur.Sig != "" {
				return nil, "", "", fmt.Errorf("line %d: two signatures in one entry", i+2)
			}
			cur.Sig = l
			cur.Kind, cur.Name = classifySig(l)
		}
	}
	flush()
	return in, pubDigest, modDigest, nil
}

// classifySig recovers an entry's kind and name from its signature line.
func classifySig(sig string) (Kind, string) {
	s := strings.TrimPrefix(sig, "pub ")
	s = strings.TrimPrefix(s, "opaque ")
	word := func(rest string) string {
		end := strings.IndexAny(rest, "<({ :=")
		if end < 0 {
			return rest
		}
		return rest[:end]
	}
	switch {
	case strings.HasPrefix(s, "type "):
		return KType, word(s[5:])
	case strings.HasPrefix(s, "enum "):
		return KEnum, word(s[5:])
	case strings.HasPrefix(s, "behaviour "):
		return KBehaviour, word(s[10:])
	case strings.HasPrefix(s, "const "):
		return KConst, word(s[6:])
	case strings.HasPrefix(s, "fn "):
		return KFn, word(s[3:])
	case strings.HasPrefix(sig, "impl "):
		return KImpl, sig
	}
	return KFn, sig
}
