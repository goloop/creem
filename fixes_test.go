package creem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedirectIsNotFollowed(t *testing.T) {
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("x-api-key"))
		fmt.Fprint(w, `{"status":"succeeded"}`)
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+"/x", http.StatusTemporaryRedirect)
	}))
	defer api.Close()

	for name, c := range map[string]*Client{
		"default client": New("creem_live_secret", WithBaseURL(api.URL)),
		"custom client":  New("creem_live_secret", WithBaseURL(api.URL), WithHTTPClient(&http.Client{})),
	} {
		st, err := c.RefundTransaction(ctx, "tran_1")
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != http.StatusTemporaryRedirect || st != "" {
			t.Errorf("%s: a redirect must come back as an error, got %q, %v", name, st, err)
		}
	}
	if v := leaked.Load(); v != nil {
		t.Fatalf("the key reached the host a redirect named: %v", v)
	}
}

func TestIDsAreAllowlisted(t *testing.T) {
	c := New("creem_k", WithBaseURL("http://127.0.0.1:1"))
	for _, id := range []string{"..", "%2e%2e", "sub_1%2F..", "a b", "a\nb", `a\b`, "sub_1;x", strings.Repeat("a", maxIDLength+1)} {
		if _, err := c.CancelSubscription(ctx, id, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("id %q must be refused before it reaches the path, got %v", id, err)
		}
	}
	if err := checkID("subscription", "sub_4bNqLxW2-z_9"); err != nil {
		t.Fatalf("a real id must pass, got %v", err)
	}
}

func TestBackoffNeverPanics(t *testing.T) {
	for attempt := -1; attempt <= 100; attempt++ {
		if d := backoff(attempt); d <= 0 || d > 8*time.Second {
			t.Fatalf("backoff(%d) = %v, want (0, 8s]", attempt, d)
		}
	}
	if c := New("k", WithMaxRetries(1000)); c.maxRetries != MaxRetries {
		t.Fatalf("WithMaxRetries must be capped at %d, got %d", MaxRetries, c.maxRetries)
	}
}

func TestSecretsAreNotPrinted(t *testing.T) {
	c := New("creem_live_secret_key")
	wh := NewWebhook("whsec_secret", WithPreviousSecret("old_secret"))
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "client", c, "webhook", wh)
	out := fmt.Sprintf("%v %+v %#v %s %v %+v %#v %s", c, c, c, c, wh, wh, wh, wh) + buf.String()
	for _, secret := range []string{"creem_live_secret_key", "whsec_secret", "old_secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q leaked into %s", secret, out)
		}
	}
}

func TestOptionsAndUnsafeConfig(t *testing.T) {
	custom := &http.Client{Timeout: time.Minute}
	c := New("k", WithHTTPClient(custom), WithTimeout(time.Second), WithHeader("X-Trace", "t1"), WithMaxRetries(1))
	if c.http.Timeout != time.Minute || custom.CheckRedirect != nil || c.maxRetries != 1 || c.headers.Get("X-Trace") != "t1" {
		t.Fatal("options")
	}
	if New("k", WithTimeout(time.Second)).http.Timeout != time.Second {
		t.Fatal("WithTimeout must set the default client's timeout")
	}
	if New("k", WithHTTPClient(nil)).http == nil {
		t.Fatal("WithHTTPClient(nil) must keep the default client")
	}
	if _, err := New("", WithBaseURL("https://api.example.com")).GetCustomer(ctx, "cust_1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty key must be refused, got %v", err)
	}
	if _, err := New("k", WithBaseURL("http://api.example.com")).GetCustomer(ctx, "cust_1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("plain HTTP to a remote host must be refused, got %v", err)
	}
	if _, err := New("k", WithBaseURL("::bad")).GetCustomer(ctx, "cust_1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a malformed base URL must be refused, got %v", err)
	}

	s, c := newServer(t, ok(`{"id":"cust_1"}`))
	c.headers.Set("X-Trace", "t2")
	if _, err := c.GetCustomer(ctx, "cust_1"); err != nil || s.got[0].Header.Get("X-Trace") != "t2" ||
		s.got[0].Header.Get("x-api-key") == "" {
		t.Fatalf("headers: %v, %v", s.got[0].Header, err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkErrorsAndCancel(t *testing.T) {
	var calls atomic.Int64
	fail := roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	})
	c := New("k", WithBaseURL("https://api.example.com"), WithHTTPClient(&http.Client{Transport: fail}))
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.GetTransaction(ctx, "tran_1"); err == nil || calls.Load() != 1+DefaultMaxRetries {
		t.Fatalf("a GET is retried after a network error, got %d calls, %v", calls.Load(), err)
	}
	calls.Store(0)
	if _, err := c.RefundTransaction(ctx, "tran_1"); err == nil || calls.Load() != 1 {
		t.Fatalf("a POST is never retried after a network error, got %d calls, %v", calls.Load(), err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.GetTransaction(cctx, "tran_1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if err := sleepCtx(cctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if err := sleepCtx(ctx, time.Millisecond); err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("a", maxResponse+1)
	c = New("k", WithBaseURL("https://api.example.com"), WithHTTPClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: ioNop(big), Header: http.Header{}}, nil
	})}))
	if _, err := c.RefundTransaction(ctx, "tran_1"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("a response beyond the limit must say so, got %v", err)
	}
}

