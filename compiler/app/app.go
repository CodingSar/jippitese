// Package app models the `app { … }` block of app.jip (spec 10) for the
// checker, the emitter and the CLI.
package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// ConfigField is one `config { name: Type }` or `name: default <lit>` entry.
type ConfigField struct {
	Pos       ast.Pos
	Name      string
	Type      ast.TypeExpr // nil when only a default literal was given
	Default   ast.Expr     // nil when no default literal
	IsDefault bool
}

// Secret is one `secrets { name: secret<T> }` entry.
type Secret struct {
	Pos  ast.Pos
	Name string
	Type ast.TypeExpr
}

// Dep is one `deps { alias = git(url, commit: "…", hash: "…") }` or
// `alias = gomod("module/path", commit: "…", hash: "…")` entry.
type Dep struct {
	Pos    ast.Pos
	Alias  string
	Kind   string // "git" or "gomod"
	URL    string // what `jip fetch` clones; `https://<GoPath>` for gomod
	GoPath string // the Go module path of a gomod entry
	Commit string
	Hash   string
}

// Model is one `models { role = provider("name") }` entry; a
// `custom(url, model)` entry also sets URL.
type Model struct {
	Pos      ast.Pos
	Role     string
	Provider string
	Name     string
	URL      string
}

// Providers are the names `models {}` accepts (spec 10 rule 3).
var Providers = []string{"openai", "anthropic", "jev", "ollama", "custom"}

// Run is one `run Name { … }` line.
type Run struct {
	Pos    ast.Pos
	Name   string
	Fields map[string]*ast.AppEntry
}

// Topic is one `topics { name: Topic<T> }` entry.
type Topic struct {
	Pos  ast.Pos
	Name string
	Type ast.TypeExpr // the message type T
}

// Env is one `env <name> { path = value }` overlay.
type Env struct {
	Pos     ast.Pos
	Name    string
	Entries []*ast.AppEntry
}

// App is the parsed app.jip.
type App struct {
	Pos     ast.Pos
	Name    string
	Config  []*ConfigField
	Secrets []*Secret
	Deps    []*Dep
	Models  []*Model
	Externs struct {
		Allow []string
		Max   int
		Set   bool
	}
	Contracts string // "all", "pre", "none" ("" = unset → all)
	Workflow  struct {
		Mode  string // "embedded"
		Store string // journal directory
	}
	Runs   []*Run
	Topics []*Topic
	Envs   []*Env
	// Effects holds the `effects {}` leaves set in the app body, by path
	// below `effects.` ("net.retry.max").
	Effects map[string]*EffectLeaf
	Entries []*ast.AppEntry // every top-level entry, in order
	Block   *ast.AppBlock
}

