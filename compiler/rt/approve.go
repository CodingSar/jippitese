package rt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ApproveHttpChannel is the one `effects.approve.channel` v1 ships.
const ApproveHttpChannel = "std.approve.http"

// DefaultApproveAddr is where `std.approve.http` listens unless
// JIP_APPROVE_ADDR names another loopback address.
const DefaultApproveAddr = "127.0.0.1:8089"

// ApproveHttp is `std.approve.http`: a loopback HTTP endpoint that lists
// pending reviews and takes one answer each. It has no authentication,
// so it only listens on a loopback address.
//
//	GET  /reviews              pending reviews, oldest first
//	GET  /reviews/{id}         one review
//	POST /reviews/{id}/approve body: the decision value as JSON
//	POST /reviews/{id}/reject  body: optional {"reason": "…"}
type ApproveHttp struct {
	Addr    string // the bound address
	mu      sync.Mutex
	next    int64
	pending map[string]*pendingReview
	srv     *http.Server
}

type pendingReview struct {
	id       string
	review   Review
	out      reflect.Type // Result[D, Rejected]
	deadline time.Time
	done     chan any
}

// reviewView is the JSON a reviewer reads.
type reviewView struct {
	Id       string       `json:"id"`
	Title    string       `json:"title"`
	Body     string       `json:"body"`
	Data     Option[Json] `json:"data"`
	Deadline string       `json:"deadline"`
}

func (p *pendingReview) view() reviewView {
	return reviewView{Id: p.id, Title: p.review.Title, Body: p.review.Body, Data: p.review.Data, Deadline: p.deadline.UTC().Format(time.RFC3339)}
}

// approveAddr reads JIP_APPROVE_ADDR, refusing a non-loopback host.
func approveAddr() (string, error) {
	addr := os.Getenv("JIP_APPROVE_ADDR")
	if addr == "" {
		return DefaultApproveAddr, nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("JIP_APPROVE_ADDR=%q: %v", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("JIP_APPROVE_ADDR=%q: std.approve.http has no authentication and listens on loopback only", addr)
	}
	return addr, nil
}

// NewApproveHttp listens on addr and serves the review endpoint.
func NewApproveHttp(addr string) (*ApproveHttp, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("std.approve.http: %v", err)
	}
	a := &ApproveHttp{Addr: ln.Addr().String(), pending: map[string]*pendingReview{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /reviews", a.list)
	mux.HandleFunc("GET /reviews/{id}", a.one)
	mux.HandleFunc("POST /reviews/{id}/approve", a.approve)
	mux.HandleFunc("POST /reviews/{id}/reject", a.reject)
	a.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go a.srv.Serve(ln)
	return a, nil
}

// Close stops the endpoint.
func (a *ApproveHttp) Close() error { return a.srv.Close() }

// Ask lists the review and blocks until a reviewer answers or the
// timeout (the review's, else effects.approve.default_timeout) rejects it
// with `timed_out: true`.
func (a *ApproveHttp) Ask(ctx *Ctx, review Review, out reflect.Type) any {
	timeout := ctx.Effects.ApproveTimeout
	if review.Timeout.Tag == OptionTag_Some {
		timeout = review.Timeout.Some_0
	}
	a.mu.Lock()
	a.next++
	p := &pendingReview{id: strconv.FormatInt(a.next, 10), review: review, out: out, deadline: time.Now().Add(timeout), done: make(chan any, 1)}
	a.pending[p.id] = p
	a.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var cancel <-chan struct{}
	if ctx.Go != nil {
		cancel = ctx.Go.Done()
	}
	select {
	case v := <-p.done:
		return v
	case <-timer.C:
		if !a.take(p.id) {
			return <-p.done // a reviewer answered first
		}
		v, _ := answerOf(out, `{"Err":{"reason":null,"timed_out":true}}`)
		return v
	case <-cancel:
		a.take(p.id)
		panic(fmt.Errorf("approve %q: shut down while pending", review.Title))
	}
}

// take removes a pending review; false if it was already answered.
func (a *ApproveHttp) take(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.pending[id]; !ok {
		return false
	}
	delete(a.pending, id)
	return true
}

// answerOf decodes a Result JSON into a value of type out.
func answerOf(out reflect.Type, js string) (any, error) {
	v := reflect.New(out)
	if err := json.Unmarshal([]byte(js), v.Interface()); err != nil {
		return nil, err
	}
	return v.Elem().Interface(), nil
}

func (a *ApproveHttp) list(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	views := []reviewView{}
	for _, p := range a.pending {
		views = append(views, p.view())
	}
	a.mu.Unlock()
	sort.Slice(views, func(i, j int) bool {
		x, _ := strconv.Atoi(views[i].Id)
		y, _ := strconv.Atoi(views[j].Id)
		return x < y
	})
	writeJSON(w, http.StatusOK, views)
}

func (a *ApproveHttp) one(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	p := a.pending[r.PathValue("id")]
	a.mu.Unlock()
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pending review " + r.PathValue("id")})
		return
	}
	writeJSON(w, http.StatusOK, p.view())
}

// answer resolves a pending review with the Result JSON built by mk from
// the request body; a body that does not decode leaves it pending.
func (a *ApproveHttp) answer(w http.ResponseWriter, r *http.Request, mk func(p *pendingReview, body []byte) (string, error)) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending[id]
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pending review " + id})
		return
	}
	js, err := mk(p, body)
	var v any
	if err == nil {
		v, err = answerOf(p.out, js)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	delete(a.pending, id)
	p.done <- v
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (a *ApproveHttp) approve(w http.ResponseWriter, r *http.Request) {
	a.answer(w, r, func(p *pendingReview, body []byte) (string, error) {
		if len(bytes.TrimSpace(body)) == 0 {
			if f, ok := p.out.FieldByName("Ok_0"); ok && f.Type == reflect.TypeOf(Unit{}) {
				body = []byte("null")
			} else {
				return "", fmt.Errorf("approve needs the decision value as the JSON body")
			}
		}
		if !json.Valid(body) {
			return "", fmt.Errorf("body is not JSON")
		}
		return `{"Ok":` + string(body) + `}`, nil
	})
}

func (a *ApproveHttp) reject(w http.ResponseWriter, r *http.Request) {
	a.answer(w, r, func(p *pendingReview, body []byte) (string, error) {
		var in struct {
			Reason *string `json:"reason"`
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &in); err != nil {
				return "", fmt.Errorf("reject body: %v", err)
			}
		}
		rej := Rejected{}
		if in.Reason != nil {
			rej.Reason = Option_Some(*in.Reason)
		}
		b, _ := json.Marshal(rej)
		return `{"Err":` + string(b) + `}`, nil
	})
}
