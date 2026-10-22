package rt

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Effects is the resolved `effects {}` of app.jip (spec 10 rule 3). v1
// wires net.timeout and net.retry into ctx.Http, llm.timeout into the
// model clients and approve.* into the approval channel; the other leaves
// are read and validated but not enforced.
type Effects struct {
	NetTimeout      time.Duration
	NetRetry        Retry
	NetRatePerSec   float64 // 0 = no limit
	LlmTimeout      time.Duration
	LlmBudget       string // "" = no budget
	LlmBudgetTokens int64  // from LlmBudget; 0 = none
	LlmBudgetCost   int64  // from LlmBudget, minor units; 0 = none
	LlmRatePerSec   float64
	IoTimeout       time.Duration
	DecideTimeout   time.Duration
	ApproveChannel  string // module path; "" = none
	ApproveTimeout  time.Duration
}

// EffectSet is one leaf the app body sets: its literal, its config mode and
// the literal's position.
type EffectSet struct {
	Value, Mode, At string
}

// effectDefaults are the spec defaults of the leaves below `effects.`.
var effectDefaults = map[string]string{
	"net.timeout":             "5s",
	"net.retry.max":           "3",
	"net.retry.backoff":       "exp",
	"net.retry.base":          "100ms",
	"llm.timeout":             "60s",
	"io.timeout":              "10s",
	"decide.timeout":          "1s",
	"approve.default_timeout": "24h",
}

// DefaultEffects is the effects of an app that sets none, before the
// overlay; it matches effectDefaults.
func DefaultEffects() Effects {
	return Effects{
		NetTimeout:     5 * time.Second,
		NetRetry:       Retry{Max: 3, Backoff: Backoff_Exp(100 * time.Millisecond)},
		LlmTimeout:     60 * time.Second,
		IoTimeout:      10 * time.Second,
		DecideTimeout:  time.Second,
		ApproveTimeout: 24 * time.Hour,
	}
}

// LoadEffects resolves every `effects` leaf: the app's literal (or the spec
// default, overridable) under the overlay.
func LoadEffects(l *ConfigLoader, set map[string]EffectSet) Effects {
	leaf := func(path string) (def, mode, at string) {
		if s, ok := set[path]; ok {
			return s.Value, s.Mode, s.At
		}
		return effectDefaults[path], CfgOverridable, ""
	}
	dur := func(path string) time.Duration {
		def, mode, at := leaf(path)
		d, _ := ParseDuration(def)
		return l.Duration("effects."+path, d, mode, at)
	}
	num := func(path string) float64 {
		def, mode, at := leaf(path)
		f, _ := strconv.ParseFloat(def, 64)
		return l.Float("effects."+path, f, mode, at)
	}
	str := func(path string) string {
		def, mode, at := leaf(path)
		return l.Str("effects."+path, def, mode, at)
	}
	var e Effects
	e.NetTimeout = dur("net.timeout")
	def, mode, at := leaf("net.retry.max")
	n, _ := strconv.ParseInt(def, 10, 64)
	e.NetRetry.Max = l.Int("effects.net.retry.max", n, mode, at)
	base := dur("net.retry.base")
	switch kind := str("net.retry.backoff"); kind {
	case "exp":
		e.NetRetry.Backoff = Backoff_Exp(base)
	case "const":
		e.NetRetry.Backoff = Backoff_Fixed(base)
	default:
		l.parseErr("effects.net.retry.backoff", kind, "`exp` or `const`")
		e.NetRetry.Backoff = Backoff_Exp(base)
	}
	e.NetRatePerSec = num("net.ratelimit.per_second")
	e.LlmTimeout = dur("llm.timeout")
	e.LlmBudget = str("llm.budget")
	if t, m, err := ParseLlmBudget(e.LlmBudget); err != nil {
		l.parseErr("effects.llm.budget", e.LlmBudget, "`<n> tokens` or `Money(<n>)`")
	} else {
		e.LlmBudgetTokens, e.LlmBudgetCost = t, m
	}
	e.LlmRatePerSec = num("llm.ratelimit.per_second")
	e.IoTimeout = dur("io.timeout")
	e.DecideTimeout = dur("decide.timeout")
	e.ApproveChannel = str("approve.channel")
	e.ApproveTimeout = dur("approve.default_timeout")
	return e
}

// ApplyEffects installs the resolved effects: the real HTTP client takes
// net.timeout and net.retry, and approve.channel starts its endpoint or
// installs the user channel registered under its name.
func (c *Ctx) ApplyEffects(e Effects) error {
	c.Effects = e
	c.Http = RealHttp{Timeout: e.NetTimeout, Retry: e.NetRetry}
	switch e.ApproveChannel {
	case "":
	case ApproveHttpChannel:
		addr, err := approveAddr()
		if err != nil {
			return err
		}
		a, err := NewApproveHttp(addr)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "jip: approvals at http://%s/reviews\n", a.Addr)
		c.Approve = a
	default:
		c.sh.mu.Lock()
		f := c.sh.channels[e.ApproveChannel]
		c.sh.mu.Unlock()
		if f == nil {
			return fmt.Errorf("effects.approve.channel `%s` is not a channel of this build (`%s` or a module with `pub fn ask(id: Str, review: Review) -> ReviewAnswer`)", e.ApproveChannel, ApproveHttpChannel)
		}
		c.Approve = f
	}
	return nil
}
