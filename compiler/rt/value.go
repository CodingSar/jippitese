package rt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- arithmetic ----

// Add is `+` on Int; overflow panics.
func Add(ctx *Ctx, a, b int64) int64 {
	c := a + b
	if (c > a) != (b > 0) {
		panic(&PanicError{Msg: fmt.Sprintf("integer overflow: %d + %d", a, b)})
	}
	return c
}

// Sub is `-` on Int; overflow panics.
func Sub(ctx *Ctx, a, b int64) int64 {
	c := a - b
	if (c < a) != (b > 0) {
		panic(&PanicError{Msg: fmt.Sprintf("integer overflow: %d - %d", a, b)})
	}
	return c
}

// Mul is `*` on Int; overflow panics.
func Mul(ctx *Ctx, a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	c := a * b
	if c/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		panic(&PanicError{Msg: fmt.Sprintf("integer overflow: %d * %d", a, b)})
	}
	return c
}

// Div is `/` on Int; division by zero panics.
func Div(ctx *Ctx, a, b int64) int64 {
	if b == 0 {
		panic(&PanicError{Msg: "division by zero"})
	}
	if a == math.MinInt64 && b == -1 {
		panic(&PanicError{Msg: fmt.Sprintf("integer overflow: %d / %d", a, b)})
	}
	return a / b
}

// Rem is `%` on Int; division by zero panics.
func Rem(ctx *Ctx, a, b int64) int64 {
	if b == 0 {
		panic(&PanicError{Msg: "division by zero"})
	}
	if b == -1 {
		return 0
	}
	return a % b
}

// ---- equality ----

// jipEqualer is implemented by containers whose Go representation is not
// canonical (Map, Set).
type jipEqualer interface{ JipEqual(other any) bool }

// Equal is structural equality on every jippitese value.
func Equal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return equalValue(reflect.ValueOf(a), reflect.ValueOf(b))
}

