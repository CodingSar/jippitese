package rt

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config modes: how a leaf may be set from the overlay.
const (
	// CfgRequired has no literal: the overlay must supply it.
	CfgRequired = "required"
	// CfgFixed has a literal without `default`: the overlay may not touch it.
	CfgFixed = "fixed"
	// CfgOverridable has a `default` literal.
	CfgOverridable = "overridable"
)

// ConfigLoader applies the overlay of spec 10: app body < env block <
// JIP_<PATH> variables. The generated appcfg.Load drives it.
type ConfigLoader struct {
	env    string
	blocks map[string]map[string]string
	block  map[string]string
	errs   []string
	seen   map[string]bool
}

// NewConfigLoader starts a load with the app's env blocks, keyed by env
// name then by field path.
func NewConfigLoader(envBlocks map[string]map[string]string) *ConfigLoader {
	return NewConfigLoaderFor(os.Getenv("JIP_ENV"), envBlocks)
}

// NewConfigLoaderFor starts a load under a named env block; scenarios use
// it with "test" whatever JIP_ENV says (spec 12 rule 9). A missing "test"
// block is not an error.
func NewConfigLoaderFor(env string, envBlocks map[string]map[string]string) *ConfigLoader {
	l := &ConfigLoader{blocks: envBlocks, seen: map[string]bool{"JIP_ENV": true}}
	l.env = env
	if l.env == "test" {
		l.block = envBlocks["test"]
		return l
	}
	if l.env != "" {
		b, ok := envBlocks[l.env]
		if !ok {
			l.errs = append(l.errs, fmt.Sprintf("JIP_ENV=%s names no env block", l.env))
		}
		l.block = b
	}
	return l
}

// EnvKey is the variable that sets a field path.
func EnvKey(path string) string {
	return "JIP_" + strings.ToUpper(strings.ReplaceAll(path, ".", "__"))
}

func (l *ConfigLoader) errf(format string, args ...any) {
	l.errs = append(l.errs, fmt.Sprintf(format, args...))
}

// lookup finds the overlay value of a path: the variable first, then the
// env block.
func (l *ConfigLoader) lookup(path string) (value, source string, ok bool) {
	key := EnvKey(path)
	if v, found := os.LookupEnv(key); found {
		l.seen[key] = true
		return v, key, true
	}
	if l.block != nil {
		if v, found := l.block[path]; found {
			return v, "env " + l.env, true
		}
	}
	return "", "", false
}

// raw resolves a leaf's string value under its mode. at is the literal's
// position for the fixed-field message.
func (l *ConfigLoader) raw(path, def, mode, at string) (string, bool) {
	v, src, ok := l.lookup(path)
	switch mode {
	case CfgFixed:
		if ok {
			if at != "" {
				l.errf("%s sets a non-default field `%s` (fixed at %s)", src, path, at)
			} else {
				l.errf("%s sets a non-default field `%s`", src, path)
			}
			return def, false
		}
		return def, true
	case CfgRequired:
		if !ok {
			l.errf("%s is unset and `%s` has no literal", EnvKey(path), path)
			return "", false
		}
		return v, true
	default:
		if ok {
			return v, true
		}
		return def, true
	}
}

func (l *ConfigLoader) parseErr(path, v, typ string) {
	l.errf("%s=%q does not parse as %s", EnvKey(path), v, typ)
}

// Str reads a Str or Url leaf.
func (l *ConfigLoader) Str(path, def, mode, at string) string {
	v, _ := l.raw(path, def, mode, at)
	return v
}

// Int reads an Int leaf.
func (l *ConfigLoader) Int(path string, def int64, mode, at string) int64 {
	v, ok := l.raw(path, strconv.FormatInt(def, 10), mode, at)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(v, "_", ""), 10, 64)
	if err != nil {
		l.parseErr(path, v, "Int")
		return def
	}
	return n
}

// Float reads a Float leaf.
func (l *ConfigLoader) Float(path string, def float64, mode, at string) float64 {
	v, ok := l.raw(path, strconv.FormatFloat(def, 'g', -1, 64), mode, at)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		l.parseErr(path, v, "Float")
		return def
	}
	return f
}

