package emit

import (
	"sort"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// emitAppCfg writes package appcfg: the Config and Secrets records the
// checker synthesised from app.jip, the overlay loader (spec 10), the run
// options, and the scenario helpers.
func (e *emitter) emitAppCfg() {
	p := &pkg{e: e, name: "appcfg", path: e.mainPkg + "/appcfg", dir: "appcfg", names: map[string]bool{}}
	e.cfgPkg = p
	e.pkgOrder = append(e.pkgOrder, p)
	f := p.newFile("appcfg.go", false)
	rtp := f.rt()
	if e.prog.Config == nil {
		f.printf("type Config struct{}\ntype Secrets struct{}\n")
		f.printf("func SecretsFake(ctx *%s.Ctx) Secrets { return Secrets{} }\n", rtp)
		f.printf("func Load() (Config, map[string]%s.RunOpts, error) { return Config{}, nil, nil }\n", rtp)
		f.printf("func TestConfig() Config { return Config{} }\n")
		return
	}
	for _, ti := range []*checker.TypeInfo{e.prog.Config, e.prog.Secrets} {
		f.printf("\n// %s\n", ti.Doc)
		f.printf("type %s struct {\n", ti.Name)
		for _, fl := range ti.Fields {
			f.printf("\t%s %s `json:\"%s\"`\n", fieldName(fl.Name), f.goType(fl.Type), fl.Name)
		}
		f.printf("}\n")
	}
	f.printf("\n// SecretsFake is `Secrets.fake()`: a placeholder per secret.\n")
	f.printf("func SecretsFake(ctx *%s.Ctx) Secrets {\n\treturn Secrets{", rtp)
	for i, fl := range e.prog.Secrets.Fields {
		if i > 0 {
			f.printf(", ")
		}
		f.printf("%s: %s(%q)", fieldName(fl.Name), f.goType(fl.Type), "fake-"+fl.Name)
	}
	f.printf("}\n}\n")

	a := e.prog.App
	// env blocks
	f.printf("\nvar envBlocks = map[string]map[string]string{\n")
	for _, env := range a.Envs {
		f.printf("\t%q: {\n", env.Name)
		for _, en := range env.Entries {
			f.printf("\t\t%q: %q,\n", en.Key, appValueString(en.Value))
		}
		f.printf("\t},\n")
	}
	f.printf("}\n")

	var known []string
	f.printf("\nfunc load(l *%s.ConfigLoader) Config {\n\tvar c Config\n", rtp)
	byName := map[string]*app.ConfigField{}
	for i := range a.Config {
		byName[a.Config[i].Name] = a.Config[i]
	}
	for _, fl := range e.prog.Config.Fields {
		if fl.Name == "secrets" {
			continue
		}
		cf := byName[fl.Name]
		mode, def, at := rtp+".CfgRequired", "", `""`
		if cf != nil && cf.Default != nil {
			mode = rtp + ".CfgFixed"
			if cf.IsDefault {
				mode = rtp + ".CfgOverridable"
			}
			def = configLit(cf.Default)
			at = strconv.Quote(cf.Default.ExprPos().String())
		}
		known = append(known, envKey(fl.Name))
		f.printf("\tc.%s = %s\n", fieldName(fl.Name), f.configRead(fl.Type, strconv.Quote(fl.Name), def, mode, at))
	}
	for _, fl := range e.prog.Secrets.Fields {
		known = append(known, envKey("secrets."+fl.Name))
		f.printf("\tc.Secrets.%s = %s(l.Secret(%q))\n", fieldName(fl.Name), f.goType(fl.Type), fl.Name)
	}
	f.printf("\treturn c\n}\n")

	// run options
	f.printf("\nfunc runs(l *%s.ConfigLoader) map[string]%s.RunOpts {\n\treturn map[string]%s.RunOpts{\n", rtp, rtp, rtp)
	for _, r := range a.Runs {
		str := func(field, def string) string {
			if en := r.Fields[field]; en != nil {
				def = appValueString(en.Value)
			}
			known = append(known, envKey("run."+r.Name+"."+field))
			return "l.RunStr(" + strconv.Quote(r.Name) + ", " + strconv.Quote(field) + ", " + strconv.Quote(def) + ")"
		}
		num := func(field string, def int64) string {
			if en := r.Fields[field]; en != nil {
				if n, ok := app.LitInt(en.Value); ok {
					def = n
				}
			}
			known = append(known, envKey("run."+r.Name+"."+field))
			return "int(l.RunInt(" + strconv.Quote(r.Name) + ", " + strconv.Quote(field) + ", " + strconv.FormatInt(def, 10) + "))"
		}
		dur := func(field, def string) string {
			if en := r.Fields[field]; en != nil {
				def = appValueString(en.Value)
			}
			known = append(known, envKey("run."+r.Name+"."+field))
			d, _ := parseDur(def)
			return "l.RunDuration(" + strconv.Quote(r.Name) + ", " + strconv.Quote(field) + ", " + strconv.FormatInt(d, 10) + ")"
		}
		f.printf("\t\t%q: {Name: %q, Restart: %s, MaxRestarts: %s, Period: %s, ShutdownTimeout: %s, Topic: %s, Port: %s, Ttl: %s},\n",
			r.Name, r.Name, str("restart", "permanent"), num("max_restarts", 3), dur("period", "60s"),
			dur("shutdown_timeout", "10s"), str("topic", ""), num("port", 8080), dur("ttl", "5m"))
	}
	f.printf("\t}\n}\n")

	sort.Strings(known)
	f.printf("\nvar known = []string{")
	for i, k := range known {
		if i > 0 {
			f.printf(", ")
		}
		f.printf("%q", k)
	}
	f.printf("}\n")
	f.printf(`
// Load applies the overlay (app body < env block < JIP_* variables) and
// reports every problem at once.
func Load() (Config, map[string]%[1]s.RunOpts, error) {
	l := %[1]s.NewConfigLoader(envBlocks)
	c := load(l)
	r := runs(l)
	return c, r, l.Finish(known)
}

// TestConfig is the config scenarios see: the env test overlay, with
// unset required fields left at their zero value.
func TestConfig() Config {
	l := %[1]s.NewConfigLoaderFor("test", envBlocks)
	c := load(l)
	c.Secrets = SecretsFake(nil)
	return c
}
`, rtp)
}

func envKey(path string) string {
	return "JIP_" + strings.ToUpper(strings.ReplaceAll(path, ".", "__"))
}

// configRead is the loader call for one leaf, converted to the field type.
func (f *file) configRead(t checker.Type, path, def, mode, at string) string {
	gt := f.goType(t)
	prim := primOf(t)
	switch prim {
	case "Int":
		if def == "" {
			def = "0"
		}
		return gt + "(l.Int(" + path + ", " + def + ", " + mode + ", " + at + "))"
	case "Float":
		if def == "" {
			def = "0"
		}
		return gt + "(l.Float(" + path + ", " + def + ", " + mode + ", " + at + "))"
	case "Bool":
		if def == "" {
			def = "false"
		}
		return gt + "(l.Bool(" + path + ", " + def + ", " + mode + ", " + at + "))"
	case "Duration":
		if def == "" {
			def = "0"
		}
		return gt + "(l.Duration(" + path + ", " + def + ", " + mode + ", " + at + "))"
	}
	if def == "" {
		def = `""`
	}
	return gt + "(l.Str(" + path + ", " + def + ", " + mode + ", " + at + "))"
}

// primOf unwraps newtypes down to a primitive name ("" if none).
func primOf(t checker.Type) string {
	for i := 0; i < 8; i++ {
		tc, ok := checker.Zonk(t).(*checker.TCon)
		if !ok {
			return ""
		}
		switch tc.Info.Kind {
		case checker.Prim:
			return tc.Info.Name
		case checker.Newtype:
			t = tc.Info.Inner
			continue
		}
		return ""
	}
	return ""
}

// configLit is the Go literal of a config default.
func configLit(x ast.Expr) string {
	switch l := x.(type) {
	case *ast.IntLit:
		return strconv.FormatInt(l.V, 10)
	case *ast.FloatLit:
		return strconv.FormatFloat(l.V, 'g', -1, 64)
	case *ast.BoolLit:
		return strconv.FormatBool(l.V)
	case *ast.DurationLit:
		return strconv.FormatInt(l.Ns, 10)
	case *ast.StrLit:
		var b strings.Builder
		for _, p := range l.Parts {
			b.WriteString(p.Lit)
		}
		return strconv.Quote(b.String())
	case *ast.CallExpr:
		if len(l.Args) == 1 {
			return configLit(l.Args[0])
		}
	}
	return strconv.Quote(checker.ExprString(x))
}

// appValueString is the overlay string of an app.jip value.
func appValueString(v ast.AppValue) string {
	switch x := v.(type) {
	case *ast.AppLit:
		if s, ok := x.X.(*ast.StrLit); ok {
			var b strings.Builder
			for _, p := range s.Parts {
				b.WriteString(p.Lit)
			}
			return b.String()
		}
		if c, ok := x.X.(*ast.CallExpr); ok && len(c.Args) == 1 {
			return appValueString(&ast.AppLit{X: c.Args[0]})
		}
		return checker.ExprString(x.X)
	case *ast.AppPath:
		return strings.Join(x.Path, ".")
	}
	if s, ok := app.LitString(v); ok {
		return s
	}
	return ""
}

// parseDur parses a duration literal to nanoseconds.
func parseDur(s string) (int64, bool) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
		i++
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(s[:i], "_", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	unit := map[string]int64{"ns": 1, "us": 1e3, "ms": 1e6, "s": 1e9, "m": 60e9, "h": 3600e9, "d": 86400e9}[s[i:]]
	if unit == 0 {
		return 0, false
	}
	return n * unit, true
}
