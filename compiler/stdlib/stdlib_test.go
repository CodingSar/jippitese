package stdlib

import (
	"testing"

	"github.com/codingsar/jippitese/compiler/parser"
)

func TestParse(t *testing.T) {
	for m := range Modules {
		if _, err := parser.ParseFile(Path(m), Source(m)); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
}
