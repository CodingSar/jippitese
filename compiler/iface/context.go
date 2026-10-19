package iface

import (
	"regexp"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// ContextOptions are the flags of `jip context`.
type ContextOptions struct {
	Short bool   // one line per symbol, signature only
	For   string // narrow both tiers to this file (path as the module records it)
}

// ref is one use of a dependency item, at a source position.
type ref struct {
	mod  *checker.Module
	name string
	file int
	pos  ast.Pos
}

func before(a, b ref) bool {
	if a.file != b.file {
		return a.file < b.file
	}
	if a.pos.Line != b.pos.Line {
		return a.pos.Line < b.pos.Line
	}
	return a.pos.Col < b.pos.Col
}

// Context prints the context pack of module m (spec 09 rule 10): tier 1 is
// the module's interface, tier 2 lists for each direct dependency, in
// first-reference order, only the referenced items plus the closure of the
// types their signatures mention.
func Context(prog *checker.Program, m *checker.Module, o ContextOptions) string {
	keep := func(f *ast.File) bool { return o.For == "" || f.Path == o.For || strings.HasSuffix(f.Path, "/"+o.For) }
	var b strings.Builder
	tier1 := ExtractFiles(m, keep)
	if o.Short {
		b.WriteString("interface " + m.Path + "\n")
		for _, e := range tier1.Public {
			b.WriteString(e.Sig + "\n")
			for _, l := range e.Lines {
				if isMemberFn(l) {
					b.WriteString("  " + l + "\n")
				}
			}
		}
	} else {
		b.WriteString(tier1.Text())
	}

	deps := map[*checker.Module]bool{}
	for _, d := range m.Deps {
		deps[d] = true
	}
	refs := collectRefs(prog, m, deps, keep)
	first := map[*checker.Module]ref{}
	var order []*checker.Module
	for _, r := range refs {
		if f, ok := first[r.mod]; !ok || before(r, f) {
			if !ok {
				order = append(order, r.mod)
			}
			first[r.mod] = r
		}
	}
	sortModules(order, first)
	b.WriteString("\n")
	for _, d := range order {
		var names []string
		seen := map[string]bool{}
		for _, r := range sortedRefs(refs, d) {
			if !seen[r.name] {
				seen[r.name] = true
				names = append(names, r.name)
			}
		}
		b.WriteString("-- " + d.Path + " (referenced: " + strings.Join(names, ", ") + ")\n")
		for _, e := range closure(Extract(d), names) {
			b.WriteString(e.Sig + "\n")
		}
	}
	for _, alias := range m.UseOrder {
		d := m.Deps[alias]
		if d == nil {
			continue
		}
		if _, ok := first[d]; !ok {
			b.WriteString("-- " + d.Path + " (imported, nothing referenced)\n")
		}
	}
	return b.String()
}

func isMemberFn(l string) bool {
	return strings.HasPrefix(l, "fn ") || strings.HasPrefix(l, "pub fn ")
}

func sortModules(ms []*checker.Module, first map[*checker.Module]ref) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && before(first[ms[j]], first[ms[j-1]]); j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}

func sortedRefs(refs []ref, d *checker.Module) []ref {
	var out []ref
	for _, r := range refs {
		if r.mod == d {
			out = append(out, r)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && before(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// collectRefs finds every use of a direct dependency's item in m.
func collectRefs(prog *checker.Program, m *checker.Module, deps map[*checker.Module]bool, keep func(*ast.File) bool) []ref {
	var refs []ref
	for fi, f := range m.Files {
		if !keep(f) {
			continue
		}
		add := func(mod *checker.Module, name string, pos ast.Pos) {
			if mod != nil && deps[mod] {
				refs = append(refs, ref{mod: mod, name: name, file: fi, pos: pos})
			}
		}
		typ := func(ti *checker.TypeInfo, pos ast.Pos) {
			if ti != nil {
				add(ti.Module, ti.Name, pos)
			}
		}
		fn := func(x *checker.FnInfo, pos ast.Pos) {
			if x == nil {
				return
			}
			if x.Impl != nil && x.Impl.TargetInfo != nil {
				typ(x.Impl.TargetInfo, pos)
				return
			}
			add(x.Module, x.Name, pos)
		}
		for _, it := range f.Items {
			ast.WalkItemExprs(it, func(e ast.Expr) {
				switch x := e.(type) {
				case *ast.Ident:
					if r := prog.Refs[x]; r != nil {
						switch r.Kind {
						case checker.RefFn:
							fn(r.Fn, x.Pos)
						case checker.RefConst:
							add(r.Const.Module, r.Const.Name, x.Pos)
						case checker.RefType:
							typ(r.Type, x.Pos)
						case checker.RefVariant:
							typ(r.Variant.Enum, x.Pos)
						}
					}
				case *ast.FieldExpr:
					if r := prog.Fields[x]; r != nil {
						switch r.Kind {
						case checker.FieldModuleItem:
							if r.Fn != nil {
								fn(r.Fn, x.Pos)
							} else if r.Const != nil {
								add(r.Const.Module, r.Const.Name, x.Pos)
							} else {
								typ(r.Type, x.Pos)
							}
						case checker.FieldVariant:
							typ(r.Variant.Enum, x.Pos)
						case checker.FieldStaticFn:
							fn(r.Fn, x.Pos)
						}
					}
				case *ast.RecordLit:
					typ(prog.RecordLits[x], x.Pos)
				}
			})
			ast.WalkItemTypes(it, func(t ast.TypeExpr) {
				tp, ok := t.(*ast.TypePath)
				if !ok || len(tp.Path) != 2 {
					return
				}
				if d := m.Deps[tp.Path[0]]; d != nil {
					add(d, tp.Path[1], tp.Pos)
				}
			})
		}
	}
	return refs
}

var typeName = regexp.MustCompile(`(^|[^.\w])([A-Z][A-Za-z0-9_]*)`)

// closure returns the entries for names, in order, then the exported
// types their signatures mention, transitively, in discovery order.
func closure(in *Interface, names []string) []Entry {
	byName := map[string]Entry{}
	for _, e := range in.Public {
		if e.Kind != KImpl {
			if _, dup := byName[e.Name]; !dup {
				byName[e.Name] = e
			}
		}
	}
	var out []Entry
	done := map[string]bool{}
	queue := append([]string{}, names...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		e, ok := byName[n]
		if !ok || done[n] {
			continue
		}
		done[n] = true
		out = append(out, e)
		sig := e.Sig
		if i := strings.Index(sig, " "+e.Name); i >= 0 {
			sig = sig[i+len(e.Name)+1:]
		}
		for _, mt := range typeName.FindAllStringSubmatch(sig, -1) {
			if t, ok := byName[mt[2]]; ok && (t.Kind == KType || t.Kind == KEnum) && !done[mt[2]] {
				queue = append(queue, mt[2])
			}
		}
	}
	return out
}
