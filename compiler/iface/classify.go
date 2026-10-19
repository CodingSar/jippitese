package iface

import (
	"sort"
	"strings"
)

// Level is a classification result.
type Level int

const (
	None Level = iota
	Compatible
	Breaking
)

func (l Level) String() string {
	return [...]string{"none", "compatible", "breaking"}[l]
}

// Change is one changed entry: its old and new rendering and why.
type Change struct {
	Old, New string
	Reason   string
	Level    Level
}

// Report is the classification of a fresh interface against the committed
// one. Only the public section is classified (spec 09 rule 7, R7).
type Report struct {
	Path    string
	Level   Level
	Removed []string
	Added   []string
	Changed []Change
}

type key struct {
	k    Kind
	name string
}

// Classify compares two interfaces of the same module.
func Classify(old, cur *Interface) Report {
	r := Report{Path: cur.Path}
	om, nm := index(old.Public), index(cur.Public)
	for _, e := range old.Public {
		k := key{e.Kind, e.Name}
		n, ok := nm[k]
		if !ok {
			r.Removed = append(r.Removed, e.Sig)
			r.raise(Breaking)
			continue
		}
		if c, changed := compare(e, n); changed {
			r.Changed = append(r.Changed, c)
			r.raise(c.Level)
		}
	}
	for _, e := range cur.Public {
		if _, ok := om[key{e.Kind, e.Name}]; !ok {
			r.Added = append(r.Added, e.Sig)
			r.raise(Compatible)
		}
	}
	return r
}

func (r *Report) raise(l Level) {
	if l > r.Level {
		r.Level = l
	}
}

func index(es []Entry) map[key]Entry {
	m := map[key]Entry{}
	for _, e := range es {
		m[key{e.Kind, e.Name}] = e
	}
	return m
}

func render(e Entry) string {
	s := e.Sig
	for _, l := range e.Lines {
		s += "\n      " + l
	}
	return s
}

// compare classifies the change of one entry.
func compare(o, n Entry) (Change, bool) {
	c := Change{Old: render(o), New: render(n)}
	if o.Sig == n.Sig && strings.Join(o.Lines, "\n") == strings.Join(n.Lines, "\n") {
		if o.Doc != n.Doc {
			c.Reason, c.Level = "doc change", Compatible
			return c, true
		}
		return c, false
	}
	level, reason := None, ""
	set := func(l Level, why string) {
		if l > level {
			level, reason = l, why
		} else if l == level && reason == "" {
			reason = why
		}
	}
	if o.Sig != n.Sig {
		switch {
		case o.Kind == KConst && constHead(o.Sig) == constHead(n.Sig):
			set(Compatible, "changed const value")
		case o.Kind == KFn:
			ob, oe := splitEffects(o.Sig)
			nb, ne := splitEffects(n.Sig)
			switch {
			case ob != nb:
				set(Breaking, "changed signature")
			case subset(ne, oe):
				set(Compatible, "removed effect")
			default:
				set(Breaking, "added effect")
			}
		case o.Kind == KEnum:
			ov, nv := variants(o.Sig), variants(n.Sig)
			head := func(s string) string { return s[:strings.Index(s, "{")] }
			switch {
			case head(o.Sig) != head(n.Sig):
				set(Breaking, "changed signature")
			case !subset(ov, nv):
				set(Breaking, "removed or renamed variant")
			case strings.HasPrefix(n.Sig, "pub opaque"):
				set(Compatible, "added variant on an opaque enum")
			default:
				set(Breaking, "added variant on a non-opaque enum")
			}
		default:
			set(Breaking, "changed signature")
		}
	}
	// Contract and member lines.
	oc, om := splitLines(o.Lines)
	nc, nm := splitLines(n.Lines)
	if strings.Join(om, "\n") != strings.Join(nm, "\n") {
		switch {
		case onlyConstValues(om, nm):
			set(Compatible, "changed const value")
		case o.Kind == KImpl && subset(om, nm) && !hasBehaviour(o.Sig):
			set(Compatible, "added method")
		case o.Kind == KBehaviour && subset(om, nm):
			set(Breaking, "added required associated item")
		default:
			set(Breaking, "changed members")
		}
	}
	for _, kind := range []string{"pre", "post", "inv"} {
		oset, nset := oc[kind], nc[kind]
		if strings.Join(oset, "\n") == strings.Join(nset, "\n") {
			continue
		}
		switch {
		case kind == "pre" && subset(nset, oset):
			set(Compatible, "removed pre conjunct")
		case kind == "post" && subset(oset, nset):
			set(Compatible, "added post conjunct")
		default:
			set(Breaking, "contract-changed")
		}
	}
	if level == None {
		level, reason = Compatible, "doc change"
	}
	c.Level, c.Reason = level, reason
	return c, true
}