type nopBody struct{ *strings.Reader }

func (nopBody) Close() error { return nil }

func ioNop(s string) nopBody { return nopBody{strings.NewReader(s)} }

func signedAt(t *testing.T, at time.Time) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"id": "evt_9", "eventType": "subscription.active", "created_at": at.UnixMilli(), "object": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEventAge(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := WithWebhookClock(func() time.Time { return now })
	for age, wantOK := range map[time.Duration]bool{
		0: true, 47 * time.Hour: true, 49 * time.Hour: false, -49 * time.Hour: false, 30 * 24 * time.Hour: false,
	} {
		body := signedAt(t, now.Add(-age))
		_, err := NewWebhook("s", clock).Parse(Sign("s", body), body)
		if wantOK != (err == nil) {
			t.Errorf("an event %v old: want ok=%v, got %v", age, wantOK, err)
		}
		if err != nil && (!errors.Is(err, ErrEventExpired) || !errors.Is(err, ErrSignature)) {
			t.Errorf("an old event fails with ErrEventExpired, which matches ErrSignature; got %v", err)
		}
	}

	old := signedAt(t, now.Add(-30*24*time.Hour))
	if _, err := NewWebhook("s", clock, WithMaxAge(0)).Parse(Sign("s", old), old); err != nil {
		t.Fatalf("WithMaxAge(0) turns the check off, got %v", err)
	}
	if _, err := NewWebhook("s", clock, WithMaxAge(time.Hour)).Parse(Sign("s", old), old); !errors.Is(err, ErrEventExpired) {
		t.Fatalf("got %v", err)
	}

	noTime := []byte(`{"id":"evt_9","eventType":"subscription.active","object":{}}`)
	if _, err := NewWebhook("s", clock).Parse(Sign("s", noTime), noTime); !errors.Is(err, ErrEventExpired) {
		t.Fatalf("a signed event without created_at cannot be dated and is refused, got %v", err)
	}
}

func TestTimeSeconds(t *testing.T) {
	var v Time
	if err := json.Unmarshal([]byte(`1893553445`), &v); err != nil || v.Year() != 2030 {
		t.Fatalf("a number of seconds must read as seconds, got %v, %v", v.Time, err)
	}
	if err := json.Unmarshal([]byte(`1893553445678`), &v); err != nil || v.Year() != 2030 || v.Nanosecond() != 678_000_000 {
		t.Fatalf("milliseconds must still read as milliseconds, got %v, %v", v.Time, err)
	}
}

