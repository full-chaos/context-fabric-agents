package l1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// errBudget is returned once the per-run request budget is spent.
var errBudget = errors.New("request budget exhausted")

// pacer serializes every request of a run, spaces them by interval, caps
// their number and records the JSON-RPC method of each one. It records
// method names only: never headers, never bodies.
type pacer struct {
	mu        sync.Mutex
	interval  time.Duration
	retryWait time.Duration
	max       int
	count     int
	last      time.Time
	methods   []string
	sleep     func(time.Duration)
}

func newPacer(max int, interval, retryWait time.Duration) *pacer {
	return &pacer{max: max, interval: interval, retryWait: retryWait, sleep: time.Sleep}
}

// Count returns the number of HTTP requests sent (retries included).
func (p *pacer) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

// mark returns the current length of the method log.
func (p *pacer) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.methods)
}

// firstMethodSince returns the first method recorded at or after mark.
func (p *pacer) firstMethodSince(mark int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if mark < len(p.methods) {
		return p.methods[mark]
	}
	return ""
}

// transport sends requests to one host only, through the pacer, with the
// bearer when token is set. A 429 is retried once after retryWait.
type transport struct {
	host  string
	token string
	p     *pacer
	next  http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.host {
		return nil, fmt.Errorf("refusing request to host %q", req.URL.Host)
	}
	body, err := readBody(req)
	if err != nil {
		return nil, err
	}
	t.p.mu.Lock()
	defer t.p.mu.Unlock()
	t.p.methods = append(t.p.methods, rpcMethod(req.Method, body))
	resp, err := t.send(req, body)
	if err == nil && resp.StatusCode == http.StatusTooManyRequests {
		drain(resp)
		t.p.sleep(t.p.retryWait)
		resp, err = t.send(req, body)
	}
	return resp, err
}

// send must be called with p.mu held.
func (t *transport) send(req *http.Request, body []byte) (*http.Response, error) {
	if t.p.count >= t.p.max {
		return nil, errBudget
	}
	if !t.p.last.IsZero() {
		if wait := t.p.interval - time.Since(t.p.last); wait > 0 {
			t.p.sleep(wait)
		}
	}
	t.p.count++
	r := req.Clone(req.Context())
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.next.RoundTrip(r)
	t.p.last = time.Now()
	return resp, err
}

func readBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(io.LimitReader(req.Body, 1<<20))
}

// rpcMethod extracts the JSON-RPC method name(s) from a request body.
func rpcMethod(httpMethod string, body []byte) string {
	if len(body) == 0 {
		return httpMethod
	}
	var one struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &one) == nil && one.Method != "" {
		return one.Method
	}
	var batch []struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &batch) == nil && len(batch) > 0 {
		return batch[0].Method
	}
	return httpMethod + " (unparsed)"
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func noRedirects(*http.Request, []*http.Request) error {
	return errors.New("redirects are refused")
}
