package rt

import (
	"strings"
	"testing"
	"time"
)

func TestLoadEffects(t *testing.T) {
	if got := LoadEffects(NewConfigLoaderFor("", nil), nil); Show(got) != Show(DefaultEffects()) {
		t.Fatalf("defaults drift: %s vs %s", Show(got), Show(DefaultEffects()))
	}
	blocks := map[string]map[string]string{"prod": {"effects.net.retry.max": "5"}}
	set := map[string]EffectSet{
		"net.retry.backoff": {Value: "const", Mode: CfgOverridable},
		"net.retry.base":    {Value: "200ms", Mode: CfgOverridable},
		"io.timeout":        {Value: "3s", Mode: CfgFixed, At: "app.jip:4:9"},
	}
	t.Setenv("JIP_EFFECTS__NET__TIMEOUT", "2s")
	l := NewConfigLoaderFor("prod", blocks)
	e := LoadEffects(l, set)
	if err := l.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if e.NetTimeout != 2*time.Second || e.NetRetry.Max != 5 || e.NetRetry.Backoff.Tag != BackoffTag_Fixed ||
		e.NetRetry.Backoff.Fixed_0 != 200*time.Millisecond || e.IoTimeout != 3*time.Second || e.ApproveTimeout != 24*time.Hour {
		t.Fatalf("effects: %+v", e)
	}
	c := NewCtx()
	if err := c.ApplyEffects(e); err != nil {
		t.Fatal(err)
	}
	if h := c.Http.(RealHttp); h.Timeout != 2*time.Second || h.Retry.Max != 5 {
		t.Fatalf("http: %+v", h)
	}

	t.Setenv("JIP_EFFECTS__IO__TIMEOUT", "1s")
	t.Setenv("JIP_EFFECTS__NET__RETRY__BACKOFF", "linear")
	l = NewConfigLoaderFor("", nil)
	LoadEffects(l, set)
	err := l.Finish(nil)
	if err == nil || !strings.Contains(err.Error(), "non-default field `effects.io.timeout` (fixed at app.jip:4:9)") ||
		!strings.Contains(err.Error(), "JIP_EFFECTS__NET__RETRY__BACKOFF=\"linear\" does not parse") {
		t.Fatalf("want fixed and backoff errors, got %v", err)
	}
}
