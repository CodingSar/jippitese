package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Position and Range are LSP's zero-based line and character offsets; the
// character unit is UTF-16 code units unless UTF-8 was negotiated.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// lineAt is line n (1-based) of text without its terminator.
func lineAt(text string, n int) string {
	for i := 1; i < n; i++ {
		j := strings.IndexByte(text, '\n')
		if j < 0 {
			return ""
		}
		text = text[j+1:]
	}
	if j := strings.IndexByte(text, '\n'); j >= 0 {
		text = text[:j]
	}
	return strings.TrimSuffix(text, "\r")
}

// character converts a 1-based byte column of line to an LSP character.
func (s *Server) character(line string, col int) int {
	b := min(max(col-1, 0), len(line))
	if s.utf8 {
		return b
	}
	n := 0
	for _, r := range line[:b] {
		n += utf16Len(r)
	}
	return n
}

// column converts an LSP character on line to a 1-based byte column.
func (s *Server) column(line string, char int) int {
	if s.utf8 {
		return min(max(char, 0), len(line)) + 1
	}
	n := 0
	for i, r := range line {
		if n >= char {
			return i + 1
		}
		n += utf16Len(r)
	}
	return len(line) + 1
}

func utf16Len(r rune) int {
	if r >= 0x10000 && r <= utf8.MaxRune {
		return 2
	}
	return 1
}

// position converts a 1-based line and byte column of text.
func (s *Server) position(text string, line, col int) Position {
	return Position{Line: max(line-1, 0), Character: s.character(lineAt(text, line), col)}
}

// endOf is the position after the last character of text.
func (s *Server) endOf(text string) Position {
	n := strings.Count(text, "\n")
	last := text[strings.LastIndexByte(text, '\n')+1:]
	return Position{Line: n, Character: s.character(last, len(last)+1)}
}

// uriPath is the file path of a file:// URI, "" for other schemes.
func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

// pathURI is the file:// URI of an absolute path.
func pathURI(p string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(p)}).String()
}
