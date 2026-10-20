package emit

import "testing"

func TestGoModVersion(t *testing.T) {
	for path, want := range map[string]string{
		"example.com/greet":    "v0",
		"example.com/greet/v2": "v2",
		"gopkg.in/yaml.v3":     "v3",
		"gopkg.in/check.v1":    "v1",
		"example.com/v1":       "v0",
	} {
		if got := GoModVersion(path); got != want+".0.0-00010101000000-000000000000" {
			t.Errorf("%s: %s", path, got)
		}
	}
}
