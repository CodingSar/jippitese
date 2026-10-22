package rt

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LLM (large language model) usage metering: every provider reply is
// priced into Money minor units, which an Agent's `max_cost` and
// `effects.llm.budget` bind on (spec 10 rule 3).

// Price is what a model costs, in Money minor units (US cents) per million
// input and output tokens.
type Price struct{ In, Out int64 }

// ModelPrices are the built-in list prices by model name. A model not
// listed costs nothing unless JIP_MODELS__<ROLE>__PRICE sets its price.
var ModelPrices = map[string]Price{
	"claude-fable-5-1":  {1000, 5000},
	"claude-fable-5":    {1000, 5000},
	"claude-opus-5-5":   {400, 2000},
	"claude-opus-5":     {500, 2500},
	"claude-sonnet-5-5": {200, 1000},
	"claude-sonnet-5":   {200, 1000},
	"claude-haiku-5-5":  {10, 50},
}

// ParsePrice parses `<in>/<out>`, minor units per million tokens.
func ParsePrice(s string) (Price, error) {
	in, out, ok := strings.Cut(s, "/")
	if ok {
		i, err1 := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(in, "_", "")), 10, 64)
		o, err2 := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(out, "_", "")), 10, 64)
		if err1 == nil && err2 == nil && i >= 0 && o >= 0 {
			return Price{i, o}, nil
		}
	}
	return Price{}, fmt.Errorf("%q is not `<in>/<out>`", s)
}

// priceFor is the price of role's model: JIP_MODELS__<ROLE>__PRICE, else
// the list price, else zero (ollama, custom, unlisted models).
func priceFor(role, provider, model string) Price {
	if v, ok := os.LookupEnv("JIP_MODELS__" + strings.ToUpper(role) + "__PRICE"); ok {
		if p, err := ParsePrice(v); err == nil {
			return p
		}
	}
	if provider == "ollama" {
		return Price{}
	}
	return ModelPrices[model]
}

// CheckPrices reports a JIP_MODELS__<ROLE>__PRICE variable among keys that
// does not parse; startup aborts on it like on any other field.
func (l *ConfigLoader) CheckPrices(keys ...string) {
	for _, k := range keys {
		if !strings.HasSuffix(k, "__PRICE") {
			continue
		}
		if v, ok := os.LookupEnv(k); ok {
			if _, err := ParsePrice(v); err != nil {
				l.errf("%s=%q does not parse as `<in>/<out>` (minor units per million tokens)", k, v)
			}
		}
	}
}

// ParseLlmBudget parses an `llm.budget` value: `<n> tokens`, or Money as
// `Money(<n>)` or a bare `<n>` (minor units). "" is no budget.
func ParseLlmBudget(s string) (tokens, cost int64, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	num := func(v string) (int64, error) {
		n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(v), "_", ""), 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not `<n> tokens` or `Money(<n>)`", s)
		}
		return n, nil
	}
	if v, ok := strings.CutSuffix(s, "tokens"); ok {
		tokens, err = num(v)
		return tokens, 0, err
	}
	if v, ok := strings.CutPrefix(s, "Money("); ok && strings.HasSuffix(v, ")") {
		s = strings.TrimSuffix(v, ")")
	}
	cost, err = num(s)
	return 0, cost, err
}

// ErrLlmBudget is a provider call refused because effects.llm.budget is
// spent.
var ErrLlmBudget = errors.New("effects.llm.budget is spent")

// meter is the program's LLM usage.
type meter struct {
	tokens int64 // input + output
	micro  int64 // cost in millionths of a minor unit
}

// llmSpent reports whether effects.llm.budget is spent.
func (c *Ctx) llmSpent() bool {
	tokens, cost := c.Effects.LlmBudgetTokens, c.Effects.LlmBudgetCost
	if tokens == 0 && cost == 0 {
		return false
	}
	c.sh.mu.Lock()
	defer c.sh.mu.Unlock()
	return tokens > 0 && c.sh.meter.tokens >= tokens || cost > 0 && c.sh.meter.micro >= cost*1_000_000
}

// charge records one provider reply of in input and out output tokens at
// price p and returns its cost in minor units, rounded up.
func (c *Ctx) charge(p Price, in, out int64) int64 {
	micro := in*p.In + out*p.Out
	c.sh.mu.Lock()
	c.sh.meter.tokens += in + out
	c.sh.meter.micro += micro
	c.sh.mu.Unlock()
	return (micro + 999_999) / 1_000_000
}

// LlmUsage is the program's LLM usage so far: tokens, and cost in minor
// units rounded up.
func (c *Ctx) LlmUsage() (tokens, cost int64) {
	c.sh.mu.Lock()
	defer c.sh.mu.Unlock()
	return c.sh.meter.tokens, (c.sh.meter.micro + 999_999) / 1_000_000
}
