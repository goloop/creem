package creem

import (
	"encoding/json"
	"testing"
)

// FuzzDecode feeds arbitrary JSON to the event shapes: decoding may fail,
// it must never panic.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(checkoutCompleted))
	f.Add([]byte(`{"object":{"order":"x","product":1,"created_at":"x"}}`))
	f.Add([]byte(`{"created_at":1e400}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		var ev Event
		if json.Unmarshal(body, &ev) != nil {
			return
		}
		_, _ = ev.Checkout()
		_, _ = ev.Refund()
		_, _ = ev.Dispute()
		_, _ = ev.Subscription()
	})
}
