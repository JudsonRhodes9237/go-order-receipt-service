package receipt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildEmailForOrderLifecycle(t *testing.T) {
	tests := []struct {
		name        string
		update      OrderUpdate
		wantSend    bool
		wantSubject string
		wantBody    string
	}{
		{
			name:     "completed checkout produces a receipt",
			update:   OrderUpdate{OrderID: "ord-42", CustomerEmail: allowedRecipient, Status: "checkout_completed", AmountCents: 1299, Currency: "usd"},
			wantSend: true, wantSubject: "Receipt for order ord-42", wantBody: "USD 12.99",
		},
		{
			name:     "fulfillment produces a tracking update",
			update:   OrderUpdate{OrderID: "ord-42", CustomerEmail: allowedRecipient, Status: "order_fulfilled", TrackingCode: "TRACK-7"},
			wantSend: true, wantSubject: "Order ord-42 fulfilled", wantBody: "TRACK-7",
		},
		{
			name:     "processing transition remains internal",
			update:   OrderUpdate{OrderID: "ord-42", CustomerEmail: allowedRecipient, Status: "payment_processing"},
			wantSend: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, send, err := BuildEmail(tt.update)
			if err != nil {
				t.Fatal(err)
			}
			if send != tt.wantSend {
				t.Fatalf("send = %v, want %v", send, tt.wantSend)
			}
			if send && (email.Subject != tt.wantSubject || !strings.Contains(email.HTML, tt.wantBody)) {
				t.Fatalf("email = %#v", email)
			}
		})
	}
}

func TestBuildEmailRejectsUnlistedRecipient(t *testing.T) {
	_, send, err := BuildEmail(OrderUpdate{
		OrderID: "ord-42", CustomerEmail: "buyer@example.com", Status: "checkout_completed",
		AmountCents: 1299, Currency: "USD",
	})
	if err == nil || send {
		t.Fatalf("send = %v, err = %v; want rejected recipient", send, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestEmailClientRetriesRateLimitWithSameKey(t *testing.T) {
	var calls int
	client := EmailClient{
		APIKey: "test-key", MaxRetries: 1, BaseDelay: time.Millisecond,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s", req.Method)
			}
			if req.Header.Get("Idempotency-Key") != "stable-key" {
				t.Fatal("idempotency key changed")
			}
			if calls == 1 {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"0"}}, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error":{"message":"retry later"}}`))}, nil
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"data":{"message_id":"msg-42"},"metadata":{}}`))}, nil
		})},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	result, err := client.Send(context.Background(), Email{To: allowedRecipient, Subject: "Receipt", HTML: "<p>Paid</p>"}, "stable-key")
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "msg-42" || calls != 2 {
		t.Fatalf("result = %#v, calls = %d", result, calls)
	}
}

func TestEmailClientRejectsUnlistedRecipientBeforeRequest(t *testing.T) {
	called := false
	client := EmailClient{
		APIKey: "test-key",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("unexpected request")
		})},
	}
	_, err := client.Send(context.Background(), Email{To: "buyer@example.com"}, "stable-key")
	if err == nil || called {
		t.Fatalf("err = %v, request made = %v; want local rejection", err, called)
	}
}
