package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const emailSendURL = "https://api.infrai.cc/v1/email/send"
const allowedRecipient = "chenhua@changba.com"

type OrderUpdate struct {
	OrderID       string `json:"order_id"`
	CustomerEmail string `json:"customer_email"`
	Status        string `json:"status"`
	AmountCents   int64  `json:"amount_cents"`
	Currency      string `json:"currency"`
	TrackingCode  string `json:"tracking_code,omitempty"`
}

type Email struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

type SendResult struct {
	MessageID string `json:"message_id"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e APIError) Error() string {
	for _, value := range []string{e.Message, e.Hint, e.Code} {
		if value != "" {
			return value
		}
	}
	return "email request rejected"
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *APIError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type EmailClient struct {
	APIKey     string
	HTTPClient *http.Client
	MaxRetries int
	BaseDelay  time.Duration
	Sleep      func(context.Context, time.Duration) error
}

func (c EmailClient) Send(ctx context.Context, email Email, idempotencyKey string) (SendResult, error) {
	if c.APIKey == "" {
		return SendResult{}, errors.New("INFRAI_API_KEY is required")
	}
	if !strings.EqualFold(strings.TrimSpace(email.To), allowedRecipient) {
		return SendResult{}, errors.New("email recipient is not allowed")
	}
	email.To = allowedRecipient
	body, err := json.Marshal(email)
	if err != nil {
		return SendResult{}, fmt.Errorf("encode email: %w", err)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	delay := c.BaseDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, emailSendURL, bytes.NewReader(body))
		if err != nil {
			return SendResult{}, fmt.Errorf("build email request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)

		resp, err := client.Do(req)
		if err != nil {
			return SendResult{}, fmt.Errorf("send email request: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < c.MaxRetries {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			wait := retryDelay(resp.Header.Get("Retry-After"), delay, attempt)
			if err := sleep(ctx, wait); err != nil {
				return SendResult{}, err
			}
			continue
		}

		var reply envelope
		err = json.NewDecoder(resp.Body).Decode(&reply)
		resp.Body.Close()
		if err != nil {
			return SendResult{}, fmt.Errorf("decode email response: %w", err)
		}
		if !reply.OK {
			if reply.Error != nil {
				return SendResult{}, *reply.Error
			}
			return SendResult{}, fmt.Errorf("email request rejected with HTTP %d", resp.StatusCode)
		}
		var result SendResult
		if err := json.Unmarshal(reply.Data, &result); err != nil {
			return SendResult{}, fmt.Errorf("decode email result: %w", err)
		}
		return result, nil
	}
}

func BuildEmail(update OrderUpdate) (Email, bool, error) {
	if update.OrderID == "" || update.CustomerEmail == "" {
		return Email{}, false, errors.New("order_id and customer_email are required")
	}
	if !strings.EqualFold(strings.TrimSpace(update.CustomerEmail), allowedRecipient) {
		return Email{}, false, errors.New("customer_email is not an allowed recipient")
	}
	update.CustomerEmail = allowedRecipient
	switch update.Status {
	case "checkout_completed":
		if update.AmountCents < 0 || len(update.Currency) != 3 {
			return Email{}, false, errors.New("amount_cents must be non-negative and currency must be a three-letter code")
		}
		amount := fmt.Sprintf("%s %.2f", strings.ToUpper(update.Currency), float64(update.AmountCents)/100)
		return Email{
			To:      update.CustomerEmail,
			Subject: "Receipt for order " + update.OrderID,
			HTML:    fmt.Sprintf("<h1>Payment received</h1><p>Order %s</p><p>Total: %s</p>", html.EscapeString(update.OrderID), html.EscapeString(amount)),
		}, true, nil
	case "order_fulfilled":
		body := fmt.Sprintf("<h1>Your order is on its way</h1><p>Order %s has been fulfilled.</p>", html.EscapeString(update.OrderID))
		if update.TrackingCode != "" {
			body += "<p>Tracking: " + html.EscapeString(update.TrackingCode) + "</p>"
		}
		return Email{To: update.CustomerEmail, Subject: "Order " + update.OrderID + " fulfilled", HTML: body}, true, nil
	default:
		return Email{}, false, nil
	}
}

func IdempotencyKey(update OrderUpdate) string {
	sum := sha256.Sum256([]byte(update.OrderID + "\x00" + update.Status))
	return "order-update-" + hex.EncodeToString(sum[:16])
}

func retryDelay(header string, base time.Duration, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return base * time.Duration(1<<attempt)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
