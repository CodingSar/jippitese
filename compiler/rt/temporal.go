package rt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Temporal is the workflow runtime backed by a Temporal server: every run
// is a Temporal workflow execution whose history holds the journal. Begin
// starts the execution with the journal header as input, each journal
// write is a signal (`jip.step`, `jip.resolve`), and Finish signals
// `jip.finish`; a worker polling task queue `jip` then completes the
// execution with that result, so Result survives the process. The run's
// body still executes in this process, as under `embedded`: a run whose
// process dies stays open in Temporal and is not resumed. Signals between
// runs are buffered in memory, as under `embedded`.
//
// The client speaks gRPC (Google Remote Procedure Call) over unencrypted
// HTTP/2 with a hand-written protobuf codec, so generated programs keep
// building from the standard library alone.
type Temporal struct {
	Addr      string // host:port of the frontend
	Namespace string

	client  *http.Client
	stop    context.CancelFunc
	pollCtx context.Context
	once    sync.Once
	mu      sync.Mutex
	signals map[string][]any
	results map[RunId]any
}

// TemporalQueue is the task queue jip's worker polls.
const TemporalQueue = "jip"

// NewTemporal is a Temporal runtime for store `host:port[/namespace]`; ""
// and the embedded default `.jip/workflow` mean localhost:7233, namespace
// default.
func NewTemporal(store string) *Temporal {
	addr, ns := store, "default"
	if addr == "" || addr == ".jip/workflow" {
		addr = "localhost:7233"
	}
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr, ns = addr[:i], addr[i+1:]
	}
	tr := &http.Transport{
		Protocols:   new(http.Protocols),
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	tr.Protocols.SetUnencryptedHTTP2(true)
	ctx, stop := context.WithCancel(context.Background())
	return &Temporal{
		Addr: addr, Namespace: ns,
		client:  &http.Client{Transport: tr},
		pollCtx: ctx, stop: stop,
		signals: map[string][]any{}, results: map[RunId]any{},
	}
}

// Close stops the worker.
func (t *Temporal) Close() { t.stop() }

func (t *Temporal) Begin(name string, version int64, sites []string) (RunId, *Journal, error) {
	t.once.Do(func() { go t.work() })
	id := newRunId()
	h, _ := json.Marshal(journalHeader{Workflow: name, Version: version, Sites: sites})
	var req pbuf
	req.str(1, t.Namespace)
	req.str(2, string(id))
	req.msg(3, new(pbuf).str(1, name))
	req.msg(4, t.queue())
	req.msg(5, payloads(h))
	req.str(9, "jip")
	req.str(10, newRequestId())
	if _, err := t.call(context.Background(), "StartWorkflowExecution", req.b); err != nil {
		return "", nil, fmt.Errorf("temporal: start %s: %w", name, err)
	}
	j := NewJournal(name, version, sites)
	j.record = func(op string, e JournalEntry) {
		b, _ := json.Marshal(e)
		if err := t.signal(id, op, b); err != nil {
			panic(fmt.Errorf("temporal: journal %s: %w", name, err))
		}
	}
	return id, j, nil
}

func (t *Temporal) Finish(id RunId, res any) {
	b, err := json.Marshal(res)
	if err != nil {
		b, _ = json.Marshal(err.Error())
	}
	t.mu.Lock()
	t.results[id] = json.RawMessage(b)
	t.mu.Unlock()
	_ = t.signal(id, "jip.finish", b)
}

func (t *Temporal) Result(id RunId) (any, bool) {
	t.mu.Lock()
	v, ok := t.results[id]
	t.mu.Unlock()
	if ok {
		return v, true
	}
	var req pbuf
	req.str(1, t.Namespace)
	req.msg(2, new(pbuf).str(1, string(id)))
	req.uint(6, 2) // HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	resp, err := t.call(context.Background(), "GetWorkflowExecutionHistory", req.b)
	if err != nil {
		return nil, false
	}
	for _, ev := range pfield(pfield1(resp, 1), 1) {
		if typ, _ := pvarint(ev, 3); typ != 2 { // EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
			continue
		}
		if data, ok := payloadData(pfield1(pfield1(ev, 7), 1)); ok {
			return json.RawMessage(data), true
		}
	}
	return nil, false
}

func (t *Temporal) Signal(name string, sig any) {
	t.mu.Lock()
	t.signals[name] = append(t.signals[name], sig)
	t.mu.Unlock()
}

func (t *Temporal) Take(name string, match func(any) bool) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	buf := t.signals[name]
	for i, v := range buf {
		if match(v) {
			t.signals[name] = append(buf[:i:i], buf[i+1:]...)
			return v, true
		}
	}
	return nil, false
}

func (t *Temporal) queue() *pbuf {
	return new(pbuf).str(1, TemporalQueue).uint(2, 1) // TASK_QUEUE_KIND_NORMAL
}

func (t *Temporal) signal(id RunId, name string, data []byte) error {
	var req pbuf
	req.str(1, t.Namespace)
	req.msg(2, new(pbuf).str(1, string(id)))
	req.str(3, name)
	req.msg(4, payloads(data))
	req.str(5, "jip")
	req.str(6, newRequestId())
	_, err := t.call(context.Background(), "SignalWorkflowExecution", req.b)
	return err
}

