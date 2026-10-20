package rt

import (
	"errors"
	"net/http"
	"strings"
	"syscall"
)

// ---- std.http --------------------------------------------------------------

// HttpResponse is `std.http.Response`.
type HttpResponse struct {
	Status  int64               `json:"status"`
	Headers Map[string, string] `json:"headers"`
	Body    []byte              `json:"body"`
}

// NetGet implements `std.http.get(url)`.
func NetGet(ctx *Ctx, url Url) Result[HttpResponse, NetError] {
	return netDo(ctx, HttpReq{Method: http.MethodGet, Url: string(url)})
}

// NetPost implements `std.http.post(url, body)`: a JSON body.
func NetPost(ctx *Ctx, url Url, body []byte) Result[HttpResponse, NetError] {
	return netDo(ctx, HttpReq{Method: http.MethodPost, Url: string(url),
		Header: map[string]string{"Content-Type": "application/json"}, Body: nonNil(body)})
}

// NetRequest implements `std.http.request(method, url, headers, body)`.
func NetRequest(ctx *Ctx, method string, url Url, headers Map[string, string], body []byte) Result[HttpResponse, NetError] {
	r := HttpReq{Method: strings.ToUpper(method), Url: string(url), Header: MapToGo(headers)}
	if len(body) > 0 {
		r.Body = body
	}
	return netDo(ctx, r)
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func netDo(ctx *Ctx, r HttpReq) Result[HttpResponse, NetError] {
	resp, err := ctx.Http.Do(ctx, r)
	if err != nil {
		return Result_Err[HttpResponse](NetErrorOf(err))
	}
	return Result_Ok[HttpResponse, NetError](HttpResponse{Status: int64(resp.Status), Headers: MapFromGo(resp.Header), Body: resp.Body})
}

// HttpResponseOk implements `ok()`: a 2xx status.
func HttpResponseOk(ctx *Ctx, r HttpResponse) bool { return r.Status >= 200 && r.Status < 300 }

// HttpResponseText implements `text()`.
func HttpResponseText(ctx *Ctx, r HttpResponse) string {
	return strings.ToValidUTF8(string(r.Body), "�")
}

// NetErrorOf maps a Go error to the prelude NetError.
func NetErrorOf(err error) NetError {
	switch {
	case isTimeout(err):
		return NetError_Timeout()
	case errors.Is(err, syscall.ECONNREFUSED):
		return NetError_Refused(err.Error())
	}
	return NetError_Other(err.Error())
}

// HttpResponseStatus implements `status()`.
func HttpResponseStatus(ctx *Ctx, r HttpResponse) int64 { return r.Status }

// HttpResponseHeaders implements `headers()`.
func HttpResponseHeaders(ctx *Ctx, r HttpResponse) Map[string, string] { return r.Headers }

// HttpResponseBody implements `body()`.
func HttpResponseBody(ctx *Ctx, r HttpResponse) []byte { return r.Body }
