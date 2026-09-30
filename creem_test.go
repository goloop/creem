package creem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

type recorded struct {
	Method, Path, Query string
	Header              http.Header
	Body                map[string]any
}

type reply struct {
	status int
	body   string
	header map[string]string
}

type server struct {
	got []recorded
}

func newServer(t *testing.T, replies ...reply) (*server, *Client) {
	t.Helper()
	s := &server{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec := recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone()}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rec.Body); err != nil {
				t.Errorf("request body is not JSON: %s", raw)
			}
		}
		s.got = append(s.got, rec)
		i := len(s.got) - 1
		if i >= len(replies) {
			t.Errorf("unexpected request %d: %s %s", i+1, r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		for k, v := range replies[i].header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(replies[i].status)
		_, _ = io.WriteString(w, replies[i].body)
	}))
	t.Cleanup(srv.Close)
	c := New("creem_abc", WithBaseURL(srv.URL))
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return s, c
}

func ok(body string) reply { return reply{status: 200, body: body} }

func TestNewPicksHostFromKey(t *testing.T) {
	if !New("creem_test_x").Test() || New("creem_x").Test() {
		t.Fatal("the host follows the key")
	}
	if New("creem_x", WithTest(true)).Test() != true || New("creem_test_x", WithTest(false)).Test() {
		t.Fatal("WithTest overrides the key")
	}
}

