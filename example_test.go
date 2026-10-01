package creem_test

import (
	"fmt"
	"net/http"
	"time"

	"github.com/goloop/creem"
)

func ExampleNew() {
	c := creem.New("creem_test_...")
	fmt.Println(c.Test())
	// Output: true
}

// ExampleClient_CreateCheckout builds a checkout for an amount decided on the
// server. Sending it needs a real API key, so this example only shows the
// shape.
func ExampleClient_CreateCheckout() {
	req := &creem.CheckoutRequest{
		ProductID:   "prod_credits",
		RequestID:   "purchase-9",
		CustomPrice: 1000, // cents per unit, one-time products only
		Customer:    &creem.CheckoutCustomer{Email: "ann@example.com"},
		SuccessURL:  "https://app.example.com/billing?paid=1",
	}
	fmt.Println(req.ProductID, req.CustomPrice)
	// Output: prod_credits 1000
}

func ExampleWebhook() {
	wh := creem.NewWebhook("whsec_...")

	http.HandleFunc("POST /webhooks/creem", func(w http.ResponseWriter, r *http.Request) {
		ev, err := wh.ParseRequest(r)
		if err != nil {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		switch ev.Type {
		case creem.EventCheckoutCompleted:
			co, err := ev.Checkout()
			if err != nil {
				http.Error(w, "bad event", http.StatusBadRequest)
				return
			}
			_ = co.RequestID // find the purchase and credit it, once per ev.ID
		case creem.EventRefundCreated, creem.EventDisputeCreated:
			// take back what the money bought
		}
		w.WriteHeader(http.StatusOK)
	})
}

func ExampleSign() {
	body := fmt.Appendf(nil, `{"id":"evt_1","eventType":"checkout.completed","created_at":%d,"object":{}}`,
		time.Now().UnixMilli())
	ev, err := creem.NewWebhook("secret").Parse(creem.Sign("secret", body), body)
	fmt.Println(ev.ID, err)
	// Output: evt_1 <nil>
}
