package rt

import (
	"reflect"
	"strings"
)

// JSON Schema (draft 2020-12 subset) of a generated Go type, following the
// encodings of MarshalJSON: a record is an object of its json fields, an
// enum a variant name or a one-key object, Option its value or null, a Map
// with string keys an object, any other Map a list of {F0, F1} entries, a
// Set a list. A recursive type is open ({}) where it recurs.

var rtPkg = reflect.TypeOf(Unit{}).PkgPath()

// SchemaOf returns the JSON Schema of the values of t.
func SchemaOf(t reflect.Type) map[string]any {
	return schemaOf(t, map[reflect.Type]bool{})
}

func schemaOf(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	switch t {
	case jsonType:
		return map[string]any{}
	case durationType:
		return map[string]any{"type": "integer", "description": "nanoseconds"}
	case bytesType:
		return map[string]any{"type": "string", "contentEncoding": "base64"}
	}
	if seen[t] {
		return map[string]any{}
	}
	if t.PkgPath() == rtPkg {
		name := t.Name()
		switch {
		case strings.HasPrefix(name, "Option["):
			return map[string]any{"anyOf": []any{schemaOf(t.Field(1).Type, seen), map[string]any{"type": "null"}}}
		case strings.HasPrefix(name, "Map["):
			k, v := t.Field(0).Type.Elem(), t.Field(1).Type.Elem()
			if k.Kind() == reflect.String {
				return map[string]any{"type": "object", "additionalProperties": schemaOf(v, seen)}
			}
			entry := map[string]any{"F0": schemaOf(k, seen), "F1": schemaOf(v, seen)}
			return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": entry, "required": []string{"F0", "F1"}, "additionalProperties": false}}
		case strings.HasPrefix(name, "Set["):
			return map[string]any{"type": "array", "items": schemaOf(t.Field(0).Type.Elem(), seen)}
		}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), seen)}
	case reflect.Struct:
		seen[t] = true
		defer delete(seen, t)
		if isEnum(t) {
			return enumSchema(t, seen)
		}
		return recordSchema(t, seen)
	}
	return map[string]any{}
}

func recordSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	props := map[string]any{}
	req := []string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		props[name] = schemaOf(f.Type, seen)
		req = append(req, name)
	}
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func enumSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	var alts []any
	for _, name := range reflect.New(t).Elem().Interface().(variantsLister).JipVariants() {
		idx, names, positional := payloadFields(t, name)
		var payload map[string]any
		switch {
		case len(idx) == 0:
			alts = append(alts, map[string]any{"const": name})
			continue
		case positional && len(idx) == 1:
			payload = schemaOf(t.Field(idx[0]).Type, seen)
		case positional:
			items := make([]any, len(idx))
			for i, fi := range idx {
				items[i] = schemaOf(t.Field(fi).Type, seen)
			}
			payload = map[string]any{"type": "array", "prefixItems": items, "minItems": len(idx), "maxItems": len(idx)}
		default:
			props := map[string]any{}
			for i, fi := range idx {
				props[names[i]] = schemaOf(t.Field(fi).Type, seen)
			}
			payload = map[string]any{"type": "object", "properties": props, "required": names, "additionalProperties": false}
		}
		alts = append(alts, map[string]any{"type": "object", "properties": map[string]any{name: payload}, "required": []string{name}, "additionalProperties": false})
	}
	if len(alts) == 1 {
		return alts[0].(map[string]any)
	}
	return map[string]any{"anyOf": alts}
}

// toolParamSchema is the schema of a tool parameter from its jippitese
// type as written; anything but a scalar is described, not typed.
func toolParamSchema(p ToolParam) map[string]any {
	s := map[string]any{"description": p.Type}
	if p.Doc != "" {
		s["description"] = p.Type + ": " + p.Doc
	}
	switch p.Type {
	case "Int":
		s["type"] = "integer"
	case "Float":
		s["type"] = "number"
	case "Bool":
		s["type"] = "boolean"
	case "Str":
		s["type"] = "string"
	}
	return s
}
