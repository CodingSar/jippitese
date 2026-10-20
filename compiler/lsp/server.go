// Package lsp is `jip lsp`: a Language Server Protocol server over stdio.
// It checks the open buffers together with the rest of their app on every
// change and publishes the diagnostics `jip check` would print, and formats
// with `jip fmt`.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/format"
	"github.com/codingsar/jippitese/compiler/modules"
	"github.com/codingsar/jippitese/compiler/parser"
)

// Server is one client connection. Messages are handled in order on one
// goroutine, so no state is shared.
type Server struct {
	out       io.Writer
	log       io.Writer
	utf8      bool
	shutdown  bool
	docs      map[string]string          // URI → text of open buffers
	published map[string]map[string]bool // analysis root → URIs holding diagnostics
}

// Run serves one client on in and out until `exit` or end of input, and
// returns the process exit code: 0 after `shutdown`, 1 otherwise.
func Run(in io.Reader, out, log io.Writer) int {
	s := &Server{out: out, log: log, docs: map[string]string{}, published: map[string]map[string]bool{}}
	r := bufio.NewReader(in)
	for {
		m, err := readMessage(r)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(log, "jip lsp: %v\n", err)
			}
			return 1
		}
		if m.Method == "exit" {
			if s.shutdown {
				return 0
			}
			return 1
		}
		s.handle(m)
	}
}

// handle answers a request or acts on a notification.
func (s *Server) handle(m *message) {
	if m.Error != nil {
		s.reply(nil, nil, m.Error)
		return
	}
	result, rerr := s.dispatch(m)
	if len(m.ID) == 0 {
		if rerr != nil {
			fmt.Fprintf(s.log, "jip lsp: %s: %s\n", m.Method, rerr.Message)
		}
		return
	}
	s.reply(m.ID, result, rerr)
}

func (s *Server) reply(id json.RawMessage, result any, rerr *rpcError) {
	resp := &message{ID: id, Error: rerr}
	if id == nil {
		resp.ID = json.RawMessage("null")
	}
	if rerr == nil {
		b, err := json.Marshal(result)
		if err != nil {
			resp.Error = &rpcError{codeInvalidRequest, err.Error()}
		} else {
			resp.Result = b
		}
	}
	s.send(resp)
}

func (s *Server) notify(method string, params any) {
	b, _ := json.Marshal(params)
	s.send(&message{Method: method, Params: b})
}

func (s *Server) send(m *message) {
	if err := writeMessage(s.out, m); err != nil {
		fmt.Fprintf(s.log, "jip lsp: %v\n", err)
	}
}

type textDocument struct {
	URI     string `json:"uri"`
	Text    string `json:"text"`
	Version int    `json:"version"`
}

