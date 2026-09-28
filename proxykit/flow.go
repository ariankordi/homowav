package proxykit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrBodyTooLarge is returned by Flow.RequestBody and Response.Body when a body
// is longer than Proxy.BodyLimit. The returned bytes are the first BodyLimit
// bytes, and the full body is still forwarded/streamed intact.
var ErrBodyTooLarge = errors.New("proxykit: body exceeds limit")

// Flow is one intercepted request/response exchange, handed to a Hook.
//
// Typical hook shapes:
//
//	f.Respond(200, h, body)                    // Custom reply, upstream never contacted.
//	res, _ := f.Forward(); b, _ := res.Body()  // Tap: observe, then it is sent as-is.
//	res.SetBody(modified)                      // Rewrite the response.
//	(do nothing)                               // Forwarded and streamed untouched.
type Flow struct {
	// Request is what will be sent upstream. Hooks may edit Method, URL,
	// Header, and Host before calling Forward. Use RequestBody/SetRequestBody
	// for the body so buffering stays consistent.
	Request *http.Request
	// Log is tagged with this flow's host and path.
	Log *slog.Logger

	proxy  *Proxy
	writer http.ResponseWriter
	start  time.Time

	reqBody       []byte
	reqBodyErr    error
	reqBodyLoaded bool

	response  *Response
	responded bool
	status    int
	written   int64
}

// Response is the upstream response as seen by a hook. StatusCode and Header
// may be edited freely before the hook returns.
type Response struct {
	StatusCode int
	Header     http.Header
	// Proto is the upstream protocol, e.g. "HTTP/2.0".
	Proto string

	// stream is what gets copied to the client when the body is not buffered
	// (the raw upstream body, or a replay of prefix+rest after an over-limit read).
	stream  io.ReadCloser
	body    []byte
	bodyErr error
	loaded  bool
	limit   int64
}

// RequestBody buffers and returns the request body (up to Proxy.BodyLimit).
// The body remains forwardable afterwards, even if it was too large.
func (f *Flow) RequestBody() ([]byte, error) {
	if f.reqBodyLoaded {
		return f.reqBody, f.reqBodyErr
	}
	f.reqBodyLoaded = true
	if f.Request.Body == nil || f.Request.Body == http.NoBody {
		return nil, nil
	}
	buf, replay, err := bufferBody(f.Request.Body, f.proxy.bodyLimit())
	f.reqBody, f.reqBodyErr = buf, err
	if replay != nil {
		f.Request.Body = replay
	}
	return buf, err
}

// SetRequestBody replaces the body that Forward will send.
func (f *Flow) SetRequestBody(body []byte) {
	f.reqBody, f.reqBodyErr, f.reqBodyLoaded = body, nil, true
	f.Request.Body = io.NopCloser(bytes.NewReader(body))
	f.Request.ContentLength = int64(len(body))
	// The Transport derives the header from ContentLength; a stale one would conflict.
	f.Request.Header.Del("Content-Length")
}

// Forward sends Request upstream and returns the response without writing it
// to the client yet. Calling it again returns the same response.
func (f *Flow) Forward() (*Response, error) {
	if f.response != nil {
		return f.response, nil
	}
	upstream, err := f.proxy.transport().RoundTrip(f.Request)
	if err != nil {
		return nil, err
	}
	removeHopByHop(upstream.Header)
	f.response = &Response{
		StatusCode: upstream.StatusCode,
		Header:     upstream.Header,
		Proto:      upstream.Proto,
		stream:     upstream.Body,
		limit:      f.proxy.bodyLimit(),
	}
	return f.response, nil
}

// ErrNotReplayable is returned by Reforward when the request body was never
// buffered (or did not fit in BodyLimit), so it cannot be sent a second time.
var ErrNotReplayable = errors.New("proxykit: request body not buffered; call RequestBody before Forward")

// Reforward discards the current upstream response and sends Request again,
// for example after a hook refreshed a credential in Request.Header. A request
// with a body can only be replayed if RequestBody (or SetRequestBody) buffered
// it before the first Forward.
func (f *Flow) Reforward() (*Response, error) {
	if f.responded {
		return nil, errors.New("proxykit: Reforward after the response was sent")
	}
	hasBody := f.Request.Body != nil && f.Request.Body != http.NoBody
	if hasBody && (!f.reqBodyLoaded || f.reqBodyErr != nil) {
		return nil, ErrNotReplayable
	}
	if f.response != nil {
		f.response.stream.Close()
		f.response = nil
	}
	// A fresh clone keeps the Transport from seeing a request it already consumed.
	f.Request = f.Request.Clone(f.Request.Context())
	if f.reqBodyLoaded {
		f.Request.Body = http.NoBody
		if len(f.reqBody) > 0 {
			f.Request.Body = io.NopCloser(bytes.NewReader(f.reqBody))
		}
		f.Request.ContentLength = int64(len(f.reqBody))
	}
	return f.Forward()
}

