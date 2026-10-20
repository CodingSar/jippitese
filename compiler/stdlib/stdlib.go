// Package stdlib embeds the jippitese sources of the prelude and the std.* modules.
package stdlib

import "embed"

//go:embed *.jip
var files embed.FS

// Modules maps a module path to its embedded file name.
var Modules = map[string]string{
	"prelude":     "prelude.jip",
	"std.json":    "json.jip",
	"std.time":    "time.jip",
	"std.math":    "math.jip",
	"std.fs":      "fs.jip",
	"std.http":    "http.jip",
	"std.gen":     "gen.jip",
	"std.testing": "testing.jip",
}

// Source returns the jip source of a stdlib module ("" when unknown).
func Source(module string) string {
	name, ok := Modules[module]
	if !ok {
		return ""
	}
	b, err := files.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(b)
}

// Path is the virtual file path reported in diagnostics for a stdlib module.
func Path(module string) string { return "<std>/" + Modules[module] }
