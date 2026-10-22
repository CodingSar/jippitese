package rt

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Restate is the workflow runtime backed by a Restate server: every run is
// a key of the virtual object `jip`, whose state holds the run's journal
// header, its entries and its result. Journal writes and results go through
// Restate's ingress; the object's handlers are served by this process on
// an endpoint it registers with Restate's admin API, speaking the service
// protocol in request-response mode. The run's body still executes in this
// process, as under `embedded`: a run whose process dies is not resumed,
// but its journal and result stay in Restate. Signals between runs are
// buffered in memory, as under `embedded`.
type Restate struct {
	Ingress  string // host:port of the ingress
	Admin    string // host:port of the admin API
	Endpoint string // host:port this process serves the object on

	client  *http.Client
	once    sync.Once
	err     error
	srv     *http.Server
	mu      sync.Mutex
	signals map[string][]any
	results map[RunId]any
}

// RestateObject is the virtual object holding jip's runs.
const RestateObject = "jip"

// NewRestate is a Restate runtime for store `ingress[,admin=host:port]
// [,endpoint=host:port]`; "" and the embedded default `.jip/workflow` mean
// ingress localhost:8080. The admin API defaults to port 9070 of the
// ingress host, the endpoint to a free port on 127.0.0.1.
func NewRestate(store string) *Restate {
	if store == "" || store == ".jip/workflow" {
		store = "localhost:8080"
	}
	parts := strings.Split(store, ",")
	r := &Restate{
		Ingress:  parts[0],
		Endpoint: "127.0.0.1:0",
		client:   &http.Client{Timeout: 30 * time.Second},
		signals:  map[string][]any{}, results: map[RunId]any{},
	}
	host, _, err := net.SplitHostPort(r.Ingress)
	if err != nil {
		host = r.Ingress
	}
	r.Admin = net.JoinHostPort(host, "9070")
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(p, "=")
		switch k {
		case "admin":
			r.Admin = v
		case "endpoint":
			r.Endpoint = v
		}
	}
	return r
}

// Close stops the endpoint.
func (r *Restate) Close() {
	if r.srv != nil {
		r.srv.Close()
	}
}

