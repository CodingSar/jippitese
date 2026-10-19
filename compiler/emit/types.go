package emit

import (
	"strings"

	"github.com/codingsar/jippitese/compiler/checker"
)

// goType is the Go spelling of a checked type in file f (spec 11 mapping):
// primitives map to Go builtins, `()` to rt.Unit, tuples to rt.T2…T6,
// List<T> to []T, the mutable collections, Cell, Task and Gen to pointers,
// functions to `func(*rt.Ctx, …) R`. An unsolved inference variable can
// carry no value, so it is rt.Unit.
func (f *file) goType(t checker.Type) string {
	t = checker.Zonk(t)
	switch t := t.(type) {
	case *checker.TParam:
		return typeParamName(t.Name)
	case *checker.TTuple:
		if len(t.Elems) == 0 {
			return f.rt() + ".Unit"
		}
		if len(t.Elems) > 6 {
			f.p.e.failf("tuple of %d elements", len(t.Elems))
		}
		return f.rt() + ".T" + itoa(len(t.Elems)) + "[" + f.goTypes(t.Elems) + "]"
	case *checker.TFn:
		var b strings.Builder
		b.WriteString("func(*" + f.rt() + ".Ctx")
		for _, p := range t.Params {
			b.WriteString(", " + f.goType(p))
		}
		b.WriteString(") " + f.goType(t.Ret))
		return b.String()
	case *checker.TCon:
		return f.conType(t)
	}
	return f.rt() + ".Unit"
}

func (f *file) goTypes(ts []checker.Type) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = f.goType(t)
	}
	return strings.Join(parts, ", ")
}

func (f *file) conType(t *checker.TCon) string {
	ti := t.Info
	if ti.Kind == checker.Prim {
		switch ti.Name {
		case "Int":
			return "int64"
		case "Float":
			return "float64"
		case "Bool":
			return "bool"
		case "Str":
			return "string"
		case "Bytes":
			return "[]byte"
		case "Char":
			return "rune"
		case "Duration":
			return f.gotime() + ".Duration"
		}
		f.p.e.failf("unknown primitive `%s`", ti.Name)
	}
	if ti.Kind == checker.Extern && ti.Mod == "prelude" {
		switch ti.Name {
		case "List":
			return "[]" + f.goType(t.Args[0])
		case "Range":
			return "[]" + f.goType(t.Args[0])
		case "MutList", "MutMap", "MutSet", "Cell", "Task", "Gen":
			return "*" + f.rt() + "." + ti.GoIdent + f.typeArgs(t.Args)
		}
	}
	if ti.Kind == checker.Extern && ti.Mod == "std.testing" {
		// Fakes, in-memory stores and journals are rt pointers.
		return "*" + f.typeRef(ti) + f.typeArgs(t.Args)
	}
	return f.typeRef(ti) + f.typeArgs(t.Args)
}

// typeArgs is `[A, B]`, or "" for none.
func (f *file) typeArgs(ts []checker.Type) string {
	if len(ts) == 0 {
		return ""
	}
	return "[" + f.goTypes(ts) + "]"
}

// typeParamName is the Go name of a jippitese type parameter.
func typeParamName(n string) string {
	if goKeywords[n] || goPredeclared[n] || reservedAlias[n] {
		return n + "_"
	}
	return n
}

// typeParamList is the `[T any, K cmp.Ordered]` list of a declaration, or
// "". An `Ord` bound becomes cmp.Ordered so `<` and `>` lower to Go
// operators; every other bound is checked by the checker and erased.
func (f *file) typeParamList(ps []*checker.TParam) string {
	if len(ps) == 0 {
		return ""
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		c := "any"
		for _, b := range p.Bounds {
			if b == "Ord" {
				c = f.use("cmp", "cmp") + ".Ordered"
			}
		}
		parts[i] = typeParamName(p.Name) + " " + c
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// typeParamUse is the `[T, U]` instantiation of a declaration's own
// parameters, or "".
func typeParamUse(ps []*checker.TParam) string {
	if len(ps) == 0 {
		return ""
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = typeParamName(p.Name)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