func equalValue(a, b reflect.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	if a.CanInterface() {
		if e, ok := a.Interface().(jipEqualer); ok {
			return e.JipEqual(b.Interface())
		}
	}
	switch a.Kind() {
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() == b.Float()
	case reflect.String:
		return a.String() == b.String()
	case reflect.Slice:
		if a.IsNil() != b.IsNil() && (a.Len() != 0 || b.Len() != 0) {
			return false
		}
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !equalValue(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if isEnum(a.Type()) {
			tag := a.Field(0).Uint()
			if tag != b.Field(0).Uint() {
				return false
			}
			name := variantNames(a)[tag]
			for i := 1; i < a.NumField(); i++ {
				if strings.HasPrefix(a.Type().Field(i).Name, name+"_") && !equalValue(a.Field(i), b.Field(i)) {
					return false
				}
			}
			return true
		}
		for i := 0; i < a.NumField(); i++ {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			if !equalValue(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Ptr, reflect.Chan, reflect.Func, reflect.Map:
		return a.Pointer() == b.Pointer()
	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() && b.IsNil()
		}
		return equalValue(a.Elem(), b.Elem())
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// JipEqual compares maps as unordered key/value sets.
func (m Map[K, V]) JipEqual(other any) bool {
	o, ok := other.(Map[K, V])
	if !ok || m.n != o.n {
		return false
	}
	eq := true
	m.root.walk(func(e *hent[K, V]) {
		if eq {
			f := o.root.find(hashOf(e.k), e.k)
			eq = f != nil && Equal(e.v, f.v)
		}
	})
	return eq
}

// JipHash is independent of insertion order, as JipEqual is.
func (m Map[K, V]) JipHash() uint64 {
	h := uint64(m.n)
	m.root.walk(func(e *hent[K, V]) { h += combine(hashOf(e.k), Hash(e.v)) })
	return h
}

// JipEqual compares sets as unordered.
func (s Set[T]) JipEqual(other any) bool {
	o, ok := other.(Set[T])
	return ok && s.m.JipEqual(o.m)
}

func (s Set[T]) JipHash() uint64 { return s.m.JipHash() }

// ---- enums by reflection ----

type variantsLister interface{ JipVariants() []string }

func isEnum(t reflect.Type) bool {
	if t.Kind() != reflect.Struct || t.NumField() == 0 || t.Field(0).Name != "Tag" {
		return false
	}
	_, ok := reflect.New(t).Elem().Interface().(variantsLister)
	return ok
}

func variantNames(v reflect.Value) []string {
	return v.Interface().(variantsLister).JipVariants()
}

// payloadFields returns the fields of variant `name`, with their json names.
func payloadFields(t reflect.Type, name string) (idx []int, names []string, positional bool) {
	positional = true
	for i := 1; i < t.NumField(); i++ {
		f := t.Field(i)
		if !strings.HasPrefix(f.Name, name+"_") {
			continue
		}
		idx = append(idx, i)
		tag := f.Tag.Get("json")
		if tag == "" {
			tag = f.Name[len(name)+1:]
		}
		if _, err := strconv.Atoi(tag); err != nil {
			positional = false
		}
		names = append(names, tag)
	}
	return
}

// MarshalEnum encodes a generated enum value with the uniform layout.
func MarshalEnum(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	t := rv.Type()
	tag := int(rv.Field(0).Uint())
	vs := variantNames(rv)
	if tag < 0 || tag >= len(vs) {
		return nil, fmt.Errorf("%s: invalid tag %d", t.Name(), tag)
	}
	name := vs[tag]
	idx, names, positional := payloadFields(t, name)
	if len(idx) == 0 {
		return json.Marshal(name)
	}
	var payload any
	switch {
	case positional && len(idx) == 1:
		payload = rv.Field(idx[0]).Interface()
	case positional:
		xs := make([]any, len(idx))
		for i, fi := range idx {
			xs[i] = rv.Field(fi).Interface()
		}
		payload = xs
	default:
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, fi := range idx {
			if i > 0 {
				buf.WriteByte(',')
			}
			k, _ := json.Marshal(names[i])
			b, err := json.Marshal(rv.Field(fi).Interface())
			if err != nil {
				return nil, err
			}
			buf.Write(k)
			buf.WriteByte(':')
			buf.Write(b)
		}
		buf.WriteByte('}')
		payload = json.RawMessage(buf.Bytes())
	}
	return json.Marshal(map[string]any{name: payload})
}

// UnmarshalEnum decodes into a generated enum value (ptr points at it).
func UnmarshalEnum(ptr any, b []byte) error {
	rv := reflect.ValueOf(ptr).Elem()
	t := rv.Type()
	vs := variantNames(rv)
	find := func(name string) int {
		for i, v := range vs {
			if v == name {
				return i
			}
		}
		return -1
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		i := find(s)
		if i < 0 {
			return fmt.Errorf("%s: unknown variant %q", t.Name(), s)
		}
		rv.Set(reflect.Zero(t))
		rv.Field(0).SetUint(uint64(i))
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("%s: expected a variant", t.Name())
	}
	if len(m) != 1 {
		return fmt.Errorf("%s: expected exactly one variant key", t.Name())
	}
	for name, raw := range m {
		i := find(name)
		if i < 0 {
			return fmt.Errorf("%s: unknown variant %q", t.Name(), name)
		}
		rv.Set(reflect.Zero(t))
		rv.Field(0).SetUint(uint64(i))
		idx, names, positional := payloadFields(t, name)
		switch {
		case len(idx) == 0:
			return nil
		case positional && len(idx) == 1:
			return json.Unmarshal(raw, rv.Field(idx[0]).Addr().Interface())
		case positional:
			var xs []json.RawMessage
			if err := json.Unmarshal(raw, &xs); err != nil {
				return err
			}
			if len(xs) != len(idx) {
				return fmt.Errorf("%s.%s: expected %d values", t.Name(), name, len(idx))
			}
			for j, fi := range idx {
				if err := json.Unmarshal(xs[j], rv.Field(fi).Addr().Interface()); err != nil {
					return err
				}
			}
			return nil
		default:
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			for j, fi := range idx {
				f, ok := fields[names[j]]
				if !ok {
					return fmt.Errorf("%s.%s: missing field %q", t.Name(), name, names[j])
				}
				if err := json.Unmarshal(f, rv.Field(fi).Addr().Interface()); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return nil
}

// ---- show ----

// Str is the text of a value inside an interpolation: strings raw,
// everything else as Show prints it.
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Show(v)
}

// Show renders a value the way a jippitese literal would read.
func Show(v any) string {
	if v == nil {
		return "()"
	}
	var sb strings.Builder
	showValue(&sb, reflect.ValueOf(v))
	return sb.String()
}

var (
	unitType     = reflect.TypeOf(Unit{})
	durationType = reflect.TypeOf(time.Duration(0))
	bytesType    = reflect.TypeOf([]byte(nil))
	jsonType     = reflect.TypeOf(Json{})
	instantType  = reflect.TypeOf(Instant{})
)

func baseName(t reflect.Type) string {
	n := t.Name()
	if i := strings.IndexByte(n, '['); i >= 0 {
		n = n[:i]
	}
	return n
}

func showValue(sb *strings.Builder, v reflect.Value) {
	t := v.Type()
	switch t {
	case unitType:
		sb.WriteString("()")
		return
	case durationType:
		sb.WriteString(showDuration(time.Duration(v.Int())))
		return
	case bytesType:
		sb.WriteString("b")
		sb.WriteString(strconv.Quote(string(v.Bytes())))
		return
	case jsonType:
		sb.WriteString(string(v.Interface().(Json).Raw))
		return
	case instantType:
		sb.WriteString("Instant(")
		sb.WriteString(time.Unix(0, v.Interface().(Instant).Nanos).UTC().Format(time.RFC3339Nano))
		sb.WriteString(")")
		return
	}
	// Named non-struct types are newtypes: Money(500), Url("…").
	if t.Name() != "" && t.Kind() != reflect.Struct && t.Kind() != reflect.Interface && t.PkgPath() != "" {
		if !(t.Kind() == reflect.Slice && baseName(t) == "") {
			sb.WriteString(baseName(t))
			sb.WriteByte('(')
			showValue(sb, v.Convert(underlying(t)))
			sb.WriteByte(')')
			return
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		sb.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int32:
		sb.WriteString(strconv.QuoteRune(rune(v.Int())))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int64:
		sb.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sb.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		sb.WriteString(showFloat(v.Float()))
	case reflect.String:
		sb.WriteString(strconv.Quote(v.String()))
	case reflect.Slice:
		sb.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				sb.WriteString(", ")
			}
			showValue(sb, v.Index(i))
		}
		sb.WriteByte(']')
	case reflect.Ptr:
		if v.IsNil() {
			sb.WriteString("()")
			return
		}
		showValue(sb, v.Elem())
	case reflect.Interface:
		if v.IsNil() {
			sb.WriteString("()")
			return
		}
		showValue(sb, v.Elem())
	case reflect.Struct:
		showStruct(sb, v)
	default:
		fmt.Fprintf(sb, "%v", v.Interface())
	}
}

func underlying(t reflect.Type) reflect.Type {
	switch t.Kind() {
	case reflect.Bool:
		return reflect.TypeOf(false)
	case reflect.Int64:
		return reflect.TypeOf(int64(0))
	case reflect.Int32:
		return reflect.TypeOf(rune(0))
	case reflect.Float64:
		return reflect.TypeOf(float64(0))
	case reflect.String:
		return reflect.TypeOf("")
	case reflect.Slice:
		return reflect.SliceOf(t.Elem())
	}
	return t
}

func showFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eIN") {
		s += ".0"
	}
	return s
}

func showDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	case d%time.Second == 0:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	case d%time.Millisecond == 0:
		return strconv.FormatInt(int64(d/time.Millisecond), 10) + "ms"
	}
	return d.String()
}

func showStruct(sb *strings.Builder, v reflect.Value) {
	t := v.Type()
	name := baseName(t)
	if m, ok := v.Interface().(jipShower); ok {
		sb.WriteString(m.JipShow())
		return
	}
	if isEnum(t) {
		tag := int(v.Field(0).Uint())
		vs := variantNames(v)
		if tag >= len(vs) {
			fmt.Fprintf(sb, "%s(?%d)", name, tag)
			return
		}
		vn := vs[tag]
		idx, names, positional := payloadFields(t, vn)
		sb.WriteString(vn)
		if len(idx) == 0 {
			return
		}
		if positional {
			sb.WriteByte('(')
			for i, fi := range idx {
				if i > 0 {
					sb.WriteString(", ")
				}
				showValue(sb, v.Field(fi))
			}
			sb.WriteByte(')')
			return
		}
		sb.WriteString(" { ")
		for i, fi := range idx {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(names[i])
			sb.WriteString(": ")
			showValue(sb, v.Field(fi))
		}
		sb.WriteString(" }")
		return
	}
	if len(name) == 2 && name[0] == 'T' && name[1] >= '2' && name[1] <= '6' && t.PkgPath() == reflect.TypeOf(Unit{}).PkgPath() {
		sb.WriteByte('(')
		for i := 0; i < v.NumField(); i++ {
			if i > 0 {
				sb.WriteString(", ")
			}
			showValue(sb, v.Field(i))
		}
		sb.WriteByte(')')
		return
	}
	sb.WriteString(name)
	sb.WriteString(" { ")
	n := 0
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if n > 0 {
			sb.WriteString(", ")
		}
		n++
		fn := f.Tag.Get("json")
		if fn == "" {
			fn = f.Name
		}
		if j := strings.IndexByte(fn, ','); j >= 0 {
			fn = fn[:j]
		}
		sb.WriteString(fn)
		sb.WriteString(": ")
		showValue(sb, v.Field(i))
	}
	sb.WriteString(" }")
}

