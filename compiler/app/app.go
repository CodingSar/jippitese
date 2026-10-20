// Package app models the `app { … }` block of app.jip (spec 10) for the
// checker, the emitter and the CLI.
package app

import (
	"fmt"
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

// Dep is one `deps { alias = git(url, commit: "…", hash: "…") }` entry.
type Dep struct {
	Pos    ast.Pos
	Alias  string
	URL    string
	Commit string
	Hash   string
}

// Model is one `models { role = provider("name") }` entry.
type Model struct {
	Pos      ast.Pos
	Role     string
	Provider string
	Name     string
}

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
	Runs    []*Run
	Topics  []*Topic
	Envs    []*Env
	Entries []*ast.AppEntry // every top-level entry, in order
	Block   *ast.AppBlock
}

// Parse builds the model from a parsed app.jip.
func Parse(f *ast.File) (*App, error) {
	if f.App == nil {
		return nil, fmt.Errorf("%s: no `app { … }` block", f.Path)
	}
	b := f.App
	a := &App{Pos: b.Pos, Block: b, Entries: b.Entries}
	a.Workflow.Mode, a.Workflow.Store = "embedded", DefaultWorkflowStore
	for _, e := range b.Entries {
		switch {
		case e.Key == "name":
			s, ok := litStr(e.Value)
			if !ok {
				return nil, fmt.Errorf("%s: `name` must be a string literal", e.Pos)
			}
			a.Name = s
		case e.Key == "config":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, fmt.Errorf("%s: `config` must be a block", e.Pos)
			}
			for _, c := range blk.Entries {
				cf := &ConfigField{Pos: c.Pos, Name: c.Key, IsDefault: c.Default}
				switch v := c.Value.(type) {
				case *ast.AppType:
					cf.Type = v.T
				case *ast.AppLit:
					cf.Default = v.X
				default:
					return nil, fmt.Errorf("%s: config field `%s` needs a type or a default literal", c.Pos, c.Key)
				}
				a.Config = append(a.Config, cf)
			}
		case e.Key == "secrets":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, fmt.Errorf("%s: `secrets` must be a block", e.Pos)
			}
			for _, c := range blk.Entries {
				t, ok := c.Value.(*ast.AppType)
				if !ok {
					return nil, fmt.Errorf("%s: secret `%s` needs `secret<T>`", c.Pos, c.Key)
				}
				tp, ok := t.T.(*ast.TypePath)
				if !ok || len(tp.Path) != 1 || tp.Path[0] != "secret" || len(tp.Args) != 1 {
					return nil, fmt.Errorf("%s: secret `%s` needs `secret<T>`", c.Pos, c.Key)
				}
				a.Secrets = append(a.Secrets, &Secret{Pos: c.Pos, Name: c.Key, Type: tp.Args[0]})
			}
		case e.Key == "deps":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, fmt.Errorf("%s: `deps` must be a block", e.Pos)
			}
			for _, c := range blk.Entries {
				d := &Dep{Pos: c.Pos, Alias: c.Key}
				if call, ok := c.Value.(*ast.AppCall); ok && call.Name == "git" {
					if len(call.Args) > 0 {
						d.URL, _ = litStr(call.Args[0])
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
				return nil, fmt.Errorf("%s: `models` must be a block", e.Pos)
			}
			for _, c := range blk.Entries {
				m := &Model{Pos: c.Pos, Role: c.Key}
				if call, ok := c.Value.(*ast.AppCall); ok {
					m.Provider = call.Name
					if len(call.Args) > 0 {
						m.Name, _ = litStr(call.Args[0])
					}
				}
				a.Models = append(a.Models, m)
			}
		case e.Key == "externs":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, fmt.Errorf("%s: `externs` must be a block", e.Pos)
			}
			a.Externs.Set = true
			for _, c := range blk.Entries {
				switch c.Key {
				case "allow":
					l, ok := c.Value.(*ast.AppList)
					if !ok {
						return nil, fmt.Errorf("%s: `externs.allow` must be a list of module paths", c.Pos)
					}
					for _, el := range l.Elems {
						p, ok := el.(*ast.AppPath)
						if !ok {
							return nil, fmt.Errorf("%s: `externs.allow` must be a list of module paths", c.Pos)
						}
						a.Externs.Allow = append(a.Externs.Allow, strings.Join(p.Path, "."))
					}
				case "max":
					n, ok := litInt(c.Value)
					if !ok {
						return nil, fmt.Errorf("%s: `externs.max` must be an integer", c.Pos)
					}
					a.Externs.Max = int(n)
				}
			}
		case e.Key == "contracts":
			p, ok := e.Value.(*ast.AppPath)
			if !ok || len(p.Path) != 1 {
				return nil, fmt.Errorf("%s: `contracts` must be `all`, `pre` or `none`", e.Pos)
			}
			switch p.Path[0] {
			case "all", "pre", "none":
				a.Contracts = p.Path[0]
			default:
				return nil, fmt.Errorf("%s: `contracts` must be `all`, `pre` or `none`", e.Pos)
			}
		case e.Key == "workflow":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, fmt.Errorf("%s: `workflow` must be a block", e.Pos)
			}
			for _, c := range blk.Entries {
				switch c.Key {
				case "mode":
					p, ok := c.Value.(*ast.AppPath)
					if !ok || len(p.Path) != 1 {
						return nil, fmt.Errorf("%s: `workflow.mode` must be `embedded`", c.Pos)
					}
					if p.Path[0] != "embedded" {
						return nil, fmt.Errorf("%s: `workflow.mode: %s` is not available; only `embedded`", c.Pos, p.Path[0])
					}
					a.Workflow.Mode = p.Path[0]
				case "store":
					s, ok := litStr(c.Value)
					if !ok {
						return nil, fmt.Errorf("%s: `workflow.store` must be a string literal", c.Pos)
					}
					a.Workflow.Store = s
				default:
					return nil, fmt.Errorf("%s: unknown `workflow` field `%s`", c.Pos, c.Key)
				}
			}
		case e.Key == "topics":
			blk, ok := e.Value.(*ast.AppBlock)
			if !ok {
				return nil, fmt.Errorf("%s: `topics` must be a block", e.Pos)
			}
			for _, c := range blk.Entries {
				t, ok := c.Value.(*ast.AppType)
				if !ok {
					return nil, fmt.Errorf("%s: topic `%s` needs `Topic<T>`", c.Pos, c.Key)
				}
				tp, ok := t.T.(*ast.TypePath)
				if !ok || len(tp.Path) != 1 || tp.Path[0] != "Topic" || len(tp.Args) != 1 {
					return nil, fmt.Errorf("%s: topic `%s` needs `Topic<T>`", c.Pos, c.Key)
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
		return nil, fmt.Errorf("%s: app.jip has no `name`", b.Pos)
	}
	return a, nil
}

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