func TestListTransactions(t *testing.T) {
	s, c := newServer(t,
		ok(`{"items":[{"id":"tran_1","amount":1500,"currency":"EUR","status":"paid","order":"ord_1","created_at":1893553445678}],
			"pagination":{"total_records":11,"total_pages":2,"current_page":1,"next_page":2,"prev_page":null}}`),
		ok(`{"items":[],"pagination":{"total_records":11,"next_page":null}}`))
	p, err := c.ListTransactions(ctx, &ListTransactionsRequest{CustomerID: "cust_1", ProductID: "prod_1", Page: 1, PageSize: 10})
	if err != nil || len(p.Items) != 1 || p.NextPage != 2 || p.TotalRecords != 11 || p.Items[0].Order.ID != "ord_1" {
		t.Fatalf("got %+v, %v", p, err)
	}
	if s.got[0].Path != "/v1/transactions/search" || !strings.Contains(s.got[0].Query, "customer_id=cust_1") ||
		!strings.Contains(s.got[0].Query, "page_number=1") || !strings.Contains(s.got[0].Query, "page_size=10") {
		t.Fatalf("got %s?%s", s.got[0].Path, s.got[0].Query)
	}
	p, err = c.ListTransactions(ctx, nil)
	if err != nil || p.NextPage != 0 {
		t.Fatalf("the last page has no next page, got %+v, %v", p, err)
	}
	if _, err := c.ListTransactions(ctx, &ListTransactionsRequest{OrderID: "../x"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := c.ListTransactions(ctx, &ListTransactionsRequest{Page: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestEndpointsRefuseEmptyIDs(t *testing.T) {
	c := New("k", WithBaseURL("http://127.0.0.1:1"))
	for _, f := range []func() error{
		func() error { _, err := c.GetCheckout(ctx, ""); return err },
		func() error { _, err := c.GetSubscription(ctx, ""); return err },
		func() error { _, err := c.CancelSubscription(ctx, "", nil); return err },
		func() error { _, err := c.PauseSubscription(ctx, ""); return err },
		func() error { _, err := c.ResumeSubscription(ctx, ""); return err },
		func() error { _, err := c.UpgradeSubscription(ctx, "", "prod_1", ""); return err },
		func() error { _, err := c.UpgradeSubscription(ctx, "sub_1", "", ""); return err },
		func() error { _, err := c.GetCustomer(ctx, ""); return err },
		func() error { _, err := c.CustomerByEmail(ctx, ""); return err },
		func() error { _, err := c.CustomerPortalLink(ctx, ""); return err },
		func() error { _, err := c.GetTransaction(ctx, ""); return err },
		func() error { _, err := c.RefundTransaction(ctx, ""); return err },
	} {
		if err := f(); !errors.Is(err, ErrInvalid) {
			t.Errorf("want ErrInvalid, got %v", err)
		}
	}
}

func TestGetCalls(t *testing.T) {
	s, c := newServer(t,
		ok(`{"id":"ch_1","status":"completed"}`),
		ok(`{"id":"cust_1","email":"a@example.com"}`),
		ok(`{"id":"tran_1","amount":1500}`))
	if co, err := c.GetCheckout(ctx, "ch_1"); err != nil || co.Status != CheckoutCompleted {
		t.Fatal(co, err)
	}
	if cu, err := c.GetCustomer(ctx, "cust_1"); err != nil || cu.Email != "a@example.com" {
		t.Fatal(cu, err)
	}
	if tr, err := c.GetTransaction(ctx, "tran_1"); err != nil || tr.Amount != 1500 {
		t.Fatal(tr, err)
	}
	if s.got[0].Query != "checkout_id=ch_1" || s.got[1].Query != "customer_id=cust_1" || s.got[2].Query != "transaction_id=tran_1" {
		t.Fatalf("got %+v", s.got)
	}
}

func TestDecodeErrorNamesType(t *testing.T) {
	ev := &Event{Type: EventCheckoutCompleted, Object: []byte(`{"id":5}`)}
	if _, err := ev.Checkout(); err == nil || !strings.Contains(err.Error(), EventCheckoutCompleted) {
		t.Fatalf("got %v", err)
	}
	if _, err := NewWebhook("s").Parse(Sign("s", []byte("{")), []byte("{")); err == nil || errors.Is(err, ErrSignature) {
		t.Fatalf("a signed body that is not JSON is a decode error, got %v", err)
	}
}

func TestEmptyOrForeignEntityIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"null":         `null`,
		"empty object": `{}`,
		"other id":     `{"id":"tran_other","amount":1500}`,
		"no body":      ``,
	} {
		_, c := newServer(t, ok(body))
		if tr, err := c.GetTransaction(ctx, "tran_1"); !errors.Is(err, ErrResponse) || tr != nil {
			t.Errorf("%s: a successful response without the transaction asked for is ErrResponse, got %+v, %v", name, tr, err)
		}
	}
	_, c := newServer(t, ok(`{"id":"ch_1","status":"pending"}`))
	if _, err := c.CreateCheckout(ctx, &CheckoutRequest{ProductID: "prod_1"}); !errors.Is(err, ErrResponse) {
		t.Fatalf("a checkout without a URL to send the customer to is ErrResponse, got %v", err)
	}
	_, c = newServer(t, ok(`{"customer_portal_link":""}`))
	if _, err := c.CustomerPortalLink(ctx, "cust_1"); !errors.Is(err, ErrResponse) {
		t.Fatalf("got %v", err)
	}
	_, c = newServer(t, ok(`{"status":"done"}`))
	if _, err := c.RefundTransaction(ctx, "tran_1"); !errors.Is(err, ErrResponse) {
		t.Fatalf("an unknown refund status is ErrResponse, got %v", err)
	}
	_, c = newServer(t, ok(`{"status":"pending"}`))
	if st, err := c.RefundTransaction(ctx, "tran_1"); err != nil || st != RefundPending {
		t.Fatalf("got %q, %v", st, err)
	}
}

func TestCustomerByEmailComparesTheAnswer(t *testing.T) {
	_, c := newServer(t, ok(`{"id":"cust_9","email":"someone-else@example.com"}`))
	if _, err := c.CustomerByEmail(ctx, "ann@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a customer with another address must never be returned, got %v", err)
	}
	_, c = newServer(t, ok(`{"id":"cust_1","email":"Ann@Example.com"}`))
	if cu, err := c.CustomerByEmail(ctx, "ann@example.com"); err != nil || cu.ID != "cust_1" {
		t.Fatalf("got %+v, %v", cu, err)
	}
}

func TestPagesMustMoveForward(t *testing.T) {
	for name, body := range map[string]string{
		"same page":  `{"items":[{"id":"tran_1"}],"pagination":{"next_page":2}}`,
		"back":       `{"items":[{"id":"tran_1"}],"pagination":{"next_page":1}}`,
		"negative":   `{"items":[{"id":"tran_1"}],"pagination":{"next_page":-1}}`,
		"item no id": `{"items":[{"amount":5}],"pagination":{"next_page":null}}`,
	} {
		_, c := newServer(t, ok(body))
		if _, err := c.ListTransactions(ctx, &ListTransactionsRequest{Page: 2}); !errors.Is(err, ErrResponse) {
			t.Errorf("%s: want ErrResponse, got %v", name, err)
		}
	}
}

func TestRetryAfterIsBounded(t *testing.T) {
	cases := map[string]time.Duration{
		"9223372037":           maxRetryAfter,
		"99999999999999999999": 0, // not an int64: falls back
		"30":                   30 * time.Second,
		"0":                    0,
		"soon":                 0,
		time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat): 20 * time.Second,
		time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat):       0,
		time.Now().Add(time.Hour).UTC().Format(http.TimeFormat):        maxRetryAfter,
	}
	for in, want := range cases {
		got := retryAfter(in)
		if got < 0 || got > maxRetryAfter || (want != 20*time.Second && got != want) || (want == 20*time.Second && (got < 18*time.Second || got > 20*time.Second)) {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTimeRefusesFractions(t *testing.T) {
	var v Time
	for _, in := range []string{`1.5`, `1e300`, `NaN`, `1893553445678.25`} {
		if err := json.Unmarshal([]byte(in), &v); err == nil {
			t.Errorf("%s must not decode as a time", in)
		}
	}
	if err := json.Unmarshal([]byte(`1.893553445678e12`), &v); err != nil || v.Year() != 2030 {
		t.Fatalf("a whole number written as a float is accepted, got %v, %v", v.Time, err)
	}
}