type jipShower interface{ JipShow() string }

// JipShow renders a Map as `{k: v, …}`.
func (m Map[K, V]) JipShow() string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, e := range m.root.ordered(m.n, m.next) {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(Show(e.k))
		sb.WriteString(": ")
		sb.WriteString(Show(e.v))
	}
	sb.WriteByte('}')
	return sb.String()
}

// JipShow renders a Set as `#{a, b}`.
func (s Set[T]) JipShow() string {
	var sb strings.Builder
	sb.WriteString("#{")
	for i, x := range s.Items() {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(Show(x))
	}
	sb.WriteByte('}')
	return sb.String()
}

// ---- Json ----

// Json is a schema-less JSON document.
type Json struct{ Raw []byte }

// MarshalJSON writes the document verbatim.
func (j Json) MarshalJSON() ([]byte, error) {
	if len(j.Raw) == 0 {
		return []byte("null"), nil
	}
	return j.Raw, nil
}

// UnmarshalJSON keeps the document verbatim.
func (j *Json) UnmarshalJSON(b []byte) error {
	j.Raw = append([]byte(nil), b...)
	return nil
}

// JipEqual compares documents by their compacted text.
func (j Json) JipEqual(other any) bool {
	o, ok := other.(Json)
	if !ok {
		return false
	}
	var a, b bytes.Buffer
	if json.Compact(&a, j.Raw) != nil || json.Compact(&b, o.Raw) != nil {
		return bytes.Equal(j.Raw, o.Raw)
	}
	return bytes.Equal(a.Bytes(), b.Bytes())
}