func TestCreateCheckout(t *testing.T) {
	s, c := newServer(t, ok(`{"id":"ch_1","mode":"test","status":"pending","request_id":"purchase-9",
		"custom_price":1500,"checkout_url":"https://checkout.creem.io/ch_1","product":"prod_1",
		"metadata":{"purchase":"p_9"}}`))
	co, err := c.CreateCheckout(ctx, &CheckoutRequest{
		ProductID:   "prod_1",
		RequestID:   "purchase-9",
		CustomPrice: 1500,
		Customer:    &CheckoutCustomer{Email: "a@example.com"},
		SuccessURL:  "https://app.example.com/billing?done=1",
		Metadata:    map[string]any{"purchase": "p_9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if co.ID != "ch_1" || co.CheckoutURL == "" || co.Product.ID != "prod_1" || co.Product.Object != nil || co.Metadata["purchase"] != "p_9" {
		t.Fatalf("checkout: %+v", co)
	}
	r := s.got[0]
	if r.Method != "POST" || r.Path != "/v1/checkouts" || r.Header.Get("x-api-key") != "creem_abc" {
		t.Fatalf("request: %s %s %v", r.Method, r.Path, r.Header)
	}
	if r.Body["custom_price"].(float64) != 1500 || r.Body["customer"].(map[string]any)["email"] != "a@example.com" {
		t.Fatalf("body: %+v", r.Body)
	}
	if _, has := r.Body["units"]; has {
		t.Fatal("unset units must not be sent")
	}
	if _, has := r.Body["customer"].(map[string]any)["id"]; has {
		t.Fatal("an unset customer id must not be sent")
	}
}

func TestCreateCheckoutValidates(t *testing.T) {
	_, c := newServer(t)
	bad := []*CheckoutRequest{
		nil,
		{},
		{ProductID: "p", CustomPrice: 99},
		{ProductID: "p", CustomPrice: MaxCustomPrice + 1},
		{ProductID: "p", Customer: &CheckoutCustomer{ID: "cust_1", Email: "a@example.com"}},
		{ProductID: "p", Units: -1},
	}
	for i, req := range bad {
		if _, err := c.CreateCheckout(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
}

func TestErrors(t *testing.T) {
	_, c := newServer(t,
		reply{status: 400, body: `{"trace_id":"tr-1","status":400,"error":"Bad Request","message":["product_id must be a string"],"timestamp":1}`},
		reply{status: 404, body: `{"trace_id":"tr-2","status":404,"error":"Not Found","message":"Subscription not found"}`})
	_, err := c.GetCheckout(ctx, "ch_1")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 400 || ae.TraceID != "tr-1" || ae.Messages[0] != "product_id must be a string" {
		t.Fatalf("want a typed 400, got %#v", err)
	}
	if !strings.Contains(err.Error(), "Bad Request: product_id must be a string") {
		t.Fatalf("message: %q", err)
	}
	_, err = c.GetSubscription(ctx, "sub_1")
	if !errors.Is(err, ErrNotFound) || !errors.As(err, &ae) || ae.Messages[0] != "Subscription not found" {
		t.Fatalf("a 404 with a single-string message: %v", err)
	}
}

func TestRetries(t *testing.T) {
	t.Run("GET on 5xx", func(t *testing.T) {
		s, c := newServer(t, reply{status: 503}, ok(`{"id":"sub_1","status":"active"}`))
		if sub, err := c.GetSubscription(ctx, "sub_1"); err != nil || sub.Status != "active" || len(s.got) != 2 {
			t.Fatalf("got %v after %d", err, len(s.got))
		}
	})
	t.Run("POST not on 5xx", func(t *testing.T) {
		s, c := newServer(t, reply{status: 500})
		if _, err := c.RefundTransaction(ctx, "tran_1"); err == nil || len(s.got) != 1 {
			t.Fatalf("a refund that may have run must not be repeated: %v after %d", err, len(s.got))
		}
	})
	t.Run("POST on 429", func(t *testing.T) {
		s, c := newServer(t, reply{status: 429, header: map[string]string{"Retry-After": "3"}}, ok(`{"status":"succeeded"}`))
		var waited time.Duration
		c.sleep = func(_ context.Context, d time.Duration) error { waited = d; return nil }
		st, err := c.RefundTransaction(ctx, "tran_1")
		if err != nil || st != RefundSucceeded || len(s.got) != 2 || waited != 3*time.Second {
			t.Fatalf("got %q %v after %d, waited %s", st, err, len(s.got), waited)
		}
	})
}

func TestSubscriptionCalls(t *testing.T) {
	sub := `{"id":"sub_1","status":"scheduled_cancel","product":{"id":"prod_1","name":"Pro","price":6000,"currency":"EUR"},
		"customer":{"id":"cust_1","email":"a@example.com"},"items":[{"id":"sitem_1","product_id":"prod_1","units":1}],
		"current_period_start_date":"2030-01-01T00:00:00.000Z","current_period_end_date":"2031-01-01T00:00:00.000Z",
		"canceled_at":null,"created_at":"2030-01-01T00:00:00.000Z","updated_at":"2030-01-01T00:00:00.000Z"}`
	s, c := newServer(t, ok(sub), ok(sub), ok(sub), ok(sub), ok(sub))

	got, err := c.CancelSubscription(ctx, "sub_1", &CancelRequest{Mode: CancelScheduled})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SubscriptionScheduledCancel || got.Product.Object.Price != 6000 || got.Customer.ID != "cust_1" ||
		got.CurrentPeriodEndDate.Year() != 2031 || !got.CanceledAt.IsZero() {
		t.Fatalf("subscription: %+v", got)
	}
	if s.got[0].Path != "/v1/subscriptions/sub_1/cancel" || s.got[0].Body["mode"] != "scheduled" {
		t.Fatalf("cancel: %+v", s.got[0])
	}
	if _, err := c.ResumeSubscription(ctx, "sub_1"); err != nil || s.got[1].Path != "/v1/subscriptions/sub_1/resume" {
		t.Fatal(err)
	}
	if _, err := c.PauseSubscription(ctx, "sub_1"); err != nil || s.got[2].Path != "/v1/subscriptions/sub_1/pause" {
		t.Fatal(err)
	}
	if _, err := c.UpgradeSubscription(ctx, "sub_1", "prod_2", ProrationNone); err != nil {
		t.Fatal(err)
	}
	if b := s.got[3].Body; b["product_id"] != "prod_2" || b["update_behavior"] != "proration-none" {
		t.Fatalf("upgrade: %+v", b)
	}
	if _, err := c.GetSubscription(ctx, "sub_1"); err != nil || s.got[4].Query != "subscription_id=sub_1" {
		t.Fatalf("get: %v %+v", err, s.got[4])
	}
	if _, err := c.GetSubscription(ctx, "sub/../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a malformed id is refused, got %v", err)
	}
}

func TestCustomersAndTransactions(t *testing.T) {
	s, c := newServer(t,
		ok(`{"id":"cust_1","email":"a+b@example.com","created_at":"2030-01-01T00:00:00Z"}`),
		ok(`{"customer_portal_link":"https://creem.io/my-orders/login/x"}`),
		ok(`{"id":"tran_1","amount":1500,"amount_paid":1800,"tax_amount":300,"currency":"EUR","type":"payment",
			"status":"paid","order":"ord_1","customer":"cust_1","created_at":1893456000000,"period_start":null}`))
	cu, err := c.CustomerByEmail(ctx, "a+b@example.com")
	if err != nil || cu.ID != "cust_1" || s.got[0].Query != "email=a%2Bb%40example.com" {
		t.Fatalf("customer: %+v %v %q", cu, err, s.got[0].Query)
	}
	link, err := c.CustomerPortalLink(ctx, "cust_1")
	if err != nil || !strings.HasPrefix(link, "https://") || s.got[1].Body["customer_id"] != "cust_1" {
		t.Fatalf("portal: %q %v", link, err)
	}
	tr, err := c.GetTransaction(ctx, "tran_1")
	if err != nil {
		t.Fatal(err)
	}
	if tr.AmountPaid != 1800 || tr.Order.ID != "ord_1" || !tr.CreatedAt.Equal(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) || !tr.PeriodStart.IsZero() {
		t.Fatalf("transaction with millisecond times: %+v", tr)
	}
}

func TestTime(t *testing.T) {
	var v struct {
		A, B, C, D, E Time
	}
	err := json.Unmarshal([]byte(`{"A":"2030-01-02T03:04:05.678Z","B":1893553445678,"C":null,"D":"","E":1.893553445678e12}`), &v)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2030, 1, 2, 3, 4, 5, 678000000, time.UTC)
	if !v.A.Equal(want) || !v.B.Equal(want) || !v.C.IsZero() || !v.D.IsZero() || !v.E.Equal(want) {
		t.Fatalf("times: %+v", v)
	}
	if err := json.Unmarshal([]byte(`{"A":"yesterday"}`), &v); err == nil {
		t.Fatal("a malformed time is an error")
	}
	b, _ := json.Marshal(struct{ A, C Time }{v.A, Time{}})
	if string(b) != `{"A":"2030-01-02T03:04:05.678Z","C":null}` {
		t.Fatalf("marshal: %s", b)
	}
}

func TestExpandable(t *testing.T) {
	var v struct {
		A, B, C Expandable[Customer]
	}
	if err := json.Unmarshal([]byte(`{"A":"cust_1","B":{"id":"cust_2","email":"b@example.com"},"C":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.ID != "cust_1" || v.A.Object != nil || v.B.ID != "cust_2" || v.B.Object.Email != "b@example.com" || v.C.ID != "" {
		t.Fatalf("expandable: %+v", v)
	}
	b, _ := json.Marshal(v)
	if !bytes.Contains(b, []byte(`"A":"cust_1"`)) || !bytes.Contains(b, []byte(`"email":"b@example.com"`)) || !bytes.Contains(b, []byte(`"C":null`)) {
		t.Fatalf("marshal: %s", b)
	}
}

func TestUpdateSubscriptionItems(t *testing.T) {
	s, c := newServer(t, ok(`{"id":"sub_1","status":"active","items":[{"id":"sitem_1","units":5}]}`))
	sub, err := c.UpdateSubscriptionItems(ctx, "sub_1", []SubscriptionItemUpdate{{ID: "sitem_1", Units: 5}}, ProrationChargeImmediately)
	if err != nil || sub.Items[0].Units != 5 {
		t.Fatalf("got %+v, %v", sub, err)
	}
	r := s.got[0]
	item := r.Body["items"].([]any)[0].(map[string]any)
	if r.Method != "POST" || r.Path != "/v1/subscriptions/sub_1" || item["id"] != "sitem_1" || item["units"].(float64) != 5 ||
		r.Body["update_behavior"] != "proration-charge-immediately" {
		t.Fatalf("request: %s %s %+v", r.Method, r.Path, r.Body)
	}
	for name, items := range map[string][]SubscriptionItemUpdate{
		"none":     nil,
		"unnamed":  {{Units: 2}},
		"no units": {{ID: "sitem_1"}},
	} {
		if _, err := c.UpdateSubscriptionItems(ctx, "sub_1", items, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}
