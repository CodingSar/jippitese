package checker

import (
	"slices"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
)

// runFields are the `run <Impl> {}` fields of app.jip (spec 10): the
// ones every runnable impl takes (every runner serves at least a health
// route on `port`), and the ones only one behaviour takes.
var (
	runCommon = []string{"restart", "max_restarts", "period", "shutdown_timeout", "port"}
	runOnly   = map[string]string{"topic": "Worker", "ttl": "Cache", "idle": "Machine"}
)

// runnable reports whether impls of behaviour b are supervised by a `run`
// line; an Agent is invoked through `X.run(goal)`.
func runnable(b string) bool {
	switch b {
	case "Service", "Worker", "Cron", "Workflow", "Repository", "Cache", "Machine":
		return true
	}
	return false
}

// checkRuns reports `run` lines naming no impl, an impl that is not
// runnable, or a field its behaviour does not take (E0308).
func (c *Checker) checkRuns() {
	a := c.prog.App
	if a == nil {
		return
	}
	curMod = nil
	for _, r := range a.Runs {
		var found, other *ImplInfo
		for _, m := range c.prog.Modules {
			if m.IsStd || m.IsScenarios {
				continue
			}
			for _, impl := range m.Impls {
				if impl.Behaviour == nil || impl.TargetInfo == nil || impl.TargetInfo.Name != r.Name {
					continue
				}
				if runnable(impl.Behaviour.Name) {
					found = impl
				} else {
					other = impl
				}
			}
		}
		if found == nil {
			if other != nil {
				d := c.bag.Errorf("E0308", r.Pos, "`run %s` names an `impl %s`, which is not runnable", r.Name, other.Behaviour.Name)
				d.Label("not a supervised behaviour")
				d.Note("`run` starts a Service, Worker, Cron, Workflow, Repository, Cache or Machine")
				if other.Behaviour.Name == "Agent" {
					d.Helpf("an Agent is invoked through `%s.run(goal)`; remove this line", r.Name)
				}
				continue
			}
			d := c.bag.Errorf("E0308", r.Pos, "`run %s` names no impl", r.Name)
			d.Label("no behaviour impl for `%s`", r.Name)
			if names := c.runnableNames(); len(names) > 0 {
				d.Helpf("runnable impls: %s", strings.Join(names, ", "))
			}
			continue
		}
		b := found.Behaviour.Name
		keys := make([]string, 0, len(r.Fields))
		for k := range r.Fields {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return posLess(r.Fields[keys[i]].Pos, r.Fields[keys[j]].Pos) })
		for _, k := range keys {
			en := r.Fields[k]
			if want, ok := runOnly[k]; ok {
				if want != b {
					d := c.bag.Errorf("E0308", en.Pos, "`%s` is a `%s` field; `%s` is a %s", k, want, r.Name, b)
					d.Label("not taken by a %s", b)
					continue
				}
				if k == "topic" {
					if t := appValueName(en); t != "" {
						if _, ok := c.prog.Topics[t]; !ok {
							d := c.bag.Errorf("E0308", en.Pos, "unknown topic `%s`", t)
							d.Label("not in `topics {}`")
						}
					}
				}
				continue
			}
			if !slices.Contains(runCommon, k) {
				d := c.bag.Errorf("E0308", en.Pos, "unknown `run` field `%s`", k)
				d.Label("not a `run` field")
				d.Helpf("`run` takes %s", strings.Join(append(append([]string{}, runCommon...), runFieldFor(b)...), ", "))
			}
		}
	}
}

func runFieldFor(b string) []string {
	for k, v := range runOnly {
		if v == b {
			return []string{k}
		}
	}
	return nil
}

func (c *Checker) runnableNames() []string {
	var names []string
	for _, m := range c.prog.Modules {
		if m.IsStd || m.IsScenarios {
			continue
		}
		for _, impl := range m.Impls {
			if impl.Behaviour != nil && impl.TargetInfo != nil && runnable(impl.Behaviour.Name) {
				names = append(names, impl.TargetInfo.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// appValueName is a path or string literal value, "" for anything else.
func appValueName(en *ast.AppEntry) string {
	if p, ok := en.Value.(*ast.AppPath); ok {
		return strings.Join(p.Path, ".")
	}
	s, _ := app.LitString(en.Value)
	return s
}
