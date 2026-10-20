// Package diag holds the diagnostics every stage of the compiler produces and
// the two printers spec 13 prescribes: the human layout and `--json`.
package diag

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// Severity of a diagnostic. Only `jip check` prints warnings (spec 13).
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Range is a half-open source span used by `--json` edits.
type Range struct {
	Line, Col, EndLine, EndCol int
}

// Edit is a machine-applicable fix attached to a help line.
type Edit struct {
	File        string
	Range       Range
	Replacement string
}

// Help is one `= help:` line with an optional edit.
type Help struct {
	Text string
	Edit *Edit
}

// Diagnostic is one error or warning.
type Diagnostic struct {
	Code     string
	Severity Severity
	Pos      ast.Pos
	End      ast.Pos // zero = Pos plus one token
	Message  string
	Caption  string // caret label
	Notes    []string
	Help     []Help
}

// Label sets the caret label.
func (d *Diagnostic) Label(format string, args ...interface{}) *Diagnostic {
	d.Caption = fmt.Sprintf(format, args...)
	return d
}

// Note appends a `= note:` line.
func (d *Diagnostic) Note(format string, args ...interface{}) *Diagnostic {
	d.Notes = append(d.Notes, fmt.Sprintf(format, args...))
	return d
}

// Helpf appends a `= help:` line without an edit.
func (d *Diagnostic) Helpf(format string, args ...interface{}) *Diagnostic {
	d.Help = append(d.Help, Help{Text: fmt.Sprintf(format, args...)})
	return d
}

// HelpEdit appends a help line carrying an edit.
func (d *Diagnostic) HelpEdit(text string, e Edit) *Diagnostic {
	d.Help = append(d.Help, Help{Text: text, Edit: &e})
	return d
}

// Span sets the end of the highlighted range.
func (d *Diagnostic) Span(end ast.Pos) *Diagnostic {
	d.End = end
	return d
}

// Width sets the highlighted width in columns on the start line.
func (d *Diagnostic) Width(n int) *Diagnostic {
	if n < 1 {
		n = 1
	}
	d.End = ast.Pos{File: d.Pos.File, Line: d.Pos.Line, Col: d.Pos.Col + n}
	return d
}

// Bag collects diagnostics and the sources they point into.
type Bag struct {
	Diags   []*Diagnostic
	Sources map[string]string
}

// NewBag returns an empty bag.
func NewBag() *Bag { return &Bag{Sources: map[string]string{}} }

// AddSource registers a file's text so the printer can quote it.
func (b *Bag) AddSource(path, src string) {
	if b.Sources == nil {
		b.Sources = map[string]string{}
	}
	b.Sources[path] = src
}

// Add appends a diagnostic.
func (b *Bag) Add(d *Diagnostic) *Diagnostic {
	b.Diags = append(b.Diags, d)
	return d
}

// Errorf adds an error at pos.
func (b *Bag) Errorf(code string, pos ast.Pos, format string, args ...interface{}) *Diagnostic {
	return b.Add(&Diagnostic{Code: code, Severity: Error, Pos: pos, Message: fmt.Sprintf(format, args...)})
}

// Warnf adds a warning at pos.
func (b *Bag) Warnf(code string, pos ast.Pos, format string, args ...interface{}) *Diagnostic {
	return b.Add(&Diagnostic{Code: code, Severity: Warning, Pos: pos, Message: fmt.Sprintf(format, args...)})
}

// HasErrors reports whether any error-severity diagnostic was added.
func (b *Bag) HasErrors() bool {
	for _, d := range b.Diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Count returns the numbers of errors and warnings.
func (b *Bag) Count() (errors, warnings int) {
	for _, d := range b.Diags {
		if d.Severity == Error {
			errors++
		} else {
			warnings++
		}
	}
	return
}

// Merge appends another bag's diagnostics and sources.
func (b *Bag) Merge(o *Bag) {
	if o == nil {
		return
	}
	b.Diags = append(b.Diags, o.Diags...)
	for k, v := range o.Sources {
		b.AddSource(k, v)
	}
}

// sorted returns the diagnostics grouped per file in path order, then by line
// and column, as spec 13 prescribes.
func (b *Bag) sorted() []*Diagnostic {
	out := append([]*Diagnostic(nil), b.Diags...)
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Pos.File != c.Pos.File {
			return a.Pos.File < c.Pos.File
		}
		if a.Pos.Line != c.Pos.Line {
			return a.Pos.Line < c.Pos.Line
		}
		return a.Pos.Col < c.Pos.Col
	})
	return out
}

// MaxShown is the number of diagnostics printed before `… and N more`.
const MaxShown = 20

