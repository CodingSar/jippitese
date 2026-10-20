package rt

import (
	"reflect"
	"testing"
)

func TestJsonNavigation(t *testing.T) {
	ctx := &Ctx{}
	j := func(s string) Json { return Json{Raw: []byte(s)} }
	doc := j(` {"z": 1, "a": [1, 2.0, 1e3, -0.5, 1e19, "3"], "z": 2, "s": "xé"} `)
	if got := JsonAsInt(ctx, JsonGet(ctx, doc, "z").Some_0); got != Option_Some[int64](2) {
		t.Errorf("last duplicate: %v", got)
	}
	if got := JsonKeys(ctx, doc); !reflect.DeepEqual(got.Some_0, []string{"z", "a", "s"}) {
		t.Errorf("keys: %v", got)
	}
	if got := JsonAsStr(ctx, JsonGet(ctx, doc, "s").Some_0); got != Option_Some("xé") {
		t.Errorf("as_str: %v", got)
	}
	a := JsonGet(ctx, doc, "a").Some_0
	ints := []Option[int64]{Option_Some[int64](1), Option_Some[int64](2), Option_Some[int64](1000), Option_None[int64](), Option_None[int64](), Option_None[int64]()}
	for i, want := range ints {
		if got := JsonAsInt(ctx, JsonAt(ctx, a, int64(i)).Some_0); got != want {
			t.Errorf("as_int %d: %v, want %v", i, got, want)
		}
	}
	if JsonAsFloat(ctx, JsonAt(ctx, a, 5).Some_0).Tag != OptionTag_None || JsonAsFloat(ctx, j("1e999")).Tag != OptionTag_None {
		t.Error("as_float accepted a string or an out-of-range number")
	}
	if JsonAt(ctx, a, 6).Tag != OptionTag_None || JsonAt(ctx, a, -1).Tag != OptionTag_None || JsonGet(ctx, a, "x").Tag != OptionTag_None {
		t.Error("out-of-shape access answered Some")
	}
	if !JsonIsNull(ctx, Json{}) || !JsonIsNull(ctx, j(" null ")) || JsonIsNull(ctx, j("0")) {
		t.Error("is_null")
	}
	if JsonAsBool(ctx, j("true")) != Option_Some(true) || JsonAsBool(ctx, j("1")).Tag != OptionTag_None {
		t.Error("as_bool")
	}
	bad := j(`{"a":`)
	if JsonGet(ctx, bad, "a").Tag != OptionTag_None || JsonAsMap(ctx, bad).Tag != OptionTag_None || JsonToStr(ctx, bad) != `{"a":` {
		t.Error("invalid document")
	}
	if got := JsonToStr(ctx, j(` [1, {"b" : 2}] `)); got != `[1,{"b":2}]` {
		t.Errorf("to_str: %q", got)
	}
	if got := JsonToStr(ctx, JsonOf(ctx, map[string]int{"k": 1})); got != `{"k":1}` {
		t.Errorf("of: %q", got)
	}
	if r := JsonParse(ctx, []byte(`[1,`)); r.Tag != ResultTag_Err {
		t.Error("parse accepted invalid input")
	}
}
