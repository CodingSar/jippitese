package emit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"runtime"

	"github.com/codingsar/jippitese/compiler/app"
)

// externCache records the externs whose Go side checked clean (spec 13
// rule 12), so a hit skips loading and type-checking the Go package, which
// is nearly all of `jip check`. A key covers the declared target and
// signature, the Go toolchain, every gomod pin and the compiler binary;
// only clean results are recorded, so a hit never hides a diagnostic.
// JIP_CACHE=off disables it.
type externCache struct {
	dir  string
	base []byte
}

// externLoads counts Go package loads, for tests.
var externLoads int

func newExternCache(root string, a *app.App) *externCache {
	if root == "" || os.Getenv("JIP_CACHE") == "off" {
		return nil
	}
	h := sha256.New()
	fmt.Fprintf(h, "jip-extern-1\x00%s\x00%s\x00", runtime.Version(), build.Default.GOROOT)
	if v, err := os.ReadFile(filepath.Join(build.Default.GOROOT, "VERSION")); err == nil {
		h.Write(v)
	}
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(exe); err == nil {
			fmt.Fprintf(h, "\x00%s\x00%d\x00%d", exe, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	if a != nil {
		for _, d := range a.Deps {
			if d.Kind == "gomod" {
				fmt.Fprintf(h, "\x00%s\x00%s\x00%s", d.Alias, d.GoPath, d.Hash)
			}
		}
	}
	return &externCache{dir: filepath.Join(root, ".jip", "cache", "externs"), base: h.Sum(nil)}
}

// key names the check of pkg.ident against the declared Go signature want
// and receiver recv.
func (c *externCache) key(pkg, ident, want, recv string) string {
	h := sha256.New()
	h.Write(c.base)
	fmt.Fprintf(h, "\x00%s\x00%s\x00%s\x00%s", pkg, ident, want, recv)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *externCache) hit(key string) bool {
	if c == nil {
		return false
	}
	_, err := os.Stat(filepath.Join(c.dir, key))
	return err == nil
}

// record marks key clean; a failed write only costs a later miss.
func (c *externCache) record(key string) {
	if c == nil {
		return
	}
	if os.MkdirAll(c.dir, 0o755) == nil {
		_ = os.WriteFile(filepath.Join(c.dir, key), nil, 0o644)
	}
}