// Respond sends a custom reply. If Forward was already called, the upstream
// response is discarded. Only the first Respond (or the automatic forward) wins.
func (f *Flow) Respond(status int, header http.Header, body []byte) {
	if f.responded {
		f.Log.Warn("Respond called twice; ignoring")
		return
	}
	f.responded = true
	if f.response != nil {
		f.response.stream.Close()
	}
	dst := f.writer.Header()
	for name, values := range header {
		dst[name] = values
	}
	dst.Set("Content-Length", strconv.Itoa(len(body)))
	f.writeHeader(status)
	n, _ := f.writer.Write(body)
	f.written += int64(n)
}

// Body buffers and returns the (already decompressed) response body, up to
// Proxy.BodyLimit. If it is longer, the prefix and ErrBodyTooLarge are
// returned and the full body is still streamed to the client.
func (r *Response) Body() ([]byte, error) {
	if r.loaded {
		return r.body, r.bodyErr
	}
	r.loaded = true
	buf, replay, err := bufferBody(r.stream, r.limit)
	r.body, r.bodyErr = buf, err
	if replay != nil {
		r.stream = replay
	}
	return buf, err
}

// SetBody replaces the body sent to the client.
func (r *Response) SetBody(body []byte) {
	if !r.loaded || r.bodyErr != nil {
		// Discard whatever is left of the upstream body; we are not sending it.
		r.stream.Close()
	}
	r.body, r.bodyErr, r.loaded = body, nil, true
}

// finish runs after the hook: it forwards if the hook did nothing and writes
// the response if the hook did not already Respond.
func (f *Flow) finish() {
	if f.responded {
		return
	}
	res, err := f.Forward()
	if err != nil {
		f.Log.Warn("upstream request failed", "err", err)
		f.Respond(http.StatusBadGateway, http.Header{"Content-Type": {"text/plain"}},
			[]byte(fmt.Sprintf("proxykit: upstream error: %v\n", err)))
		return
	}
	f.writeResponse(res)
}

// writeResponse copies res to the client, buffered if a hook loaded or replaced
// the body, streamed otherwise.
func (f *Flow) writeResponse(res *Response) {
	f.responded = true
	defer res.stream.Close()
	dst := f.writer.Header()
	for name, values := range res.Header {
		dst[name] = values
	}
	if res.loaded && res.bodyErr == nil {
		// Body is fully in memory (read or replaced), so the length is known exactly.
		dst.Set("Content-Length", strconv.Itoa(len(res.body)))
		f.writeHeader(res.StatusCode)
		n, _ := f.writer.Write(res.body)
		f.written += int64(n)
		return
	}
	f.writeHeader(res.StatusCode)
	n, err := copyWithFlush(f.writer, res.stream, dst.Get("Content-Length") == "")
	f.written += n
	if err != nil {
		f.Log.Debug("response copy ended early", "err", err)
	}
}

// writeHeader records the status for the access log before sending it.
func (f *Flow) writeHeader(status int) {
	f.status = status
	f.writer.WriteHeader(status)
}

// copyBufferSize matches io.Copy's default so streaming costs the same.
const copyBufferSize = 32 * 1024

// copyWithFlush streams src to w. When the length is unknown (chunked, SSE,
// long-poll) it flushes after every read so the client sees data immediately,
// mirroring httputil.ReverseProxy's behavior for ContentLength == -1.
func copyWithFlush(w http.ResponseWriter, src io.Reader, flushEachRead bool) (int64, error) {
	if !flushEachRead {
		return io.Copy(w, src)
	}
	controller := http.NewResponseController(w)
	buf := make([]byte, copyBufferSize)
	var total int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			_ = controller.Flush()
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

// bufferBody reads up to limit bytes of rc. If the body fits, rc is closed and
// replay is a fresh reader over the bytes. If it does not fit, the first limit
// bytes are returned with ErrBodyTooLarge and replay yields the whole body
// (everything read so far, then the rest of rc) so nothing is lost.
func bufferBody(rc io.ReadCloser, limit int64) (buf []byte, replay io.ReadCloser, err error) {
	// Read one extra byte so "exactly limit" and "more than limit" are distinguishable.
	read, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return read, readCloser{io.MultiReader(bytes.NewReader(read), rc), rc}, err
	}
	if int64(len(read)) > limit {
		return read[:limit], readCloser{io.MultiReader(bytes.NewReader(read), rc), rc}, ErrBodyTooLarge
	}
	rc.Close()
	return read, io.NopCloser(bytes.NewReader(read)), nil
}

// readCloser pairs a Reader with the Closer of the stream it wraps.
type readCloser struct {
	io.Reader
	io.Closer
}

// hopByHopHeaders apply to a single connection and must not be forwarded.
// List taken from Go's net/http/httputil/reverseproxy.go (BSD-3-Clause,
// https://github.com/golang/go/blob/go1.24.1/src/net/http/httputil/reverseproxy.go).
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// removeHopByHop strips hop-by-hop headers in place, including any named in Connection.
func removeHopByHop(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				header.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		header.Del(name)
	}
}