func hasBehaviour(sig string) bool { return strings.Contains(sig, " for ") }

// splitEffects separates `… with {a, b}` into the rest and the effects.
func splitEffects(sig string) (string, []string) {
	i := strings.LastIndex(sig, " with {")
	if i < 0 {
		return sig, nil
	}
	inner := strings.TrimSuffix(sig[i+len(" with {"):], "}")
	if inner == "" {
		return sig[:i], nil
	}
	return sig[:i], strings.Split(inner, ", ")
}

// variants lists the top-level variants of an enum signature.
func variants(sig string) []string {
	i, j := strings.Index(sig, "{"), strings.LastIndex(sig, "}")
	if i < 0 || j <= i {
		return nil
	}
	return splitTop(strings.TrimSpace(sig[i+1 : j]))
}

// splitTop splits on commas outside brackets.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(', '{', '<', '[':
			depth++
		case ')', '}', '>', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		out = append(out, t)
	}
	return out
}

// splitLines separates contract lines (by kind) from member lines.
func splitLines(ls []string) (map[string][]string, []string) {
	cs := map[string][]string{}
	var members []string
	for _, l := range ls {
		f := strings.Fields(l)
		if len(f) > 0 && (f[0] == "pre" || f[0] == "post" || f[0] == "inv") {
			cs[f[0]] = append(cs[f[0]], strings.TrimSpace(strings.TrimPrefix(l, f[0])))
			continue
		}
		members = append(members, l)
	}
	for k := range cs {
		sort.Strings(cs[k])
	}
	return cs, members
}

// subset says whether every element of a is in b.
func subset(a, b []string) bool {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	for _, x := range a {
		if !in[x] {
			return false
		}
	}
	return true
}

// Text prints a report as `jip check` shows it.
func (r Report) Text() string {
	var b strings.Builder
	b.WriteString(r.Path + ": " + r.Level.String() + "\n")
	if len(r.Removed) > 0 {
		b.WriteString("  Removed:\n")
		for _, s := range r.Removed {
			b.WriteString("    - " + s + "\n")
		}
	}
	if len(r.Changed) > 0 {
		b.WriteString("  Changed:\n")
		for _, c := range r.Changed {
			b.WriteString("    - " + c.Old + "\n    + " + c.New + "\n      (" + c.Reason + ", " + c.Level.String() + ")\n")
		}
	}
	if len(r.Added) > 0 {
		b.WriteString("  Added:\n")
		for _, s := range r.Added {
			b.WriteString("    + " + s + "\n")
		}
	}
	return b.String()
}

// constHead is a const signature without its value.
func constHead(sig string) string {
	if i := strings.Index(sig, " = "); i >= 0 {
		return sig[:i]
	}
	return sig
}

// onlyConstValues says whether two member lists differ only in the values
// of `const` lines: a value is not part of any signature (spec 09 rule 8
// lists no such break), and a workflow `version` bump must not need
// --accept-breaking.
func onlyConstValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if !strings.HasPrefix(a[i], "const ") || constHead(a[i]) != constHead(b[i]) {
			return false
		}
	}
	return true
}
