package driver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/format"
)

// Fmt rewrites every .jip file under the root to canonical form; with
// check it writes nothing and fails when a file would change (spec 13
// rule 5). Vendored dependencies and .jip/ are left alone: their bytes are
// pinned by hash.
func (e *Env) Fmt(check bool) int {
	var files []string
	filepath.WalkDir(e.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if p != e.Root && (strings.HasPrefix(n, ".") || n == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".jip") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	code := ExitOK
	for _, p := range files {
		rel := relTo(e.Root, p)
		src, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(e.Stderr, "error: %v\n", err)
			code = ExitFail
			continue
		}
		out, err := format.Source(filepath.ToSlash(rel), string(src))
		if err != nil {
			fmt.Fprintf(e.Stderr, "%s: %v\n", rel, err)
			if strings.Contains(err.Error(), "internal:") {
				code = ExitInternal
			} else if code == ExitOK {
				code = ExitFail
			}
			continue
		}
		if out == string(src) {
			continue
		}
		if check {
			fmt.Fprintln(e.Stdout, rel)
			if code == ExitOK {
				code = ExitFail
			}
			continue
		}
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			fmt.Fprintf(e.Stderr, "error: %v\n", err)
			code = ExitFail
			continue
		}
		fmt.Fprintln(e.Stdout, rel)
	}
	return code
}
