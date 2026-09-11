package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// WebhookDispatcher defines an interface for sending notifications to partners.
type WebhookDispatcher interface {
	Dispatch(ctx context.Context, webhookURL string, eventType string, payload any) error
}

// WebhookPayload wraps event data sent to partner webhook endpoints.
type WebhookPayload struct {
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// HTTPWebhookDispatcher delivers webhooks over HTTP with timeouts and bounded concurrency.
type HTTPWebhookDispatcher struct {
	client *http.Client
	slots  chan struct{}
}

// NewHTTPWebhookDispatcher initializes a dispatcher with the given per-request timeout and a
// ceiling on concurrent deliveries.
func NewHTTPWebhookDispatcher(timeout time.Duration, maxInFlight int) *HTTPWebhookDispatcher {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxInFlight <= 0 {
		maxInFlight = 256
	}
	return &HTTPWebhookDispatcher{
		client: &http.Client{Timeout: timeout},
		slots:  make(chan struct{}, maxInFlight),
	}
}

// Dispatch sends a POST request with JSON payload to partner's webhook URL asynchronously.
func (d *HTTPWebhookDispatcher) Dispatch(ctx context.Context, webhookURL, eventType string, payload any) error {
	if webhookURL == "" {
		return nil
	}

	body := WebhookPayload{
		Event:     eventType,
		Timestamp: time.Now().UTC(),
		Data:      payload,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	select {
	case d.slots <- struct{}{}:
	default:
		return fmt.Errorf("webhook delivery saturated: %d deliveries already in flight", cap(d.slots))
	}

	deliveryCtx := context.WithoutCancel(ctx)

	go func() {
		defer func() { <-d.slots }()

		reqCtx, cancel := context.WithTimeout(deliveryCtx, d.client.Timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, webhookURL, bytes.NewReader(data))
		if err != nil {
			slog.ErrorContext(reqCtx, "failed to create webhook request", "url", webhookURL, "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "GoKitchen-Webhook-Dispatcher/1.0")

		resp, err := d.client.Do(req)
		if err != nil {
			slog.WarnContext(reqCtx, "failed to deliver webhook",
				"url", webhookURL, "event", eventType, "error", err)
			return
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		_, _ = io.Copy(io.Discard, resp.Body)

		if resp.StatusCode >= 400 {
			slog.WarnContext(reqCtx, "webhook returned non-success status",
				"url", webhookURL, "event", eventType, "status", resp.StatusCode)
		} else {
			slog.InfoContext(reqCtx, "webhook successfully delivered",
				"url", webhookURL, "event", eventType, "status", resp.StatusCode)
		}
	}()

	return nil
}