type docParams struct {
	TextDocument   textDocument `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
	Position Position `json:"position"`
}

// dispatch runs one method. Unknown requests are an error; unknown
// notifications are ignored.
func (s *Server) dispatch(m *message) (any, *rpcError) {
	var p docParams
	if len(m.Params) > 0 && m.Method != "initialize" {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, err.Error()}
		}
	}
	uri := p.TextDocument.URI
	switch m.Method {
	case "initialize":
		return s.initialize(m.Params), nil
	case "initialized", "$/cancelRequest", "$/setTrace", "workspace/didChangeConfiguration":
		return nil, nil
	case "shutdown":
		s.shutdown = true
		return nil, nil
	case "textDocument/didOpen":
		s.docs[uri] = p.TextDocument.Text
		s.analyze(uri)
	case "textDocument/didChange":
		if n := len(p.ContentChanges); n > 0 {
			s.docs[uri] = p.ContentChanges[n-1].Text
		}
		s.analyze(uri)
	case "textDocument/didSave":
		s.analyze(uri)
	case "textDocument/didClose":
		delete(s.docs, uri)
		s.analyze(uri)
	case "textDocument/formatting":
		return s.formatting(uri), nil
	case "textDocument/documentSymbol":
		return s.documentSymbols(uri), nil
	default:
		if len(m.ID) > 0 {
			return nil, &rpcError{codeMethodNotFound, "unsupported method " + m.Method}
		}
	}
	return nil, nil
}

func (s *Server) initialize(params json.RawMessage) any {
	var p struct {
		Capabilities struct {
			General struct {
				PositionEncodings []string `json:"positionEncodings"`
			} `json:"general"`
		} `json:"capabilities"`
	}
	json.Unmarshal(params, &p)
	enc := "utf-16"
	if slices.Contains(p.Capabilities.General.PositionEncodings, "utf-8") {
		s.utf8, enc = true, "utf-8"
	}
	return map[string]any{
		"capabilities": map[string]any{
			"positionEncoding": enc,
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1, // full text on every change
				"save":      map[string]any{"includeText": false},
			},
			"documentFormattingProvider": true,
			"documentSymbolProvider":     true,
		},
		"serverInfo": map[string]any{"name": "jip"},
	}
}

// text is the open buffer of uri.
func (s *Server) text(uri string) (string, bool) {
	if t, ok := s.docs[uri]; ok {
		return t, true
	}
	return "", false
}

type lspDiagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// analyze checks the app holding uri with every open buffer in place of
// its file, and publishes diagnostics for each file of the app; files that
// had diagnostics and have none now are cleared. A file outside any app is
// only parsed.
func (s *Server) analyze(uri string) {
	path := uriPath(uri)
	if path == "" {
		return
	}
	root, err := modules.FindRoot(filepath.Dir(path))
	bag := diag.NewBag()
	if err != nil {
		root = path // a lone file is its own analysis unit
		if t, ok := s.text(uri); ok {
			bag.AddSource(filepath.Base(path), t)
			if _, err := parser.ParseFile(filepath.Base(path), t); err != nil {
				if pe, ok := err.(*parser.Error); ok {
					bag.Errorf(pe.Code, pe.Pos, "%s", pe.Msg)
				}
			}
		}
		s.publish(root, filepath.Dir(path), bag)
		return
	}
	overlay := map[string]string{}
	for u, t := range s.docs {
		if p := uriPath(u); p != "" {
			if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
				overlay[filepath.ToSlash(rel)] = t
			}
		}
	}
	s.check(root, overlay, bag)
	s.publish(root, root, bag)
}

// check loads and type-checks; a checker panic becomes a diagnostic so the
// server survives a half-typed buffer.
func (s *Server) check(root string, overlay map[string]string, bag *diag.Bag) (prog *checker.Program) {
	defer func() {
		if r := recover(); r != nil {
			bag.Errorf("E9999", ast.Pos{}, "internal: checker panic: %v", r)
			prog = nil
		}
	}()
	in, err := modules.LoadOverlay(root, overlay, bag)
	if err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
		return nil
	}
	if bag.HasErrors() {
		return nil
	}
	return checker.Check(in, bag)
}

// publish sends bag's diagnostics grouped by file; File paths in bag are
// relative to dir. Diagnostics with no file go to app.jip.
func (s *Server) publish(root, dir string, bag *diag.Bag) {
	byURI := map[string][]lspDiagnostic{}
	for _, d := range bag.Diags {
		file := d.Pos.File
		if strings.HasPrefix(file, "<std>") {
			continue
		}
		if file == "" {
			file = "app.jip"
		}
		src := bag.Sources[file]
		start := s.position(src, d.Pos.Line, d.Pos.Col)
		var end Position
		if d.End.Line > 0 {
			end = s.position(src, d.End.Line, d.End.Col)
		} else {
			line := lineAt(src, d.Pos.Line)
			end = s.position(src, d.Pos.Line, d.Pos.Col+diag.TokenWidth(line, d.Pos.Col))
		}
		msg := d.Message
		if d.Caption != "" {
			msg += ": " + d.Caption
		}
		for _, n := range d.Notes {
			msg += "\nnote: " + n
		}
		for _, h := range d.Help {
			msg += "\nhelp: " + h.Text
		}
		sev := 1
		if d.Severity == diag.Warning {
			sev = 2
		}
		u := pathURI(filepath.Join(dir, filepath.FromSlash(file)))
		byURI[u] = append(byURI[u], lspDiagnostic{Range{start, end}, sev, d.Code, "jip", msg})
	}
	prev := s.published[root]
	now := map[string]bool{}
	for u, ds := range byURI {
		now[u] = true
		s.notify("textDocument/publishDiagnostics", map[string]any{"uri": u, "diagnostics": ds})
	}
	for u := range prev {
		if !now[u] {
			s.notify("textDocument/publishDiagnostics", map[string]any{"uri": u, "diagnostics": []lspDiagnostic{}})
		}
	}
	s.published[root] = now
}

type textEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// formatting replaces the whole buffer with its `jip fmt` form; a buffer
// that does not parse is left alone.
func (s *Server) formatting(uri string) []textEdit {
	t, ok := s.text(uri)
	if !ok {
		return nil
	}
	out, err := format.Source(filepath.Base(uriPath(uri)), t)
	if err != nil || out == t {
		return []textEdit{}
	}
	return []textEdit{{Range{Position{}, s.endOf(t)}, out}}
}
