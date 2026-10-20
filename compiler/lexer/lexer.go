// Package lexer turns .jip source into tokens per spec section 01.
package lexer

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind classifies a token.
type Kind int

const (
	EOF  Kind = iota
	Term      // statement terminator (newline or ';')
	Ident
	Keyword
	Int
	Float
	Str    // raw text between quotes, escapes and interpolation unprocessed
	RawStr // r"…"
	Bytes  // b"…"
	Char
	Duration // value in nanoseconds in Num
	Op
	Doc // /// line, text in Text
	Tag // @name
)

// Pos is a source position.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string { return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col) }

// Token is one lexical token.
type Token struct {
	Kind Kind
	Text string // identifier, keyword, operator or literal source text
	Num  int64  // Int, Char (rune), Duration (ns)
	F    float64
	Pos  Pos
}

func (t Token) String() string {
	switch t.Kind {
	case EOF:
		return "end of file"
	case Term:
		return "end of line"
	case Str:
		return "string literal"
	}
	return "`" + t.Text + "`"
}

// Keywords is the reserved word set from section 01.
var Keywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields("module use pub fn type enum impl behaviour for let var if else match while in return break continue spawn task with ret old forall exists scenario given when then any where via extern go unsafe pure app env const self Self llm decide approve true false") {
		Keywords[k] = true
	}
}

// Error is a lexical error.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

var ops = []string{"..=", "::<", "&&", "||", "==", "!=", "<=", ">=", "+=", "-=", "*=", "/=", "->", "=>", "..", "::",
	"+", "-", "*", "/", "%", "<", ">", "!", "=", "?", ".", ",", ":", "|", "@", "(", ")", "[", "]", "{", "}"}

// Lex tokenizes src. Terminators are inserted per section 01 rule 7.
func Lex(file, src string) ([]Token, error) {
	l := &lexer{file: file, src: src, line: 1, col: 1}
	return l.run()
}

type lexer struct {
	file      string
	src       string
	i         int
	line, col int
	toks      []Token
}

func (l *lexer) pos() Pos { return Pos{l.file, l.line, l.col} }

func (l *lexer) peek(off int) byte {
	if l.i+off < len(l.src) {
		return l.src[l.i+off]
	}
	return 0
}

func (l *lexer) advance(n int) {
	for k := 0; k < n && l.i < len(l.src); k++ {
		if l.src[l.i] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.i++
	}
}

func (l *lexer) emit(t Token) { l.toks = append(l.toks, t) }

func (l *lexer) lastAllowsTerm() bool {
	if len(l.toks) == 0 {
		return false
	}
	t := l.toks[len(l.toks)-1]
	switch t.Kind {
	case Ident, Int, Float, Str, RawStr, Bytes, Char, Duration:
		return true
	case Keyword:
		return t.Text == "break" || t.Text == "continue" || t.Text == "return" || t.Text == "true" || t.Text == "false" || t.Text == "self" || t.Text == "Self" || t.Text == "ret"
	case Op:
		return t.Text == ")" || t.Text == "]" || t.Text == "}" || t.Text == "?"
	}
	return false
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }
func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }

func (l *lexer) run() ([]Token, error) {
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == '\n':
			if l.lastAllowsTerm() {
				l.emit(Token{Kind: Term, Text: "\n", Pos: l.pos()})
			}
			l.advance(1)
		case c == ' ' || c == '\t' || c == '\r':
			l.advance(1)
		case c == ';':
			l.emit(Token{Kind: Term, Text: ";", Pos: l.pos()})
			l.advance(1)
		case c == '/' && l.peek(1) == '/':
			p := l.pos()
			start := l.i
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance(1)
			}
			text := l.src[start:l.i]
			if strings.HasPrefix(text, "///") {
				l.emit(Token{Kind: Doc, Text: strings.TrimSpace(strings.TrimPrefix(text, "///")), Pos: p})
			}
		case c == '@' && isIdentStart(l.peek(1)):
			p := l.pos()
			l.advance(1)
			start := l.i
			for l.i < len(l.src) && isIdentChar(l.src[l.i]) {
				l.advance(1)
			}
			l.emit(Token{Kind: Tag, Text: l.src[start:l.i], Pos: p})
		case c == '"':
			if err := l.str(Str); err != nil {
				return nil, err
			}
		case (c == 'r' || c == 'b') && l.peek(1) == '"':
			k := RawStr
			if c == 'b' {
				k = Bytes
			}
			l.advance(1)
			if err := l.str(k); err != nil {
				return nil, err
			}
		case c == '\'':
			p := l.pos()
			l.advance(1)
			var r rune
			if l.peek(0) == '\\' {
				s, n, err := decodeEscape(l.src[l.i:])
				if err != nil {
					return nil, &Error{p, err.Error()}
				}
				r = []rune(s)[0]
				l.advance(n)
			} else {
				rs := []rune(l.src[l.i:])
				r = rs[0]
				l.advance(len(string(r)))
			}
			if l.peek(0) != '\'' {
				return nil, &Error{p, "unterminated char literal"}
			}
			l.advance(1)
			l.emit(Token{Kind: Char, Text: string(r), Num: int64(r), Pos: p})
		case isDigit(c):
			if err := l.number(); err != nil {
				return nil, err
			}
		case isIdentStart(c):
			p := l.pos()
			start := l.i
			for l.i < len(l.src) && isIdentChar(l.src[l.i]) {
				l.advance(1)
			}
			text := l.src[start:l.i]
			k := Ident
			if Keywords[text] {
				k = Keyword
			}
			l.emit(Token{Kind: k, Text: text, Pos: p})
		default:
			p := l.pos()
			matched := false
			for _, op := range ops {
				if strings.HasPrefix(l.src[l.i:], op) {
					l.emit(Token{Kind: Op, Text: op, Pos: p})
					l.advance(len(op))
					matched = true
					break
				}
			}
			if !matched {
				return nil, &Error{p, fmt.Sprintf("unexpected character %q", c)}
			}
		}
	}
	if l.lastAllowsTerm() {
		l.emit(Token{Kind: Term, Text: "\n", Pos: l.pos()})
	}
	l.emit(Token{Kind: EOF, Pos: l.pos()})
	return l.toks, nil
}

// str lexes a quoted literal starting at the opening quote; the token text is
// the raw content without quotes (escapes are left for the parser so that
// interpolation braces can be found).
func (l *lexer) str(k Kind) error {
	p := l.pos()
	l.advance(1)
	start := l.i
	for {
		if l.i >= len(l.src) || l.src[l.i] == '\n' {
			return &Error{p, "unterminated string literal"}
		}
		c := l.src[l.i]
		if c == '\\' && k != RawStr {
			l.advance(2)
			continue
		}
		if c == '"' {
			break
		}
		l.advance(1)
	}
	text := l.src[start:l.i]
	l.advance(1)
	l.emit(Token{Kind: k, Text: text, Pos: p})
	return nil
}

var durUnits = map[string]int64{"ns": 1, "us": 1000, "ms": 1000000, "s": 1000000000, "m": 60000000000, "h": 3600000000000}

func (l *lexer) number() error {
	p := l.pos()
	start := l.i
	if l.peek(0) == '0' && (l.peek(1) == 'x' || l.peek(1) == 'X') {
		l.advance(2)
		for l.i < len(l.src) && (isDigit(l.src[l.i]) || strings.IndexByte("abcdefABCDEF_", l.src[l.i]) >= 0) {
			l.advance(1)
		}
		v, err := strconv.ParseInt(strings.ReplaceAll(l.src[start+2:l.i], "_", ""), 16, 64)
		if err != nil {
			return &Error{p, "bad hex literal"}
		}
		l.emit(Token{Kind: Int, Text: l.src[start:l.i], Num: v, Pos: p})
		return nil
	}
	for l.i < len(l.src) && (isDigit(l.src[l.i]) || l.src[l.i] == '_') {
		l.advance(1)
	}
	isFloat := false
	if l.peek(0) == '.' && isDigit(l.peek(1)) {
		isFloat = true
		l.advance(1)
		for l.i < len(l.src) && (isDigit(l.src[l.i]) || l.src[l.i] == '_') {
			l.advance(1)
		}
	}
	if (l.peek(0) == 'e' || l.peek(0) == 'E') && (isDigit(l.peek(1)) || ((l.peek(1) == '-' || l.peek(1) == '+') && isDigit(l.peek(2)))) {
		isFloat = true
		l.advance(2)
		for l.i < len(l.src) && isDigit(l.src[l.i]) {
			l.advance(1)
		}
	}
	text := strings.ReplaceAll(l.src[start:l.i], "_", "")
	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return &Error{p, "bad float literal"}
		}
		l.emit(Token{Kind: Float, Text: l.src[start:l.i], F: f, Pos: p})
		return nil
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return &Error{p, "integer literal out of range"}
	}
	// duration suffix
	us := l.i
	for us < len(l.src) && isIdentChar(l.src[us]) {
		us++
	}
	if us > l.i {
		unit := l.src[l.i:us]
		mul, ok := durUnits[unit]
		if !ok {
			return &Error{p, fmt.Sprintf("unknown duration unit %q", unit)}
		}
		l.advance(us - l.i)
		l.emit(Token{Kind: Duration, Text: l.src[start:l.i], Num: v * mul, Pos: p})
		return nil
	}
	l.emit(Token{Kind: Int, Text: l.src[start:l.i], Num: v, Pos: p})
	return nil
}