// ready serves the endpoint and registers it, once.
func (r *Restate) ready() error {
	r.once.Do(func() {
		ln, err := net.Listen("tcp", r.Endpoint)
		if err != nil {
			r.err = fmt.Errorf("restate: endpoint: %w", err)
			return
		}
		r.Endpoint = ln.Addr().String()
		mux := http.NewServeMux()
		mux.HandleFunc("GET /discover", restateDiscover)
		mux.HandleFunc("POST /invoke/"+RestateObject+"/{handler}", restateInvoke)
		r.srv = &http.Server{Handler: mux, Protocols: new(http.Protocols)}
		r.srv.Protocols.SetHTTP1(true)
		r.srv.Protocols.SetUnencryptedHTTP2(true)
		go r.srv.Serve(ln)
		body, _ := json.Marshal(map[string]any{"uri": "http://" + r.Endpoint, "force": true, "use_http_11": true})
		resp, err := r.client.Post("http://"+r.Admin+"/deployments", "application/json", bytes.NewReader(body))
		if err != nil {
			r.err = fmt.Errorf("restate: register: %w", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(resp.Body)
			r.err = fmt.Errorf("restate: register: %s: %s", resp.Status, b)
		}
	})
	return r.err
}

// call invokes handler of the run id through the ingress.
func (r *Restate) call(id RunId, handler string, body []byte) ([]byte, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	resp, err := r.client.Post("http://"+r.Ingress+"/"+RestateObject+"/"+string(id)+"/"+handler,
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("restate: %s: %s: %s", handler, resp.Status, b)
	}
	return b, nil
}

func (r *Restate) Begin(name string, version int64, sites []string) (RunId, *Journal, error) {
	id := newRunId()
	h, _ := json.Marshal(journalHeader{Workflow: name, Version: version, Sites: sites})
	if _, err := r.call(id, "begin", h); err != nil {
		return "", nil, fmt.Errorf("restate: start %s: %w", name, err)
	}
	j := NewJournal(name, version, sites)
	j.record = func(op string, e JournalEntry) {
		b, _ := json.Marshal(e)
		if _, err := r.call(id, "entry", b); err != nil {
			panic(fmt.Errorf("restate: journal %s: %w", name, err))
		}
	}
	return id, j, nil
}

func (r *Restate) Finish(id RunId, res any) {
	b, err := json.Marshal(res)
	if err != nil {
		b, _ = json.Marshal(err.Error())
	}
	r.mu.Lock()
	r.results[id] = json.RawMessage(b)
	r.mu.Unlock()
	_, _ = r.call(id, "finish", b)
}

func (r *Restate) Result(id RunId) (any, bool) {
	r.mu.Lock()
	v, ok := r.results[id]
	r.mu.Unlock()
	if ok {
		return v, true
	}
	b, err := r.call(id, "result", nil)
	if err != nil || len(b) == 0 {
		return nil, false
	}
	return json.RawMessage(b), true
}

// Journal reads the journal of run id back from Restate.
func (r *Restate) Journal(id RunId) (*Journal, error) {
	b, err := r.call(id, "journal", nil)
	if err != nil {
		return nil, err
	}
	var rec struct {
		Header  journalHeader
		Entries []JournalEntry
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	j := NewJournal(rec.Header.Workflow, rec.Header.Version, rec.Header.Sites)
	j.Entries = rec.Entries
	return j, nil
}

func (r *Restate) Signal(name string, sig any) {
	r.mu.Lock()
	r.signals[name] = append(r.signals[name], sig)
	r.mu.Unlock()
}

func (r *Restate) Take(name string, match func(any) bool) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	buf := r.signals[name]
	for i, v := range buf {
		if match(v) {
			r.signals[name] = append(buf[:i:i], buf[i+1:]...)
			return v, true
		}
	}
	return nil, false
}

// ---- endpoint ------------------------------------------------------------

// The object's handlers: begin, entry and finish write state, result and
// journal read it.
var restateHandlers = []struct{ name, ty string }{
	{"begin", "EXCLUSIVE"}, {"entry", "EXCLUSIVE"}, {"finish", "EXCLUSIVE"},
	{"result", "SHARED"}, {"journal", "SHARED"},
}

func restateDiscover(w http.ResponseWriter, req *http.Request) {
	ct := ""
	for _, v := range []string{"v1", "v2", "v3"} {
		if c := "application/vnd.restate.endpointmanifest." + v + "+json"; strings.Contains(req.Header.Get("Accept"), c) {
			ct = c
		}
	}
	if ct == "" {
		http.Error(w, "unsupported discovery protocol", http.StatusUnsupportedMediaType)
		return
	}
	var hs []map[string]any
	for _, h := range restateHandlers {
		hs = append(hs, map[string]any{"name": h.name, "ty": h.ty})
	}
	w.Header().Set("Content-Type", ct)
	json.NewEncoder(w).Encode(map[string]any{
		"protocolMode": "REQUEST_RESPONSE", "minProtocolVersion": 5, "maxProtocolVersion": 7,
		"services": []any{map[string]any{"name": RestateObject, "ty": "VIRTUAL_OBJECT", "handlers": hs}},
	})
}

// Service protocol message types.
const (
	rsStart    = 0x0000
	rsError    = 0x0002
	rsEnd      = 0x0003
	rsInput    = 0x0400
	rsOutput   = 0x0401
	rsSetState = 0x0403
)

// restateInvoke runs one handler invocation: it reads the start message
// (with the object's state) and the input, and answers with the state
// writes, the output and the end message. A retried invocation replays the
// commands it already sent; those are skipped.
func restateInvoke(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return
	}
	var known uint64
	state := map[string][]byte{}
	var partial bool
	var input []byte
	for len(body) >= 8 {
		hd := binary.BigEndian.Uint64(body[:8])
		n := uint64(uint32(hd))
		if uint64(len(body)-8) < n {
			break
		}
		msg := body[8 : 8+n]
		body = body[8+n:]
		switch uint16(hd >> 48) {
		case rsStart:
			known, _ = pvarint(msg, 3)
			for _, e := range pfield(msg, 4) {
				state[string(pfield1(e, 1))] = pfield1(e, 2)
			}
			if v, _ := pvarint(msg, 5); v != 0 {
				partial = true
			}
		case rsInput:
			input = pfield1(pfield1(msg, 14), 1)
		}
	}
	var out bytes.Buffer
	frame := func(ty uint16, m *pbuf) {
		var hd [8]byte
		binary.BigEndian.PutUint64(hd[:], uint64(ty)<<48|uint64(len(m.b)))
		out.Write(hd[:])
		out.Write(m.b)
	}
	var cmds []func()
	set := func(key string, v []byte) {
		cmds = append(cmds, func() {
			frame(rsSetState, new(pbuf).bytes(1, []byte(key)).msg(3, new(pbuf).bytes(1, v)))
		})
	}
	output := func(v []byte) {
		cmds = append(cmds, func() { frame(rsOutput, new(pbuf).msg(14, new(pbuf).bytes(1, v))) })
	}
	switch req.PathValue("handler") {
	case "begin":
		set("header", input)
		output(nil)
	case "entry":
		var e struct{ Seq int }
		_ = json.Unmarshal(input, &e)
		set(fmt.Sprintf("e/%08d", e.Seq), input)
		output(nil)
	case "finish":
		set("result", input)
		output(nil)
	case "result", "journal":
		if partial {
			frame(rsError, new(pbuf).uint(1, 500).str(2, "jip: partial state; lazy state is not supported"))
			break
		}
		if req.PathValue("handler") == "result" {
			output(state["result"])
			break
		}
		var keys []string
		for k := range state {
			if strings.HasPrefix(k, "e/") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var sb strings.Builder
		sb.WriteString(`{"header":`)
		if h := state["header"]; len(h) > 0 {
			sb.Write(h)
		} else {
			sb.WriteString("null")
		}
		sb.WriteString(`,"entries":[`)
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.Write(state[k])
		}
		sb.WriteString("]}")
		output([]byte(sb.String()))
	default:
		frame(rsError, new(pbuf).uint(1, 404).str(2, "jip: unknown handler "+req.PathValue("handler")))
	}
	// known counts the start-adjacent input too: commands already
	// journaled by an earlier attempt are not sent again.
	for i, c := range cmds {
		if uint64(i+1) >= known {
			c()
		}
	}
	frame(rsEnd, new(pbuf))
	w.Header().Set("Content-Type", req.Header.Get("Content-Type"))
	w.Write(out.Bytes())
}
