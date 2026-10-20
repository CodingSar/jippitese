package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// message is one JSON-RPC 2.0 request, notification or response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// readMessage reads one Content-Length framed message.
func readMessage(r *bufio.Reader) (*message, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			if n, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, fmt.Errorf("bad Content-Length %q", v)
			}
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("message without Content-Length")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	m := &message{}
	if err := json.Unmarshal(body, m); err != nil {
		return &message{Error: &rpcError{codeParseError, err.Error()}}, nil
	}
	return m, nil
}

// writeMessage writes m with its Content-Length header.
func writeMessage(w io.Writer, m *message) error {
	m.JSONRPC = "2.0"
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}