// decodeEscape decodes one escape sequence at the start of s and returns the
// decoded text and the number of source bytes consumed.
func decodeEscape(s string) (string, int, error) {
	if len(s) < 2 || s[0] != '\\' {
		return "", 0, fmt.Errorf("bad escape")
	}
	switch s[1] {
	case 'n':
		return "\n", 2, nil
	case 't':
		return "\t", 2, nil
	case 'r':
		return "\r", 2, nil
	case '\\':
		return "\\", 2, nil
	case '"':
		return "\"", 2, nil
	case '\'':
		return "'", 2, nil
	case '0':
		return "\x00", 2, nil
	case 'u':
		if len(s) < 4 || s[2] != '{' {
			return "", 0, fmt.Errorf("bad \\u escape")
		}
		end := strings.IndexByte(s, '}')
		if end < 0 {
			return "", 0, fmt.Errorf("bad \\u escape")
		}
		v, err := strconv.ParseInt(s[3:end], 16, 32)
		if err != nil {
			return "", 0, fmt.Errorf("bad \\u escape")
		}
		return string(rune(v)), end + 1, nil
	}
	return "", 0, fmt.Errorf("unknown escape \\%c", s[1])
}

// StrPart is one piece of a string literal: literal text or an interpolated
// expression source with its position.
type StrPart struct {
	Lit    string
	Expr   string
	IsExpr bool
	Pos    Pos
}

// SplitStr splits the raw content of a Str token into literal and
// interpolation parts; `{{` and `}}` are literal braces.
func SplitStr(raw string, p Pos) ([]StrPart, error) {
	var parts []StrPart
	var lit strings.Builder
	col := p.Col + 1
	i := 0
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == '\\':
			s, n, err := decodeEscape(raw[i:])
			if err != nil {
				return nil, &Error{Pos{p.File, p.Line, col}, err.Error()}
			}
			lit.WriteString(s)
			i += n
			col += n
		case c == '{' && i+1 < len(raw) && raw[i+1] == '{':
			lit.WriteByte('{')
			i += 2
			col += 2
		case c == '}' && i+1 < len(raw) && raw[i+1] == '}':
			lit.WriteByte('}')
			i += 2
			col += 2
		case c == '{':
			if lit.Len() > 0 {
				parts = append(parts, StrPart{Lit: lit.String()})
				lit.Reset()
			}
			depth := 1
			j := i + 1
			for j < len(raw) && depth > 0 {
				if raw[j] == '{' {
					depth++
				} else if raw[j] == '}' {
					depth--
				}
				j++
			}
			if depth != 0 {
				return nil, &Error{Pos{p.File, p.Line, col}, "unterminated interpolation; a literal `{` is written `{{`"}
			}
			parts = append(parts, StrPart{Expr: raw[i+1 : j-1], IsExpr: true, Pos: Pos{p.File, p.Line, col + 1}})
			col += j - i
			i = j
		case c == '}':
			return nil, &Error{Pos{p.File, p.Line, col}, "unmatched `}` in string; write `}}`"}
		default:
			lit.WriteByte(c)
			i++
			col++
		}
	}
	if lit.Len() > 0 || len(parts) == 0 {
		parts = append(parts, StrPart{Lit: lit.String()})
	}
	return parts, nil
}

// DecodeStr decodes the escapes of a raw string body without interpolation
// (used for bytes literals, where `{` is an ordinary byte).
func DecodeStr(raw string, p Pos) (string, error) {
	var sb strings.Builder
	i := 0
	for i < len(raw) {
		if raw[i] == '\\' {
			s, n, err := decodeEscape(raw[i:])
			if err != nil {
				return "", &Error{p, err.Error()}
			}
			sb.WriteString(s)
			i += n
			continue
		}
		sb.WriteByte(raw[i])
		i++
	}
	return sb.String(), nil
}
