package creem

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

const secret = "whsec_test"

const checkoutCompleted = `{"id":"evt_1","eventType":"checkout.completed","created_at":1893553445678,
"object":{"id":"ch_1","object":"checkout","request_id":"purchase-9","status":"completed","mode":"test",
"metadata":{"purchase":"p_9"},
"order":{"id":"ord_1","customer":"cust_1","product":"prod_1","amount":1500,"currency":"EUR","status":"paid",
"type":"onetime","created_at":"2030-01-02T03:04:05.000Z","updated_at":"2030-01-02T03:04:05.000Z"},
"product":{"id":"prod_1","name":"Credits","price":1000,"currency":"EUR","billing_type":"onetime"},
"customer":{"id":"cust_1","email":"a@example.com","name":"Ann","country":"UA"},
"subscription":null}}`

func TestWebhookCheckoutCompleted(t *testing.T) {
	body := []byte(checkoutCompleted)
	ev, err := NewWebhook(secret).Parse(Sign(secret, body), body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "evt_1" || ev.Type != EventCheckoutCompleted || !ev.CreatedAt.Equal(time.Date(2030, 1, 2, 3, 4, 5, 678000000, time.UTC)) {
		t.Fatalf("envelope: %+v", ev)
	}
	co, err := ev.Checkout()
	if err != nil {
		t.Fatal(err)
	}
	if co.RequestID != "purchase-9" || co.Order.Amount != 1500 || co.Order.Currency != "EUR" || co.Order.Customer.ID != "cust_1" ||
		co.Customer.Object.Email != "a@example.com" || co.Product.Object.Price != 1000 || co.Subscription.ID != "" ||
		co.Metadata["purchase"] != "p_9" {
		t.Fatalf("checkout: %+v", co)
	}
}

func TestWebhookRefundAndDispute(t *testing.T) {
	wh := NewWebhook(secret)
	parse := func(s string) *Event {
		t.Helper()
		ev, err := wh.Parse(Sign(secret, []byte(s)), []byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	ref, err := parse(`{"id":"evt_2","eventType":"refund.created","created_at":1893553445678,"object":{"id":"ref_1",
		"object":"refund","status":"succeeded","refund_amount":1800,"refund_currency":"EUR","reason":"requested_by_customer",
		"transaction":{"id":"tran_1","status":"refunded","amount":1500},"checkout":{"id":"ch_1","request_id":"purchase-9"},
		"order":{"id":"ord_1","amount":1500},"customer":{"id":"cust_1"},"created_at":1893553445678}}`).Refund()
	if err != nil || ref.RefundAmount != 1800 || ref.Transaction.ID != "tran_1" || ref.Checkout.Object.RequestID != "purchase-9" {
		t.Fatalf("refund: %+v %v", ref, err)
	}
	dis, err := parse(`{"id":"evt_3","eventType":"dispute.created","created_at":1893553445678,"object":{"id":"disp_1",
		"object":"dispute","amount":1800,"currency":"EUR","transaction":{"id":"tran_1","status":"chargeback"},
		"checkout":"ch_1","created_at":1893553445678}}`).Dispute()
	if err != nil || dis.Amount != 1800 || dis.Transaction.Object.Status != "chargeback" || dis.Checkout.ID != "ch_1" {
		t.Fatalf("dispute: %+v %v", dis, err)
	}
	sub, err := parse(`{"id":"evt_4","eventType":"subscription.paid","created_at":1893553445678,"object":{"id":"sub_1",
		"status":"active","product":{"id":"prod_1"},"customer":{"id":"cust_1"},"last_transaction_id":"tran_9",
		"current_period_end_date":"2031-01-02T03:04:05.000Z"}}`).Subscription()
	if err != nil || sub.LastTransactionID != "tran_9" || sub.CurrentPeriodEndDate.Year() != 2031 {
		t.Fatalf("subscription: %+v %v", sub, err)
	}
}

func TestWebhookRejects(t *testing.T) {
	body := []byte(checkoutCompleted)
	cases := map[string]struct {
		sig  string
		body []byte
		wh   *Webhook
	}{
		"empty":         {"", body, NewWebhook(secret)},
		"not hex":       {"zz", body, NewWebhook(secret)},
		"wrong secret":  {Sign("other", body), body, NewWebhook(secret)},
		"tampered body": {Sign(secret, body), bytes.Replace(body, []byte("1500"), []byte("150"), 1), NewWebhook(secret)},
		"no secret":     {Sign("", body), body, NewWebhook("")},
	}
	for name, c := range cases {
		if _, err := c.wh.Parse(c.sig, c.body); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: want ErrSignature, got %v", name, err)
		}
	}
	if _, err := NewWebhook("new", WithPreviousSecret(secret)).Parse(Sign(secret, body), body); err != nil {
		t.Fatalf("the previous secret must still verify: %v", err)
	}
	if _, err := NewWebhook(secret).Parse(" "+Sign(secret, body)+"\n", body); err != nil {
		t.Fatalf("whitespace around the signature is tolerated: %v", err)
	}
	noID := []byte(`{"eventType":"checkout.completed","object":{}}`)
	if _, err := NewWebhook(secret).Parse(Sign(secret, noID), noID); err == nil {
		t.Fatal("an event without an id cannot be deduplicated and must be refused")
	}
}

func TestWebhookRequest(t *testing.T) {
	body := []byte(checkoutCompleted)
	r := httptest.NewRequest("POST", "/webhooks/creem", bytes.NewReader(body))
	r.Header.Set(SignatureHeader, Sign(secret, body))
	if ev, err := NewWebhook(secret).ParseRequest(r); err != nil || ev.ID != "evt_1" {
		t.Fatalf("got %+v %v", ev, err)
	}
	r = httptest.NewRequest("POST", "/webhooks/creem", bytes.NewReader(body))
	r.Header.Set(SignatureHeader, Sign(secret, body))
	if _, err := NewWebhook(secret, WithMaxBody(16)).ParseRequest(r); err == nil {
		t.Fatal("an oversized body is refused")
	}
}
