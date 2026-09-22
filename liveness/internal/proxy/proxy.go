// Package proxy is the liveness L2 recording reverse proxy (CHAOS-6205).
//
// A real MCP client is pointed at http://127.0.0.1:<port>/mcp. The proxy
// forwards every request to the production hosted MCP and appends one
// record per request to a JSON Lines file:
//
//	seq, method (JSON-RPC method name), requested_revision,
//	revision (negotiated), status (HTTP), latency_ms
//
// Guarantees:
//
//   - It listens on a loopback IP address only (127.0.0.0/8 or ::1). A host
//     name, an unspecified address or any other address is refused.
//   - The upstream is the compiled constant Upstream. No flag, environment
//     variable or HTTP proxy setting changes it.
//   - It never records a header, a body or a token. The method name and the
//     revisions are read from the request and response and kept only when
//     they match a fixed pattern; everything else is dropped.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// Upstream is the only host the proxy forwards to. Deliberately a
	// compiled constant: not a flag, not an environment variable.
	Upstream = "https://mcp.fullchaos.dev"
	// Path is the only path the proxy forwards. Anything else is a local 404.
	Path = "/mcp"

	maxRequestBody = 4 << 20
	maxCapture     = 256 << 10
)

var (
	methodRe   = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,64}$`)
	revisionRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Record is one line of the record file. It holds no header, body or token.
type Record struct {
	Seq               int    `json:"seq"`
	Method            string `json:"method"`
	RequestedRevision string `json:"requested_revision"`
	Revision          string `json:"revision"`
	Status            int    `json:"status"`
	LatencyMS         int64  `json:"latency_ms"`
}

// Options are the only knobs. None of them selects the upstream.
type Options struct {
	// Listen is host:port; host must be a loopback IP literal.
	Listen string
	// RecordPath is the JSON Lines file the records are appended to.
	RecordPath string
	// MaxRequests caps forwarded requests; later ones get a local 429.
	MaxRequests int
	// MinInterval spaces the start of forwarded requests.
	MinInterval time.Duration
}

// Server is a running proxy.
type Server struct {
	opts     Options
	upstream *url.URL
	rp       *httputil.ReverseProxy
	ln       net.Listener
	srv      *http.Server

	mu      sync.Mutex
	seq     int
	sent    int
	last    time.Time
	out     *os.File
	writeMu sync.Mutex
}

var upstreamURL = func() *url.URL {
	u, err := url.Parse(Upstream)
	if err != nil {
		panic(err)
	}
	return u
}()

// CheckListen refuses any listen address whose host is not a loopback IP
// literal.
func CheckListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", listen, err)
	}
	if port == "" {
		return fmt.Errorf("listen %q: port is empty", listen)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("listen %q: host must be a loopback IP literal (127.0.0.1 or ::1), not a name", listen)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("listen %q: %s is not a loopback address", listen, ip)
	}
	return nil
}

// Start listens and serves in the background. It forwards to Upstream.
func Start(opts Options) (*Server, error) {
	return start(opts, upstreamURL, nil)
}

// start is Start with an injectable upstream and transport, for tests only.
func start(opts Options, upstream *url.URL, rt http.RoundTripper) (*Server, error) {
	if err := CheckListen(opts.Listen); err != nil {
		return nil, err
	}
	if opts.RecordPath == "" {
		return nil, errors.New("record path is empty")
	}
	if opts.MaxRequests <= 0 {
		return nil, errors.New("max requests must be positive")
	}
	out, err := os.OpenFile(opts.RecordPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if rt == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		// Never honour HTTP(S)_PROXY: the upstream must stay the constant.
		t.Proxy = nil
		rt = t
	}
	s := &Server{opts: opts, upstream: upstream, out: out}
	s.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstream.Scheme
			pr.Out.URL.Host = upstream.Host
			pr.Out.URL.Path = Path
			pr.Out.URL.RawPath = ""
			pr.Out.Host = upstream.Host
		},
		Transport:     rt,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			s.onResponse(resp)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if st, ok := r.Context().Value(stateKey{}).(*reqState); ok {
				s.write(st.finish(http.StatusBadGateway, ""))
			}
			w.WriteHeader(http.StatusBadGateway)
		},
		// Upstream errors are reported by status only; the default logger
		// could print request details.
		ErrorLog: nil,
	}
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		out.Close()
		return nil, err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// URL is the client-facing endpoint, http://<listen addr>/mcp.
func (s *Server) URL() string { return "http://" + s.ln.Addr().String() + Path }

// Upstream returns the upstream the server forwards to.
func (s *Server) Upstream() string { return s.upstream.String() }

// Close stops the server and closes the record file.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if cerr := s.out.Close(); err == nil {
		err = cerr
	}
	return err
}

type stateKey struct{}

// reqState carries what one request's record needs; it never holds a header
// value other than the revision after validation, and never a body.
type reqState struct {
	seq       int
	method    string
	requested string
	start     time.Time
	once      sync.Once
	rec       Record
}

func (st *reqState) finish(status int, negotiated string) *Record {
	var out *Record
	st.once.Do(func() {
		st.rec = Record{
			Seq:               st.seq,
			Method:            st.method,
			RequestedRevision: st.requested,
			Revision:          negotiated,
			Status:            status,
			LatencyMS:         time.Since(st.start).Milliseconds(),
		}
		out = &st.rec
	})
	return out
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path {
		http.NotFound(w, r)
		return
	}
	var body []byte
	if r.Body != nil && r.Body != http.NoBody {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
		r.Body.Close()
		if err != nil || len(b) > maxRequestBody {
			http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	}
	method, requested := parseRequest(r.Method, r.Header.Get("MCP-Protocol-Version"), body)

	s.mu.Lock()
	s.seq++
	st := &reqState{seq: s.seq, method: method, requested: requested}
	over := s.sent >= s.opts.MaxRequests
	if !over {
		s.sent++
		if !s.last.IsZero() {
			if wait := s.opts.MinInterval - time.Since(s.last); wait > 0 {
				time.Sleep(wait)
			}
		}
		s.last = time.Now()
	}
	s.mu.Unlock()
	st.start = time.Now()

	if over {
		s.write(st.finish(http.StatusTooManyRequests, ""))
		http.Error(w, "liveness proxy request budget exhausted", http.StatusTooManyRequests)
		return
	}
	s.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))
}

// onResponse records the request. For a handshake (initialize,
// server/discover) with a 2xx status the negotiated revision is read from
// the response once its body is fully read or closed.
func (s *Server) onResponse(resp *http.Response) {
	st, ok := resp.Request.Context().Value(stateKey{}).(*reqState)
	if !ok {
		return
	}
	status := resp.StatusCode
	ok2xx := status >= 200 && status < 300
	switch {
	case ok2xx && (st.method == "initialize" || st.method == "server/discover"):
		ct := resp.Header.Get("Content-Type")
		resp.Body = &captureBody{rc: resp.Body, done: func(b []byte) {
			s.write(st.finish(status, negotiated(st.method, st.requested, ct, b)))
		}}
	case ok2xx:
		// Every other request runs at the revision the client sends.
		s.write(st.finish(status, st.requested))
	default:
		s.write(st.finish(status, ""))
	}
}

func (s *Server) write(rec *Record) {
	if rec == nil {
		return
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(line, '\n'))
	_ = s.out.Sync()
}

// captureBody passes the body through and keeps at most maxCapture bytes;
// done runs once, at EOF or Close.
type captureBody struct {
	rc   io.ReadCloser
	buf  bytes.Buffer
	once sync.Once
	done func([]byte)
}

func (c *captureBody) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 && c.buf.Len() < maxCapture {
		room := maxCapture - c.buf.Len()
		c.buf.Write(p[:min(n, room)])
	}
	if err != nil {
		c.fire()
	}
	return n, err
}

func (c *captureBody) Close() error {
	c.fire()
	return c.rc.Close()
}

func (c *captureBody) fire() { c.once.Do(func() { c.done(c.buf.Bytes()) }) }

// parseRequest returns the JSON-RPC method (or the HTTP method for a body-less
// request) and the requested revision. Values outside the fixed patterns are
// replaced, never copied.
func parseRequest(httpMethod, header string, body []byte) (method, requested string) {
	method = httpMethod
	if len(body) > 0 {
		var one struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		switch {
		case json.Unmarshal(body, &one) == nil && one.Method != "":
			method = one.Method
		default:
			var batch []json.RawMessage
			if json.Unmarshal(body, &batch) == nil && len(batch) > 0 && json.Unmarshal(batch[0], &one) == nil && one.Method != "" {
				method = one.Method
			} else {
				method = httpMethod + " (unparsed)"
			}
		}
		if !methodRe.MatchString(method) && method != httpMethod+" (unparsed)" {
			method = "(invalid method)"
		}
		if revisionRe.MatchString(header) {
			return method, header
		}
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			Meta            struct {
				ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
			} `json:"_meta"`
		}
		if len(one.Params) > 0 && json.Unmarshal(one.Params, &p) == nil {
			for _, v := range []string{p.ProtocolVersion, p.Meta.ProtocolVersion} {
				if revisionRe.MatchString(v) {
					return method, v
				}
			}
		}
		return method, ""
	}
	if revisionRe.MatchString(header) {
		return method, header
	}
	return method, ""
}

// negotiated reads the revision the server agreed to from a handshake
// response (JSON or SSE). initialize: result.protocolVersion. server/discover:
// the requested revision when result.supportedVersions lists it.
func negotiated(method, requested, contentType string, body []byte) string {
	for _, msg := range messages(contentType, body) {
		var m struct {
			Result *struct {
				ProtocolVersion   string   `json:"protocolVersion"`
				SupportedVersions []string `json:"supportedVersions"`
			} `json:"result"`
		}
		if json.Unmarshal(msg, &m) != nil || m.Result == nil {
			continue
		}
		switch method {
		case "initialize":
			if revisionRe.MatchString(m.Result.ProtocolVersion) {
				return m.Result.ProtocolVersion
			}
		case "server/discover":
			if requested != "" && slices.Contains(m.Result.SupportedVersions, requested) {
				return requested
			}
		}
		return ""
	}
	return ""
}

// messages splits a response body into JSON-RPC messages.
func messages(contentType string, body []byte) [][]byte {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") {
		return [][]byte{body}
	}
	var out [][]byte
	var data bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), maxCapture)
	flush := func() {
		if data.Len() > 0 {
			out = append(out, bytes.Clone(data.Bytes()))
			data.Reset()
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return out
}

// ReadRecords reads a record file strictly.
func ReadRecords(path string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Record
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var r Record
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Record) int { return a.Seq - b.Seq })
	return out, nil
}