// Parse builds the model from a parsed app.jip.
func Parse(f *ast.File) (*App, error) {
	if f.App == nil {
		return nil, errAt(ast.Pos{File: f.Path, Line: 1, Col: 1}, "no `app { … }` block")
	}
	b := f.App
	a := &App{Pos: b.Pos, Block: b, Entries: b.Entries, Effects: map[string]*EffectLeaf{}}
	a.Workflow.Mode, a.Workflow.Store = "embedded", DefaultWorkflowStore
	for _, e := range b.Entries {
		switch {
		case e.Key == "name":
			s, ok := litStr(e.Value)
			if !ok {
				return nil, errAt(e.Pos, "`name` must be a string literal")
			}
			a.Name = s
		case e.Key == "config":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`config` must be a block")
			}
			for _, c := range blk.Entries {
				cf := &ConfigField{Pos: c.Pos, Name: c.Key, IsDefault: c.Default}
				switch v := c.Value.(type) {
				case *ast.AppType:
					cf.Type = v.T
				case *ast.AppLit:
					cf.Default = v.X
				default:
					return nil, errAt(c.Pos, "config field `%s` needs a type or a default literal", c.Key)
				}
				a.Config = append(a.Config, cf)
			}
		case e.Key == "secrets":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`secrets` must be a block")
			}
			for _, c := range blk.Entries {
				t, ok := c.Value.(*ast.AppType)
				if !ok {
					return nil, errAt(c.Pos, "secret `%s` needs `secret<T>`", c.Key)
				}
				tp, ok := t.T.(*ast.TypePath)
				if !ok || len(tp.Path) != 1 || tp.Path[0] != "secret" || len(tp.Args) != 1 {
					return nil, errAt(c.Pos, "secret `%s` needs `secret<T>`", c.Key)
				}
				a.Secrets = append(a.Secrets, &Secret{Pos: c.Pos, Name: c.Key, Type: tp.Args[0]})
			}
		case e.Key == "deps":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`deps` must be a block")
			}
			for _, c := range blk.Entries {
				d := &Dep{Pos: c.Pos, Alias: c.Key}
				if call, ok := c.Value.(*ast.AppCall); ok && (call.Name == "git" || call.Name == "gomod") {
					d.Kind = call.Name
					if len(call.Args) > 0 {
						d.URL, _ = litStr(call.Args[0])
					}
					if d.Kind == "gomod" {
						d.GoPath, d.URL = d.URL, "https://"+d.URL
					}
					for _, kw := range call.Kw {
						switch kw.Key {
						case "commit":
							d.Commit, _ = litStr(kw.Value)
						case "hash":
							d.Hash, _ = litStr(kw.Value)
						}
					}
				}
				a.Deps = append(a.Deps, d)
			}
		case e.Key == "models":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`models` must be a block")
			}
			for _, c := range blk.Entries {
				m := &Model{Pos: c.Pos, Role: c.Key}
				call, ok := c.Value.(*ast.AppCall)
				if !ok || !slices.Contains(Providers, call.Name) {
					return nil, errAt(c.Pos, "model `%s` must be one of %s", c.Key, strings.Join(Providers, ", "))
				}
				m.Provider = call.Name
				var args []string
				for _, x := range call.Args {
					s, ok := litStr(x)
					if !ok {
						return nil, errAt(c.Pos, "model `%s` takes string literals", c.Key)
					}
					args = append(args, s)
				}
				switch {
				case m.Provider == "custom" && len(args) == 2:
					m.URL, m.Name = args[0], args[1]
				case m.Provider == "custom":
					return nil, errAt(c.Pos, "model `%s` must be `custom(url, model)`", c.Key)
				case len(args) == 1:
					m.Name = args[0]
				default:
					return nil, errAt(c.Pos, "model `%s` must be `%s(model)`", c.Key, m.Provider)
				}
				a.Models = append(a.Models, m)
			}
		case e.Key == "externs":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`externs` must be a block")
			}
			a.Externs.Set = true
			for _, c := range blk.Entries {
				switch c.Key {
				case "allow":
					l, ok := c.Value.(*ast.AppList)
					if !ok {
						return nil, errAt(c.Pos, "`externs.allow` must be a list of module paths")
					}
					for _, el := range l.Elems {
						p, ok := el.(*ast.AppPath)
						if !ok {
							return nil, errAt(c.Pos, "`externs.allow` must be a list of module paths")
						}
						a.Externs.Allow = append(a.Externs.Allow, strings.Join(p.Path, "."))
					}
				case "max":
					n, ok := litInt(c.Value)
					if !ok {
						return nil, errAt(c.Pos, "`externs.max` must be an integer")
					}
					a.Externs.Max = int(n)
				}
			}
		case e.Key == "contracts":
			p, ok := e.Value.(*ast.AppPath)
			if !ok || len(p.Path) != 1 {
				return nil, errAt(e.Pos, "`contracts` must be `all`, `pre` or `none`")
			}
			switch p.Path[0] {
			case "all", "pre", "none":
				a.Contracts = p.Path[0]
			default:
				return nil, errAt(e.Pos, "`contracts` must be `all`, `pre` or `none`")
			}
		case e.Key == "workflow":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`workflow` must be a block")
			}
			for _, c := range blk.Entries {
				switch c.Key {
				case "mode":
					p, ok := c.Value.(*ast.AppPath)
					if !ok || len(p.Path) != 1 || !slices.Contains(WorkflowModes, p.Path[0]) {
						return nil, errAt(c.Pos, "`workflow.mode` must be `embedded`, `temporal` or `restate`")
					}
					if !slices.Contains(WorkflowAdapters, p.Path[0]) {
						return nil, errAt(c.Pos, "`workflow.mode: %s` has no runtime adapter yet; only `embedded`", p.Path[0])
					}
					a.Workflow.Mode = p.Path[0]
				case "store":
					s, ok := litStr(c.Value)
					if !ok {
						return nil, errAt(c.Pos, "`workflow.store` must be a string literal")
					}
					a.Workflow.Store = s
				default:
					return nil, errAt(c.Pos, "unknown `workflow` field `%s`", c.Key)
				}
			}
		case e.Key == "effects":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`effects` must be a block")
			}
			if err := a.effects("", blk.Entries, e.Default); err != nil {
				return nil, err
			}
		case e.Key == "topics":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, errAt(e.Pos, "`topics` must be a block")
			}
			for _, c := range blk.Entries {
				t, ok := c.Value.(*ast.AppType)
				if !ok {
					return nil, errAt(c.Pos, "topic `%s` needs `Topic<T>`", c.Key)
				}
				tp, ok := t.T.(*ast.TypePath)
				if !ok || len(tp.Path) != 1 || tp.Path[0] != "Topic" || len(tp.Args) != 1 {
					return nil, errAt(c.Pos, "topic `%s` needs `Topic<T>`", c.Key)
				}
				a.Topics = append(a.Topics, &Topic{Pos: c.Pos, Name: c.Key, Type: tp.Args[0]})
			}
		case strings.HasPrefix(e.Key, "run "):
			blk, _ := e.Value.(*ast.AppBlock)
			r := &Run{Pos: e.Pos, Name: strings.TrimPrefix(e.Key, "run "), Fields: map[string]*ast.AppEntry{}}
			if blk != nil {
				for _, c := range blk.Entries {
					r.Fields[c.Key] = c
				}
			}
			a.Runs = append(a.Runs, r)
		case strings.HasPrefix(e.Key, "env "):
			blk, _ := e.Value.(*ast.AppBlock)
			env := &Env{Pos: e.Pos, Name: strings.TrimPrefix(e.Key, "env ")}
			if blk != nil {
				env.Entries = blk.Entries
			}
			a.Envs = append(a.Envs, env)
		}
	}
	if a.Name == "" {
		return nil, errAt(b.Pos, "app.jip has no `name`")
	}
	if err := a.checkEnvs(); err != nil {
		return nil, err
	}
	return a, nil
}