// Bool reads a Bool leaf.
func (l *ConfigLoader) Bool(path string, def bool, mode, at string) bool {
	v, ok := l.raw(path, strconv.FormatBool(def), mode, at)
	if !ok {
		return def
	}
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	l.parseErr(path, v, "Bool")
	return def
}

// ParseDuration parses a jippitese duration literal (`250ms`, `5s`, `2m`,
// `1h`, `24h`, `1d`).
func ParseDuration(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	i := 0
	for i < len(v) && (v[i] >= '0' && v[i] <= '9' || v[i] == '_') {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(v[:i], "_", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	switch v[i:] {
	case "ns":
		return time.Duration(n), true
	case "us":
		return time.Duration(n) * time.Microsecond, true
	case "ms":
		return time.Duration(n) * time.Millisecond, true
	case "s":
		return time.Duration(n) * time.Second, true
	case "m":
		return time.Duration(n) * time.Minute, true
	case "h":
		return time.Duration(n) * time.Hour, true
	case "d":
		return time.Duration(n) * 24 * time.Hour, true
	}
	return 0, false
}

// FormatDuration prints a duration as the shortest literal that
// round-trips through ParseDuration.
func FormatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	case d%time.Millisecond == 0:
		return fmt.Sprintf("%dms", d/time.Millisecond)
	case d%time.Microsecond == 0:
		return fmt.Sprintf("%dus", d/time.Microsecond)
	}
	return fmt.Sprintf("%dns", int64(d))
}

// Duration reads a Duration leaf.
func (l *ConfigLoader) Duration(path string, def time.Duration, mode, at string) time.Duration {
	v, ok := l.raw(path, FormatDuration(def), mode, at)
	if !ok {
		return def
	}
	d, good := ParseDuration(v)
	if !good {
		l.parseErr(path, v, "Duration")
		return def
	}
	return d
}

// Secret reads `secrets.<name>`; the value comes only from the overlay.
func (l *ConfigLoader) Secret(name string) string {
	return l.Str("secrets."+name, "", CfgRequired, "")
}

// RunStr reads a run-line field with its default.
func (l *ConfigLoader) RunStr(name, field, def string) string {
	return l.Str("run."+name+"."+field, def, CfgOverridable, "")
}

// RunInt reads an integer run-line field with its default.
func (l *ConfigLoader) RunInt(name, field string, def int64) int64 {
	return l.Int("run."+name+"."+field, def, CfgOverridable, "")
}

// RunDuration reads a duration run-line field with its default.
func (l *ConfigLoader) RunDuration(name, field string, def time.Duration) time.Duration {
	return l.Duration("run."+name+"."+field, def, CfgOverridable, "")
}

// Accept marks variables read elsewhere (model keys) as fields, so Finish
// does not report them.
func (l *ConfigLoader) Accept(keys ...string) {
	for _, k := range keys {
		l.seen[k] = true
	}
}

// exemptVars are JIP_* variables that never name a field.
var exemptVars = map[string]bool{
	"JIP_ENV": true, "JIP_GO": true, "JIP_SCENARIO_FILTER": true, "JIP_SEED": true,
	"JIP_RUNS": true, "JIP_SLOW": true, "JIP_SEED_DIR": true, "JIP_APPROVE_ADDR": true,
}

// Finish reports unknown JIP_* variables and returns the joined errors.
func (l *ConfigLoader) Finish(known []string) error {
	var unknown []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, "JIP_") || exemptVars[k] || l.seen[k] {
			continue
		}
		unknown = append(unknown, k)
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		if best := closest(k, known); best != "" {
			l.errf("%s matches no field (did you mean %s?)", k, best)
		} else {
			l.errf("%s matches no field", k)
		}
	}
	if len(l.errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(l.errs, "; "))
}

// closest picks the known key with the smallest edit distance, within a
// third of the key's length.
func closest(k string, known []string) string {
	best, bestD := "", len(k)/3+1
	for _, c := range known {
		if d := editDistance(k, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
