package format

import (
	"fmt"

	"github.com/codingsar/jippitese/compiler/parser"
)

// Source formats one file's text. It refuses (returns an error) when the
// result would not parse back to the same program or would lose a
// comment, so `jip fmt` can never change meaning or drop text.
func Source(path, src string) (string, error) {
	f, err := parser.ParseFile(path, src)
	if err != nil {
		return "", err
	}
	out := File(f, src)
	g, err := parser.ParseFile(path, out)
	if err != nil {
		return "", fmt.Errorf("internal: formatted %s does not parse: %v", path, err)
	}
	if File(f, "") != File(g, "") {
		return "", fmt.Errorf("internal: formatting %s would change its meaning", path)
	}
	if a, b := len(scanComments(src)), len(scanComments(out)); a != b {
		return "", fmt.Errorf("internal: formatting %s would drop comments (%d of %d kept)", path, b, a)
	}
	return out, nil
}
