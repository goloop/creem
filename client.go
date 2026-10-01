package creem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// API hosts.
const (
	ProductionURL = "https://api.creem.io"
	TestURL       = "https://test-api.creem.io"
)

// Client defaults.
const (
	DefaultTimeout    = 30 * time.Second
	DefaultMaxRetries = 2
)

// testKeyPrefix starts every test-mode API key. A key says which mode it
// belongs to, so the client picks the host itself and a test key never ends
// up talking to production by a missing option.
const testKeyPrefix = "creem_test_"

// maxResponse bounds how much of a response body is read.
const maxResponse = 4 << 20

// maxRetryAfter caps how long a Retry-After header can make a call wait.
const maxRetryAfter = 60 * time.Second

// MaxRetries is the most WithMaxRetries accepts. The backoff tops out at 8
// seconds, so ten retries already wait over a minute.
const MaxRetries = 10

// maxIDLength bounds an id put into a request path.
const maxIDLength = 128

// ErrInvalid reports a request refused before it was sent.
var ErrInvalid = errors.New("creem: invalid request")

// Client calls the Creem API.
//
// A Client is safe for concurrent use.
type Client struct {
	apiKey     string
	baseURL    string
	http       *http.Client
	customHTTP bool
	maxRetries int
	headers    http.Header
	sleep      func(context.Context, time.Duration) error
}

// Option configures a Client in New.
type Option func(*Client)

// WithTest points the client at the test host (true) or production (false),
// overriding what the API key implies.
func WithTest(test bool) Option {
	return func(c *Client) {
		if test {
			c.baseURL = TestURL
		} else {
			c.baseURL = ProductionURL
		}
	}
}

// WithBaseURL overrides the API host (a proxy, a test server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the HTTP client used for requests; nil keeps the
// default. The client is copied, and the copy never follows a redirect: the
// API has no reason to send one, and following it would send the request -
// and with it the key or the body - to another host.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http, c.customHTTP = h, true
		}
	}
}

// WithTimeout sets the per-request timeout of the default HTTP client. It
// has no effect together with WithHTTPClient.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if !c.customHTTP {
			c.http.Timeout = d
		}
	}
}

// WithMaxRetries sets how many times a request is retried, between 0 and
// MaxRetries. See the package documentation for which failures are retried.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = min(max(0, n), MaxRetries) }
}

// WithHeader adds a header sent with every request.
func WithHeader(key, value string) Option {
	return func(c *Client) { c.headers.Add(key, value) }
}

// New returns a Client for the API key. The host is the test host for a
// test key ("creem_test_...") and production otherwise; WithTest and
// WithBaseURL override it.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:     apiKey,
		baseURL:    ProductionURL,
		http:       &http.Client{Timeout: DefaultTimeout},
		maxRetries: DefaultMaxRetries,
		headers:    http.Header{},
		sleep:      sleepCtx,
	}
	if strings.HasPrefix(apiKey, testKeyPrefix) {
		c.baseURL = TestURL
	}
	for _, o := range opts {
		o(c)
	}
	h := *c.http
	h.CheckRedirect = noRedirect
	c.http = &h
	return c
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// String names the host the client talks to and never the key, so a client
// that ends up in a log line or an error message does not leak it.
func (c *Client) String() string { return "creem.Client(" + c.baseURL + ")" }

// GoString is String, for %#v.
func (c *Client) GoString() string { return c.String() }

// LogValue implements slog.LogValuer: the host, never the key.
func (c *Client) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Test reports whether the client talks to the test host.
func (c *Client) Test() bool { return c.baseURL == TestURL }

// do sends one request and decodes the response into out.
//
// Only failures that cannot have changed anything are retried: a 429 (the
// request was refused before it ran) on any method, and a network error or
// a 5xx on a GET. A POST that failed with a 5xx may have been applied -
// a refund, a cancellation - so repeating it is the caller's decision, with
// the error in hand.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	if err := c.check(); err != nil {
		return err
	}
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("creem: encode request: %w", err)
		}
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		status, data, header, err := c.send(ctx, method, path, body)
		retry := false
		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, ErrInvalid) {
				return err // the request could not be built: retrying cannot help
			}
			retry = method == http.MethodGet
			err = fmt.Errorf("creem: %s %s: %w", method, path, err)
		case status == http.StatusTooManyRequests:
			retry = true
			wait = retryAfter(header.Get("Retry-After"))
			err = parseError(status, data)
		case status >= 500:
			retry = method == http.MethodGet
			err = parseError(status, data)
		case status >= 300:
			return parseError(status, data)
		default:
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("creem: decode response: %w", err)
			}
			return nil
		}

		if !retry || attempt >= c.maxRetries {
			return err
		}
		if wait == 0 {
			wait = backoff(attempt)
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return serr
		}
	}
}

func (c *Client) send(ctx context.Context, method, path string, body []byte) (int, []byte, http.Header, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(data) > maxResponse {
		return 0, nil, nil, fmt.Errorf("response larger than %d bytes", maxResponse)
	}
	return res.StatusCode, data, res.Header, nil
}

// backoff is 0.5s, 1s, 2s, ... with jitter, capped at 8s.
func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << min(max(attempt, 0), 4)
	return d/2 + rand.N(d/2+1)
}

// retryAfter reads a Retry-After header in seconds; anything else falls back
// to the default backoff.
func retryAfter(v string) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || s <= 0 {
		return 0
	}
	return min(time.Duration(s)*time.Second, maxRetryAfter)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// check refuses a client that cannot be used safely: no key, or a host
// reached over plain HTTP, which would send the key in the clear. Plain HTTP
// is allowed to a loopback host, for a test server.
func (c *Client) check() error {
	if c.apiKey == "" {
		return fmt.Errorf("%w: empty API key", ErrInvalid)
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: base URL %q", ErrInvalid, c.baseURL)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("%w: base URL %q is not https", ErrInvalid, c.baseURL)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkID admits the characters the provider's ids are made of and nothing
// else. An id goes into the request path, and a denylist would always miss
// something: "..", an escaped slash, a space.
func checkID(what, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty %s id", ErrInvalid, what)
	}
	if len(id) > maxIDLength {
		return fmt.Errorf("%w: %s id longer than %d bytes", ErrInvalid, what, maxIDLength)
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return fmt.Errorf("%w: malformed %s id %q", ErrInvalid, what, id)
		}
	}
	return nil
}