// checkEnvs rejects an env block that sets a leaf the app body fixes
// (E0303): a config or effects leaf with a literal and no `default`.
func (a *App) checkEnvs() error {
	fixed := map[string]ast.Pos{}
	for _, c := range a.Config {
		if c.Default != nil && !c.IsDefault {
			fixed["config."+c.Name] = c.Default.ExprPos()
		}
	}
	for path, lf := range a.Effects {
		if !lf.IsDefault {
			fixed["effects."+path] = lf.Pos
		}
	}
	for _, env := range a.Envs {
		for _, en := range env.Entries {
			if at, ok := fixed[en.Key]; ok {
				return &Error{Pos: en.Pos, Code: "E0303", Msg: fmt.Sprintf("overlay sets a field that is not `default`: `%s` is fixed at %s", en.Key, at)}
			}
		}
	}
	return nil
}

// Error is an invalid app.jip (E0301).
type Error struct {
	Pos  ast.Pos
	Code string // "" is E0301
	Msg  string
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

func errAt(pos ast.Pos, format string, args ...any) error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// EffectLeaf is one leaf of `effects {}`; a `default` on an enclosing
// record makes every leaf in it overridable.
type EffectLeaf struct {
	Pos       ast.Pos
	Path      string
	Value     ast.AppValue
	IsDefault bool
}

// EffectField is one leaf of the closed `effects {}` schema (spec 10 rule 3).
type EffectField struct {
	Path    string // below `effects.`
	Kind    string // "Duration", "Int", "Float", "Backoff", "Module", "Budget"
	Default string // the literal used when the app sets none; "" = unset
}

// EffectSchema lists the `effects {}` leaves with their spec defaults.
var EffectSchema = []EffectField{
	{"net.timeout", "Duration", "5s"},
	{"net.retry.max", "Int", "3"},
	{"net.retry.backoff", "Backoff", "exp"},
	{"net.retry.base", "Duration", "100ms"},
	{"net.ratelimit.per_second", "Float", ""},
	{"llm.timeout", "Duration", "60s"},
	{"llm.budget", "Budget", ""},
	{"llm.ratelimit.per_second", "Float", ""},
	{"io.timeout", "Duration", "10s"},
	{"decide.timeout", "Duration", "1s"},
	{"approve.channel", "Module", ""},
	{"approve.default_timeout", "Duration", "24h"},
}

func effectField(path string) *EffectField {
	for i := range EffectSchema {
		if EffectSchema[i].Path == path {
			return &EffectSchema[i]
		}
	}
	return nil
}

func effectRecord(path string) bool {
	for _, f := range EffectSchema {
		if strings.HasPrefix(f.Path, path+".") {
			return true
		}
	}
	return false
}

// effects flattens the entries of an `effects` record below prefix.
func (a *App) effects(prefix string, entries []*ast.AppEntry, isDefault bool) error {
	for _, c := range entries {
		path := prefix + c.Key
		def := isDefault || c.Default
		if blk, ok := c.Value.(*ast.AppBlock); ok {
			if !effectRecord(path) {
				return errAt(c.Pos, "unknown `effects` record `%s`", path)
			}
			if err := a.effects(path+".", blk.Entries, def); err != nil {
				return err
			}
			continue
		}
		fd := effectField(path)
		if fd == nil {
			if effectRecord(path) {
				return errAt(c.Pos, "`effects.%s` must be a record", path)
			}
			return errAt(c.Pos, "unknown `effects` field `%s`", path)
		}
		if _, dup := a.Effects[path]; dup {
			return errAt(c.Pos, "`effects.%s` is set twice", path)
		}
		if !effectValueOk(fd.Kind, c.Value) {
			return errAt(c.Pos, "`effects.%s` must be %s", path, effectKindText[fd.Kind])
		}
		// v1 has no user channels: a module implementing
		// std.approve.Channel waits on a design for module-valued config.
		if p, ok := c.Value.(*ast.AppPath); ok && path == "approve.channel" && strings.Join(p.Path, ".") != "std.approve.http" {
			return errAt(c.Pos, "`effects.approve.channel`: v1 ships only `std.approve.http`")
		}
		a.Effects[path] = &EffectLeaf{Pos: c.Pos, Path: path, Value: c.Value, IsDefault: def}
	}
	return nil
}

var effectKindText = map[string]string{
	"Duration": "a duration literal",
	"Int":      "an integer",
	"Float":    "a number",
	"Backoff":  "`exp` or `const`",
	"Module":   "a module path",
	"Budget":   "`<n> tokens` or `Money(<n>)`",
}

// effectValueOk reports whether an app value fits a leaf kind.
func effectValueOk(kind string, v ast.AppValue) bool {
	switch kind {
	case "Duration":
		l, ok := v.(*ast.AppLit)
		if !ok {
			return false
		}
		_, ok = l.X.(*ast.DurationLit)
		return ok
	case "Int":
		_, ok := litInt(v)
		return ok
	case "Float":
		l, ok := v.(*ast.AppLit)
		if !ok {
			return false
		}
		switch l.X.(type) {
		case *ast.FloatLit, *ast.IntLit:
			return true
		}
		return false
	case "Backoff":
		p, ok := v.(*ast.AppPath)
		return ok && len(p.Path) == 1 && (p.Path[0] == "exp" || p.Path[0] == "const")
	case "Module":
		_, ok := v.(*ast.AppPath)
		return ok
	case "Budget":
		l, ok := v.(*ast.AppLit)
		if !ok {
			return false
		}
		if _, ok := l.X.(*ast.IntLit); ok {
			return l.Unit == "tokens"
		}
		c, ok := l.X.(*ast.CallExpr)
		if !ok || len(c.Args) != 1 {
			return false
		}
		id, ok := c.Fn.(*ast.Ident)
		return ok && id.Name == "Money"
	}
	return false
}

// WorkflowModes are the values of `workflow { mode }` (07 r16).
var WorkflowModes = []string{"embedded", "temporal", "restate"}

// WorkflowAdapters are the modes with a runtime adapter in rt
// (rt.WorkflowRuntimes); a test keeps the two in step.
var WorkflowAdapters = []string{"embedded"}

// DefaultWorkflowStore is the journal directory when `workflow { store }`
// is absent: one JSON-lines file per run (spec 07 rule 16).
const DefaultWorkflowStore = ".jip/workflow"

// Run returns the run line for an impl name, or nil.
func (a *App) RunFor(name string) *Run {
	for _, r := range a.Runs {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// Dep returns the dependency with the alias, or nil.
func (a *App) DepFor(alias string) *Dep {
	for _, d := range a.Deps {
		if d.Alias == alias {
			return d
		}
	}
	return nil
}

// ModelFor is the `models {}` entry of role, nil when it is not declared.
func (a *App) ModelFor(role string) *Model {
	for _, m := range a.Models {
		if m.Role == role {
			return m
		}
	}
	return nil
}

// GoModFor is the gomod entry whose module holds the Go import path, if any.
// The longest module path wins, as in Go.
func (a *App) GoModFor(path string) *Dep {
	var best *Dep
	for _, d := range a.Deps {
		if d.Kind == "gomod" && d.GoPath != "" && (path == d.GoPath || strings.HasPrefix(path, d.GoPath+"/")) {
			if best == nil || len(d.GoPath) > len(best.GoPath) {
				best = d
			}
		}
	}
	return best
}

// ExternAllowed reports whether a module may declare `extern fn`.
func (a *App) ExternAllowed(module string) bool {
	for _, m := range a.Externs.Allow {
		if m == module {
			return true
		}
	}
	return false
}

// ContractLevel returns the effective `contracts:` level.
func (a *App) ContractLevel() string {
	if a.Contracts == "" {
		return "all"
	}
	return a.Contracts
}

func litStr(v ast.AppValue) (string, bool) {
	l, ok := v.(*ast.AppLit)
	if !ok {
		return "", false
	}
	s, ok := l.X.(*ast.StrLit)
	if !ok {
		return "", false
	}
	var sb strings.Builder
	for _, p := range s.Parts {
		if p.Expr != nil {
			return "", false
		}
		sb.WriteString(p.Lit)
	}
	return sb.String(), true
}

func litInt(v ast.AppValue) (int64, bool) {
	l, ok := v.(*ast.AppLit)
	if !ok {
		return 0, false
	}
	i, ok := l.X.(*ast.IntLit)
	if !ok {
		return 0, false
	}
	return i.V, true
}

// LitString exposes litStr for other packages (run-line fields, env overlays).
func LitString(v ast.AppValue) (string, bool) { return litStr(v) }

// LitInt exposes litInt for other packages.
func LitInt(v ast.AppValue) (int64, bool) { return litInt(v) }
