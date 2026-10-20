package rt

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// ---- std.fs ----------------------------------------------------------------
//
// std.fs reaches the file system through ctx.Fs, so a scenario that binds a
// FakeFs routes it in memory; with none installed it is the real one
// (spec 12 rule 10: `io` runs for real unless faked).

// Fs is the file system std.fs uses. Errors follow the os package, so
// IoErrorOf maps them the same way for both implementations.
type Fs interface {
	ReadFile(p string) ([]byte, error)
	WriteFile(p string, b []byte, appendTo bool) error
	ReadDir(p string) ([]string, error)
	Stat(p string) (exists bool, err error)
	Remove(p string) error
	MkdirAll(p string) error
}

func (c *Ctx) fs() Fs {
	if c.Fs == nil {
		return osFs{}
	}
	return c.Fs
}

type osFs struct{}

func (osFs) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func (osFs) WriteFile(p string, b []byte, appendTo bool) error {
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendTo {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := os.OpenFile(p, flag, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (osFs) ReadDir(p string) ([]string, error) {
	es, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name()
	}
	return names, nil
}

func (osFs) Stat(p string) (bool, error) {
	_, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (osFs) Remove(p string) error   { return os.Remove(p) }
func (osFs) MkdirAll(p string) error { return os.MkdirAll(p, 0o755) }

func ioResult[T any](v T, err error) Result[T, IoError] {
	if err != nil {
		return Result_Err[T](ioError(err))
	}
	return Result_Ok[T, IoError](v)
}

// FsRead implements `std.fs.read(p)`.
func FsRead(ctx *Ctx, p Path) Result[[]byte, IoError] {
	return ioResult(ctx.fs().ReadFile(string(p)))
}

// FsReadText implements `std.fs.read_text(p)`.
func FsReadText(ctx *Ctx, p Path) Result[string, IoError] {
	b, err := ctx.fs().ReadFile(string(p))
	return ioResult(strings.ToValidUTF8(string(b), "�"), err)
}

// FsWrite implements `std.fs.write(p, b)`.
func FsWrite(ctx *Ctx, p Path, b []byte) Result[Unit, IoError] {
	return ioResult(U, ctx.fs().WriteFile(string(p), b, false))
}

// FsWriteText implements `std.fs.write_text(p, s)`.
func FsWriteText(ctx *Ctx, p Path, s string) Result[Unit, IoError] {
	return ioResult(U, ctx.fs().WriteFile(string(p), []byte(s), false))
}

// FsAppend implements `std.fs.append(p, b)`.
func FsAppend(ctx *Ctx, p Path, b []byte) Result[Unit, IoError] {
	return ioResult(U, ctx.fs().WriteFile(string(p), b, true))
}

// FsList implements `std.fs.list(p)`: entry names, sorted.
func FsList(ctx *Ctx, p Path) Result[[]string, IoError] {
	names, err := ctx.fs().ReadDir(string(p))
	slices.Sort(names)
	return ioResult(names, err)
}

// FsPresent implements `std.fs.present(p)`.
func FsPresent(ctx *Ctx, p Path) Result[bool, IoError] {
	return ioResult(ctx.fs().Stat(string(p)))
}

// FsRemove implements `std.fs.remove(p)`.
func FsRemove(ctx *Ctx, p Path) Result[Unit, IoError] {
	return ioResult(U, ctx.fs().Remove(string(p)))
}

// FsMkdir implements `std.fs.mkdir(p)`.
func FsMkdir(ctx *Ctx, p Path) Result[Unit, IoError] {
	return ioResult(U, ctx.fs().MkdirAll(string(p)))
}

// ---- Path ------------------------------------------------------------------

// PathOf implements `Path.of(s)`.
func PathOf(ctx *Ctx, s string) Path { return Path(filepath.Clean(s)) }

// PathJoin implements `join(name)`.
func PathJoin(ctx *Ctx, p Path, name string) Path { return Path(filepath.Join(string(p), name)) }

// PathToStr implements `to_str()`.
func PathToStr(ctx *Ctx, p Path) string { return string(p) }

// PathName implements `name()`.
func PathName(ctx *Ctx, p Path) string { return filepath.Base(string(p)) }

// PathParent implements `parent()`.
func PathParent(ctx *Ctx, p Path) Path { return Path(filepath.Dir(string(p))) }

// ---- FakeFs ----------------------------------------------------------------

// FakeFs is `std.testing.FakeFs`: an in-memory file system. Paths are
// cleaned, so `a/./b` and `a/b` name one file; the root and `.` always
// exist. Like the real one, writing needs the parent directory and
// removing a directory needs it empty.
type FakeFs struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

// FakeFsNew implements `FakeFs.new()`.
func FakeFsNew(ctx *Ctx) *FakeFs {
	return &FakeFs{files: map[string][]byte{}, dirs: map[string]bool{}}
}

// FakeFsFile implements `file(p, b)`: adds a file and its parents.
func FakeFsFile(ctx *Ctx, f *FakeFs, p Path, b []byte) *FakeFs {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(string(p))
	f.mkdirs(filepath.Dir(k))
	f.files[k] = slices.Clone(b)
	return f
}

// FakeFsContents implements `contents(p)`.
func FakeFsContents(ctx *Ctx, f *FakeFs, p Path) Option[[]byte] {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.files[filepath.Clean(string(p))]; ok {
		return Option_Some(slices.Clone(b))
	}
	return Option_None[[]byte]()
}

func pathErr(op, p string, err error) error { return &fs.PathError{Op: op, Path: p, Err: err} }

func (f *FakeFs) isDir(k string) bool {
	return f.dirs[k] || k == "." || k == string(filepath.Separator) || filepath.Dir(k) == k
}

func (f *FakeFs) mkdirs(k string) {
	for ; !f.isDir(k); k = filepath.Dir(k) {
		f.dirs[k] = true
	}
}

func (f *FakeFs) ReadFile(p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(p)
	if b, ok := f.files[k]; ok {
		return slices.Clone(b), nil
	}
	if f.isDir(k) {
		return nil, pathErr("read", p, errors.New("is a directory"))
	}
	return nil, pathErr("open", p, fs.ErrNotExist)
}

func (f *FakeFs) WriteFile(p string, b []byte, appendTo bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(p)
	if f.isDir(k) {
		return pathErr("open", p, errors.New("is a directory"))
	}
	if parent := filepath.Dir(k); !f.isDir(parent) {
		if _, isFile := f.files[parent]; isFile {
			return pathErr("open", p, errors.New("not a directory"))
		}
		return pathErr("open", p, fs.ErrNotExist)
	}
	if appendTo {
		f.files[k] = append(slices.Clone(f.files[k]), b...)
	} else {
		f.files[k] = slices.Clone(b)
	}
	return nil
}

func (f *FakeFs) ReadDir(p string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(p)
	if !f.isDir(k) {
		if _, isFile := f.files[k]; isFile {
			return nil, pathErr("readdir", p, errors.New("not a directory"))
		}
		return nil, pathErr("open", p, fs.ErrNotExist)
	}
	var names []string
	for c := range f.dirs {
		if c != k && filepath.Dir(c) == k {
			names = append(names, filepath.Base(c))
		}
	}
	for c := range f.files {
		if filepath.Dir(c) == k {
			names = append(names, filepath.Base(c))
		}
	}
	return names, nil
}

func (f *FakeFs) Stat(p string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(p)
	_, isFile := f.files[k]
	return isFile || f.isDir(k), nil
}

func (f *FakeFs) Remove(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(p)
	if _, ok := f.files[k]; ok {
		delete(f.files, k)
		return nil
	}
	if !f.dirs[k] {
		if f.isDir(k) {
			return pathErr("remove", p, fs.ErrPermission)
		}
		return pathErr("remove", p, fs.ErrNotExist)
	}
	for c := range f.dirs {
		if filepath.Dir(c) == k {
			return pathErr("remove", p, errors.New("directory not empty"))
		}
	}
	for c := range f.files {
		if filepath.Dir(c) == k {
			return pathErr("remove", p, errors.New("directory not empty"))
		}
	}
	delete(f.dirs, k)
	return nil
}

func (f *FakeFs) MkdirAll(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := filepath.Clean(p)
	for a := k; !f.isDir(a); a = filepath.Dir(a) {
		if _, isFile := f.files[a]; isFile {
			return pathErr("mkdir", p, errors.New("not a directory"))
		}
	}
	f.mkdirs(k)
	return nil
}