// Format prints the human layout. With all=false at most MaxShown
// diagnostics are shown; warnings are included only when warnings is true.
func (b *Bag) Format(w io.Writer, all, warnings bool) {
	var ds []*Diagnostic
	for _, d := range b.sorted() {
		if d.Severity == Warning && !warnings {
			continue
		}
		ds = append(ds, d)
	}
	shown := 0
	hidden := 0
	curFile := ""
	perFile := 0
	flush := func() {
		if curFile != "" {
			fmt.Fprintf(w, "%s: %s\n", curFile, plural(perFile, "error"))
		}
	}
	for _, d := range ds {
		if d.Pos.File != curFile {
			if curFile != "" {
				flush()
				fmt.Fprintln(w)
			}
			curFile = d.Pos.File
			perFile = 0
		}
		if d.Severity == Error {
			perFile++
		}
		if !all && shown >= MaxShown {
			hidden++
			continue
		}
		b.formatOne(w, d)
		shown++
	}
	if hidden > 0 {
		fmt.Fprintf(w, "… and %s (run with --all)\n", plural(hidden, "more error"))
	}
	flush()
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// FormatOne prints a single diagnostic in the human layout.
func (b *Bag) FormatOne(w io.Writer, d *Diagnostic) { b.formatOne(w, d) }

func (b *Bag) formatOne(w io.Writer, d *Diagnostic) {
	fmt.Fprintf(w, "%s[%s]: %s\n", d.Severity, d.Code, d.Message)
	if d.Pos.File == "" {
		for _, n := range d.Notes {
			fmt.Fprintf(w, "   = note: %s\n", n)
		}
		for _, h := range d.Help {
			fmt.Fprintf(w, "   = help: %s\n", h.Text)
		}
		return
	}
	fmt.Fprintf(w, "  --> %s:%d:%d\n", d.Pos.File, d.Pos.Line, d.Pos.Col)
	num := fmt.Sprintf("%d", d.Pos.Line)
	gutter := strings.Repeat(" ", len(num)+1) + "|"
	fmt.Fprintf(w, "%s\n", gutter)
	line := b.sourceLine(d.Pos.File, d.Pos.Line)
	fmt.Fprintf(w, "%s | %s\n", num, line)
	width := 1
	if d.End.Line == d.Pos.Line && d.End.Col > d.Pos.Col {
		width = d.End.Col - d.Pos.Col
	} else if d.End.Line == 0 {
		width = tokenWidth(line, d.Pos.Col)
	} else if d.End.Line > d.Pos.Line {
		width = len(line) - d.Pos.Col + 1
		if width < 1 {
			width = 1
		}
	}
	caret := strings.Repeat(" ", d.Pos.Col-1) + strings.Repeat("^", width)
	if d.Caption != "" {
		caret += " " + d.Caption
	}
	fmt.Fprintf(w, "%s %s\n", gutter, caret)
	for _, n := range d.Notes {
		fmt.Fprintf(w, "%s = note: %s\n", strings.Repeat(" ", len(num)+1), n)
	}
	for _, h := range d.Help {
		fmt.Fprintf(w, "%s = help: %s\n", strings.Repeat(" ", len(num)+1), h.Text)
	}
}

func (b *Bag) sourceLine(file string, line int) string {
	src, ok := b.Sources[file]
	if !ok {
		return ""
	}
	lines := strings.Split(src, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimRight(lines[line-1], "\r")
}

// tokenWidth guesses the width of the token starting at col (1-based).
// TokenWidth is the width in bytes of the token at byte column col of line,
// the span a diagnostic without an End highlights.
func TokenWidth(line string, col int) int { return tokenWidth(line, col) }

func tokenWidth(line string, col int) int {
	if col < 1 || col > len(line) {
		return 1
	}
	i := col - 1
	c := line[i]
	isIdent := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	if isIdent(c) {
		j := i
		for j < len(line) && (isIdent(line[j]) || line[j] == '.') {
			j++
		}
		return j - i
	}
	if c == '"' {
		j := i + 1
		for j < len(line) && line[j] != '"' {
			if line[j] == '\\' {
				j++
			}
			j++
		}
		if j < len(line) {
			j++
		}
		return j - i
	}
	return 1
}

type jsonEdit struct {
	File        string `json:"file"`
	Range       Range  `json:"range"`
	Replacement string `json:"replacement"`
}
type jsonHelp struct {
	Text string    `json:"text"`
	Edit *jsonEdit `json:"edit,omitempty"`
}
type jsonDiag struct {
	Code     string     `json:"code"`
	Severity string     `json:"severity"`
	File     string     `json:"file"`
	Line     int        `json:"line"`
	Col      int        `json:"col"`
	EndLine  int        `json:"end_line"`
	EndCol   int        `json:"end_col"`
	Message  string     `json:"message"`
	Notes    []string   `json:"notes"`
	Help     []jsonHelp `json:"help"`
}

// FormatJSON prints one JSON object per diagnostic followed by the summary
// object, as spec 13 prescribes for `--json`.
func (b *Bag) FormatJSON(w io.Writer, warnings bool) {
	errs, warns := 0, 0
	for _, d := range b.sorted() {
		if d.Severity == Warning && !warnings {
			continue
		}
		if d.Severity == Error {
			errs++
		} else {
			warns++
		}
		end := d.End
		if end.Line == 0 {
			end = ast.Pos{File: d.Pos.File, Line: d.Pos.Line, Col: d.Pos.Col + tokenWidth(b.sourceLine(d.Pos.File, d.Pos.Line), d.Pos.Col)}
		}
		jd := jsonDiag{Code: d.Code, Severity: d.Severity.String(), File: d.Pos.File, Line: d.Pos.Line, Col: d.Pos.Col,
			EndLine: end.Line, EndCol: end.Col, Message: d.Message, Notes: d.Notes, Help: []jsonHelp{}}
		if jd.Notes == nil {
			jd.Notes = []string{}
		}
		for _, h := range d.Help {
			jh := jsonHelp{Text: h.Text}
			if h.Edit != nil {
				jh.Edit = &jsonEdit{File: h.Edit.File, Range: h.Edit.Range, Replacement: h.Edit.Replacement}
			}
			jd.Help = append(jd.Help, jh)
		}
		enc, _ := json.Marshal(jd)
		fmt.Fprintln(w, string(enc))
	}
	sum, _ := json.Marshal(map[string]interface{}{"summary": true, "errors": errs, "warnings": warns})
	fmt.Fprintln(w, string(sum))
}

// Error makes a Bag usable as an error value.
func (b *Bag) Error() string {
	var sb strings.Builder
	b.Format(&sb, true, false)
	return sb.String()
}
