package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codingsar/jippitese/compiler/format"
)

// session runs a server over the given client messages and returns the
// exit code and every message the server wrote.
func session(t *testing.T, msgs ...map[string]any) (int, []*message) {
	t.Helper()
	var in, out, log bytes.Buffer
	for _, m := range msgs {
		m["jsonrpc"] = "2.0"
		b, _ := json.Marshal(m)
		in.WriteString("Content-Length: ")
		in.WriteString(itoa(len(b)))
		in.WriteString("\r\n\r\n")
		in.Write(b)
	}
	code := Run(&in, &out, &log)
	var got []*message
	r := bufio.NewReader(&out)
	for {
		m, err := readMessage(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, m)
	}
	if log.Len() > 0 {
		t.Logf("server log: %s", log.String())
	}
	return code, got
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func card(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", "../checker/testdata/orders/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	return dir
}

func req(id int, method string, params any) map[string]any {
	return map[string]any{"id": id, "method": method, "params": params}
}

func note(method string, params any) map[string]any {
	return map[string]any{"method": method, "params": params}
}

// diagnostics is the last publishDiagnostics for uri in msgs[from:].
func diagnostics(t *testing.T, msgs []*message, uri string) (found bool, ds []lspDiagnostic) {
	for _, m := range msgs {
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p struct {
			URI         string          `json:"uri"`
			Diagnostics []lspDiagnostic `json:"diagnostics"`
		}
		json.Unmarshal(m.Params, &p)
		if p.URI == uri {
			found, ds = true, p.Diagnostics
		}
	}
	return
}

func TestSessionDiagnosticsAndFormatting(t *testing.T) {
	root := card(t)
	path := filepath.Join(root, "orders/billing/billing.jip")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	clean := string(b)
	uri := pathURI(path)
	broken := clean + "\nfn broken() -> Int { \"x\" }\n"
	open := note("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "jip", "version": 1, "text": broken}})

	code, msgs := session(t,
		req(1, "initialize", map[string]any{"capabilities": map[string]any{}}),
		note("initialized", map[string]any{}),
		open,
		req(2, "shutdown", nil),
		note("exit", nil),
	)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if string(msgs[0].ID) != "1" || !strings.Contains(string(msgs[0].Result), `"positionEncoding":"utf-16"`) {
		t.Fatalf("initialize: %s", msgs[0].Result)
	}
	found, ds := diagnostics(t, msgs, uri)
	if !found || len(ds) == 0 {
		t.Fatalf("no diagnostics for the broken buffer: %v", msgs)
	}
	last := strings.Count(broken, "\n") - 1
	var hit bool
	for _, d := range ds {
		if d.Severity == 1 && d.Range.Start.Line == last && strings.HasPrefix(d.Code, "E") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("no error on line %d: %+v", last, ds)
	}

	// Fixing the buffer clears the file; formatting rewrites a messy one.
	messy := strings.Replace(clean, "\n", "\n\n\n\n", 1)
	want, err := format.Source("billing.jip", messy)
	if err != nil {
		t.Fatal(err)
	}
	change := func(text string) map[string]any {
		return note("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []any{map[string]any{"text": text}}})
	}
	_, msgs = session(t,
		req(1, "initialize", map[string]any{"capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}}}}),
		open,
		change(messy),
		req(2, "textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}, "options": map[string]any{"tabSize": 2, "insertSpaces": true}}),
		req(3, "textDocument/hover?", nil),
	)
	found, ds = diagnostics(t, msgs, uri)
	if !found {
		t.Fatalf("no diagnostics published for the fixed buffer")
	}
	for _, d := range ds {
		if d.Severity == 1 {
			t.Fatalf("fixed buffer still has errors: %+v", ds)
		}
	}
	var edits []textEdit
	for _, m := range msgs {
		switch string(m.ID) {
		case "2":
			json.Unmarshal(m.Result, &edits)
		case "3":
			if m.Error == nil || m.Error.Code != codeMethodNotFound {
				t.Errorf("unknown request: %+v", m)
			}
		}
	}
	if len(edits) != 1 || edits[0].NewText != want || edits[0].Range.Start != (Position{}) {
		t.Fatalf("formatting: %+v", edits)
	}
}

func TestPositions(t *testing.T) {
	line := "aé𝄞x"
	for _, tc := range []struct {
		utf8      bool
		col, char int
	}{
		{false, 1, 0}, {false, 2, 1}, {false, 4, 2}, {false, 8, 4}, {false, 9, 5},
		{true, 4, 3}, {true, 8, 7},
	} {
		s := &Server{utf8: tc.utf8}
		if got := s.character(line, tc.col); got != tc.char {
			t.Errorf("utf8=%v character(col %d) = %d, want %d", tc.utf8, tc.col, got, tc.char)
		}
		if got := s.column(line, tc.char); got != tc.col {
			t.Errorf("utf8=%v column(char %d) = %d, want %d", tc.utf8, tc.char, got, tc.col)
		}
	}
	if got := uriPath(pathURI("/a b/c.jip")); got != "/a b/c.jip" {
		t.Errorf("uri round trip: %q", got)
	}
}

func TestDocumentSymbols(t *testing.T) {
	src := `module m

/// A customer order.
pub type Order { id: Int, total: Int }
  inv total >= 0

pub enum Status { Pending, Paid }

// the size
pub fn size(o: Order) -> Int { 1 }

impl Order {
  const MAX: Int = 3
  pub fn lines(self) -> Int {
    0
  }
}

scenario "size is one" {
  then size(Order { id: 1, total: 2 }) == 1
}
`
	uri := "file:///tmp/nowhere/m.jip"
	_, msgs := session(t,
		req(1, "initialize", map[string]any{"capabilities": map[string]any{}}),
		note("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "jip", "version": 1, "text": src}}),
		req(2, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}}),
	)
	var syms []documentSymbol
	for _, m := range msgs {
		if string(m.ID) == "2" {
			if err := json.Unmarshal(m.Result, &syms); err != nil {
				t.Fatal(err)
			}
		}
	}
	type want struct {
		name           string
		kind           int
		start, end     int // 0-based lines
		selLine, selCh int
		children       int
	}
	wants := []want{
		{"Order", kindStruct, 3, 4, 3, 9, 2},
		{"Status", kindEnum, 6, 6, 6, 9, 2},
		{"size", kindFunction, 9, 9, 9, 7, 0},
		{"impl Order", kindClass, 11, 16, 11, 0, 2},
		{"size is one", kindEvent, 18, 20, 18, 0, 0},
	}
	if len(syms) != len(wants) {
		t.Fatalf("got %d symbols: %+v", len(syms), syms)
	}
	for i, w := range wants {
		g := syms[i]
		if g.Name != w.name || g.Kind != w.kind || g.Range.Start.Line != w.start || g.Range.End.Line != w.end ||
			g.SelectionRange.Start != (Position{w.selLine, w.selCh}) || len(g.Children) != w.children {
			t.Errorf("symbol %d: got %+v, want %+v", i, g, w)
		}
	}
	m := syms[3].Children[1]
	if m.Name != "lines" || m.Kind != kindMethod || m.Range.Start.Line != 13 || m.Range.End.Line != 15 || !strings.Contains(m.Detail, "fn lines(self) -> Int") {
		t.Errorf("method: %+v", m)
	}
	if f := syms[0].Children[1]; f.Name != "total" || f.Detail != "Int" || f.SelectionRange.Start != (Position{3, 26}) {
		t.Errorf("field: %+v", f)
	}
}

// at is the LSP position of the n-th byte of needle on the first line of
// text containing needle (ASCII lines only).
func at(t *testing.T, text, needle string, n int) Position {
	t.Helper()
	for i, l := range strings.Split(text, "\n") {
		if k := strings.Index(l, needle); k >= 0 {
			return Position{i, k + n}
		}
	}
	t.Fatalf("%q not found", needle)
	return Position{}
}

func TestHoverAndDefinition(t *testing.T) {
	root := card(t)
	path := filepath.Join(root, "orders/billing/billing.jip")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	uri := pathURI(path)
	spath := filepath.Join(root, "orders/billing/scenarios.jip")
	sb, err := os.ReadFile(spath)
	if err != nil {
		t.Fatal(err)
	}
	suri := pathURI(spath)
	pos := func(u string, p Position) map[string]any {
		return map[string]any{"textDocument": map[string]any{"uri": u}, "position": p}
	}
	_, msgs := session(t,
		req(1, "initialize", map[string]any{"capabilities": map[string]any{}}),
		note("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "jip", "version": 1, "text": src}}),
		req(2, "textDocument/hover", pos(uri, at(t, src, "let r = charge(", 9))),          // charge
		req(3, "textDocument/hover", pos(uri, at(t, src, "let r = charge(", 4))),          // r
		req(4, "textDocument/hover", pos(uri, at(t, src, "Tier.High", 6))),                // High
		req(5, "textDocument/hover", pos(uri, at(t, src, "self.lines.len()", 6))),         // lines
		req(6, "textDocument/definition", pos(suri, at(t, string(sb), "= withdraw(", 3))), // unopened file
		req(7, "textDocument/definition", pos(uri, at(t, src, "if risk(o)", 8))),          // o
		req(8, "textDocument/hover", pos(uri, at(t, src, "/// Money in minor", 0))),       // nothing
	)
	res := map[string]string{}
	for _, m := range msgs {
		if len(m.ID) > 0 {
			res[string(m.ID)] = string(m.Result)
		}
	}
	hover := func(id, want string) {
		t.Helper()
		var h hoverResult
		if err := json.Unmarshal([]byte(res[id]), &h); err != nil || !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover %s: got %s, want %q", id, res[id], want)
		}
	}
	hover("2", "fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet}")
	hover("2", "Charges the order through the payment gateway.")
	hover("3", "let r: Receipt")
	hover("4", "Tier.High")
	hover("5", "lines: List<Line>")
	var loc location
	if err := json.Unmarshal([]byte(res["6"]), &loc); err != nil || loc.URI != uri || loc.Range.Start != at(t, src, "pub fn withdraw(", 7) {
		t.Errorf("definition of withdraw: %s", res["6"])
	}
	loc = location{}
	if err := json.Unmarshal([]byte(res["7"]), &loc); err != nil || loc.URI != uri || loc.Range.Start != at(t, src, "fn run(self, o: Order)", 13) {
		t.Errorf("definition of o: %s", res["7"])
	}
	if res["8"] != "null" {
		t.Errorf("hover on a comment: %s", res["8"])
	}
}