// work polls task queue jip until Close: an execution whose history holds
// `jip.finish` is completed with that result, any other task is answered
// with no commands.
func (t *Temporal) work() {
	for t.pollCtx.Err() == nil {
		var req pbuf
		req.str(1, t.Namespace)
		req.msg(2, t.queue())
		req.str(3, "jip")
		resp, err := t.call(t.pollCtx, "PollWorkflowTaskQueue", req.b)
		if err != nil {
			select {
			case <-t.pollCtx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		token := pfield1(resp, 1)
		if token == nil {
			continue // the long poll expired
		}
		events := pfield(pfield1(resp, 8), 1)
		for next := pfield1(resp, 9); len(next) > 0; {
			var hr pbuf
			hr.str(1, t.Namespace)
			hr.msg(2, &pbuf{b: pfield1(resp, 2)})
			hr.bytes(4, next)
			page, err := t.call(t.pollCtx, "GetWorkflowExecutionHistory", hr.b)
			if err != nil {
				break
			}
			events = append(events, pfield(pfield1(page, 1), 1)...)
			next = pfield1(page, 3)
		}
		var done pbuf
		done.bytes(1, token)
		for _, ev := range events {
			if typ, _ := pvarint(ev, 3); typ != 26 { // EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED
				continue
			}
			attrs := pfield1(ev, 26)
			if string(pfield1(attrs, 1)) != "jip.finish" {
				continue
			}
			var cmd pbuf
			cmd.uint(1, 4) // COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION
			cmd.msg(4, new(pbuf).bytes(1, pfield1(attrs, 2)))
			done.msg(2, &cmd)
			break
		}
		done.str(3, "jip")
		done.str(9, t.Namespace)
		_, _ = t.call(t.pollCtx, "RespondWorkflowTaskCompleted", done.b)
	}
}

// call is one unary gRPC call of the WorkflowService; long polls get 70
// seconds, every other call 10.
func (t *Temporal) call(ctx context.Context, method string, msg []byte) ([]byte, error) {
	timeout := 10 * time.Second
	if strings.HasPrefix(method, "Poll") {
		timeout = 70 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body := make([]byte, 5+len(msg))
	binary.BigEndian.PutUint32(body[1:5], uint32(len(msg)))
	copy(body[5:], msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+t.Addr+"/temporal.api.workflowservice.v1.WorkflowService/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("Grpc-Timeout", fmt.Sprintf("%dS", int(timeout/time.Second)))
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	status := resp.Trailer.Get("Grpc-Status")
	if status == "" {
		status = resp.Header.Get("Grpc-Status")
	}
	if resp.StatusCode != http.StatusOK || (status != "" && status != "0") {
		m := resp.Trailer.Get("Grpc-Message")
		if m == "" {
			m = resp.Header.Get("Grpc-Message")
		}
		return nil, fmt.Errorf("%s: http %d grpc-status %s: %s", method, resp.StatusCode, status, m)
	}
	if len(data) < 5 {
		return nil, nil
	}
	n := binary.BigEndian.Uint32(data[1:5])
	if data[0] != 0 || int(n) > len(data)-5 {
		return nil, errors.New(method + ": malformed gRPC frame")
	}
	return data[5 : 5+n], nil
}

func newRequestId() string {
	id := newRunId() + newRunId()
	s := string(id)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// payloads is a temporal.api.common.v1.Payloads of one JSON payload.
func payloads(data []byte) *pbuf {
	var meta pbuf
	meta.str(1, "encoding").str(2, "json/plain")
	var p pbuf
	p.msg(1, &meta)
	p.bytes(2, data)
	return new(pbuf).msg(1, &p)
}

// payloadData is the data of the first payload of Payloads ps.
func payloadData(ps []byte) ([]byte, bool) {
	p := pfield1(ps, 1)
	if p == nil {
		return nil, false
	}
	return pfield1(p, 2), true
}

// ---- protobuf ------------------------------------------------------------

// pbuf encodes a protobuf message.
type pbuf struct{ b []byte }

func (p *pbuf) tag(field, wire int) {
	p.b = binary.AppendUvarint(p.b, uint64(field<<3|wire))
}

func (p *pbuf) uint(field int, v uint64) *pbuf {
	p.tag(field, 0)
	p.b = binary.AppendUvarint(p.b, v)
	return p
}

func (p *pbuf) bytes(field int, v []byte) *pbuf {
	p.tag(field, 2)
	p.b = binary.AppendUvarint(p.b, uint64(len(v)))
	p.b = append(p.b, v...)
	return p
}

func (p *pbuf) str(field int, v string) *pbuf { return p.bytes(field, []byte(v)) }

func (p *pbuf) msg(field int, m *pbuf) *pbuf { return p.bytes(field, m.b) }

// pscan calls f with each field of message b: its number, wire type,
// varint value (wire 0) and bytes (wire 2); it stops at malformed input.
func pscan(b []byte, f func(field, wire int, v uint64, data []byte)) {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return
		}
		b = b[n:]
		field, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return
			}
			b = b[n:]
			f(field, wire, v, nil)
		case 1:
			if len(b) < 8 {
				return
			}
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return
			}
			f(field, wire, 0, b[n:n+int(l)])
			b = b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return
			}
			b = b[4:]
		default:
			return
		}
	}
}

// pfield is every length-delimited value of field in b.
func pfield(b []byte, field int) [][]byte {
	var out [][]byte
	pscan(b, func(f, w int, _ uint64, d []byte) {
		if f == field && w == 2 {
			out = append(out, d)
		}
	})
	return out
}

// pfield1 is the last length-delimited value of field in b, or nil.
func pfield1(b []byte, field int) []byte {
	var out []byte
	pscan(b, func(f, w int, _ uint64, d []byte) {
		if f == field && w == 2 {
			out = d
			if out == nil {
				out = []byte{}
			}
		}
	})
	return out
}

// pvarint is the varint value of field in b.
func pvarint(b []byte, field int) (uint64, bool) {
	var out uint64
	var ok bool
	pscan(b, func(f, w int, v uint64, _ []byte) {
		if f == field && w == 0 {
			out, ok = v, true
		}
	})
	return out, ok
}