// JsonEncode implements `std.json.encode`.
func JsonEncode[T any](ctx *Ctx, v T) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(&PanicError{Msg: "json.encode: " + err.Error()})
	}
	return b
}

// JsonDecode implements `std.json.decode`.
func JsonDecode[T any](ctx *Ctx, b []byte) Result[T, JsonError] {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		switch e := err.(type) {
		case *json.SyntaxError:
			return Result_Err[T](JsonError_Syntax(e.Offset))
		default:
			return Result_Err[T](JsonError_Shape(err.Error()))
		}
	}
	return Result_Ok[T, JsonError](v)
}

// MarshalJSON encodes a Map as an object when its keys are strings, else as
// a list of [k, v] pairs.
func (m Map[K, V]) MarshalJSON() ([]byte, error) {
	var zero K
	if _, ok := any(zero).(string); ok || reflect.TypeOf(zero) != nil && reflect.TypeOf(zero).Kind() == reflect.String {
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, e := range m.root.ordered(m.n, m.next) {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(reflect.ValueOf(e.k).String())
			vb, err := json.Marshal(e.v)
			if err != nil {
				return nil, err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	}
	return json.Marshal(m.Entries())
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (m *Map[K, V]) UnmarshalJSON(b []byte) error {
	var zero K
	if reflect.TypeOf(zero) != nil && reflect.TypeOf(zero).Kind() == reflect.String {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(b, &obj); err != nil {
			return err
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		*m = Map[K, V]{}
		for _, k := range keys {
			var v V
			if err := json.Unmarshal(obj[k], &v); err != nil {
				return err
			}
			kk := reflect.ValueOf(&zero).Elem()
			kk.SetString(k)
			*m = m.insert(kk.Interface().(K), v)
		}
		return nil
	}
	var pairs []T2[K, V]
	if err := json.Unmarshal(b, &pairs); err != nil {
		return err
	}
	*m = Map[K, V]{}
	for _, p := range pairs {
		*m = m.insert(p.F0, p.F1)
	}
	return nil
}

// MarshalJSON encodes a Set as a list.
func (s Set[T]) MarshalJSON() ([]byte, error) { return json.Marshal(s.Items()) }

// UnmarshalJSON decodes a Set from a list.
func (s *Set[T]) UnmarshalJSON(b []byte) error {
	var xs []T
	if err := json.Unmarshal(b, &xs); err != nil {
		return err
	}
	*s = Set[T]{}
	for _, x := range xs {
		*s = SetInsert(nil, *s, x)
	}
	return nil
}
