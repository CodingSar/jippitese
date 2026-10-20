package rt

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Json navigation. Every accessor answers None when the document is not of
// the asked shape, including when Raw is not valid JSON; an empty Raw is
// null.

// jsonTrim is the document without surrounding whitespace, "null" when empty.
func jsonTrim(j Json) []byte {
	b := bytes.TrimSpace(j.Raw)
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// jsonNumber is the number literal in j, when j is one.
func jsonNumber(j Json) (string, bool) {
	b := jsonTrim(j)
	if c := b[0]; c != '-' && (c < '0' || c > '9') || !json.Valid(b) {
		return "", false
	}
	return string(b), true
}

// JsonParse implements `std.json.parse`.
func JsonParse(ctx *Ctx, b []byte) Result[Json, JsonError] { return JsonDecode[Json](ctx, b) }

// JsonOf implements `std.json.of`.
func JsonOf[T any](ctx *Ctx, v T) Json { return Json{Raw: JsonEncode(ctx, v)} }

// JsonGet is the field `key` of an object; the last duplicate wins.
func JsonGet(ctx *Ctx, j Json, key string) Option[Json] {
	var m map[string]json.RawMessage
	if json.Unmarshal(jsonTrim(j), &m) != nil || m == nil {
		return Option_None[Json]()
	}
	v, ok := m[key]
	if !ok {
		return Option_None[Json]()
	}
	return Option_Some(Json{Raw: v})
}

// JsonAt is element i of an array.
func JsonAt(ctx *Ctx, j Json, i int64) Option[Json] {
	xs := JsonAsList(ctx, j)
	if xs.Tag == OptionTag_None || i < 0 || i >= int64(len(xs.Some_0)) {
		return Option_None[Json]()
	}
	return Option_Some(xs.Some_0[i])
}

// JsonAsStr is the text of a string.
func JsonAsStr(ctx *Ctx, j Json) Option[string] {
	b := jsonTrim(j)
	var s string
	if b[0] != '"' || json.Unmarshal(b, &s) != nil {
		return Option_None[string]()
	}
	return Option_Some(s)
}

// JsonAsInt is an integral number within Int range; `2.0` and `1e3` count.
func JsonAsInt(ctx *Ctx, j Json) Option[int64] {
	s, ok := jsonNumber(j)
	if !ok {
		return Option_None[int64]()
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return Option_Some(n)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != float64(int64(f)) || f < -9223372036854775808 || f >= 9223372036854775808 {
		return Option_None[int64]()
	}
	return Option_Some(int64(f))
}

// JsonAsFloat is a number that fits a Float.
func JsonAsFloat(ctx *Ctx, j Json) Option[float64] {
	s, ok := jsonNumber(j)
	if !ok {
		return Option_None[float64]()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Option_None[float64]()
	}
	return Option_Some(f)
}

// JsonAsBool is a boolean.
func JsonAsBool(ctx *Ctx, j Json) Option[bool] {
	switch string(jsonTrim(j)) {
	case "true":
		return Option_Some(true)
	case "false":
		return Option_Some(false)
	}
	return Option_None[bool]()
}

// JsonIsNull tests for null.
func JsonIsNull(ctx *Ctx, j Json) bool { return string(jsonTrim(j)) == "null" }

// JsonAsList is the elements of an array.
func JsonAsList(ctx *Ctx, j Json) Option[[]Json] {
	b := jsonTrim(j)
	var xs []json.RawMessage
	if b[0] != '[' || json.Unmarshal(b, &xs) != nil {
		return Option_None[[]Json]()
	}
	out := make([]Json, len(xs))
	for i, x := range xs {
		out[i] = Json{Raw: x}
	}
	return Option_Some(out)
}

// JsonAsMap is the fields of an object in source order; a duplicate key
// keeps its first position and its last value.
func JsonAsMap(ctx *Ctx, j Json) Option[Map[string, Json]] {
	b := jsonTrim(j)
	if b[0] != '{' || !json.Valid(b) {
		return Option_None[Map[string, Json]]()
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.Token() // {
	m := MapNew[string, Json](ctx)
	for dec.More() {
		k, _ := dec.Token()
		var v json.RawMessage
		dec.Decode(&v)
		m = m.insert(k.(string), Json{Raw: v})
	}
	return Option_Some(m)
}

// JsonKeys is the field names of an object in source order.
func JsonKeys(ctx *Ctx, j Json) Option[[]string] {
	m := JsonAsMap(ctx, j)
	if m.Tag == OptionTag_None {
		return Option_None[[]string]()
	}
	return Option_Some(MapKeys(ctx, m.Some_0))
}

// JsonToStr is the compact text; invalid documents are returned verbatim.
func JsonToStr(ctx *Ctx, j Json) string { return string(JsonToBytes(ctx, j)) }

// JsonToBytes is the compact encoding; invalid documents are returned verbatim.
func JsonToBytes(ctx *Ctx, j Json) []byte {
	var buf bytes.Buffer
	if json.Compact(&buf, jsonTrim(j)) != nil {
		return append([]byte(nil), j.Raw...)
	}
	return buf.Bytes()
}
