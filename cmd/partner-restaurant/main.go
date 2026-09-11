package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

const (
	maxEventsHistory     = 1000
	statusUpdateAttempts = 3
	retryBackoff         = 500 * time.Millisecond
)

// errPermanent marks a response the kitchen must not retry: the platform rejected the request
// itself (bad credentials, illegal transition), so repeating it changes nothing.
var errPermanent = errors.New("permanent rejection")

// Config holds runtime parameters for the partner restaurant demo simulator.
type Config struct {
	Port          string
	KitchenAPIURL string
	APIKey        string
	AutoCook      bool
	StepInterval  time.Duration
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig() Config {
	autoCook, err := strconv.ParseBool(getEnv("AUTO_COOK", "true"))
	if err != nil {
		autoCook = true
	}

	stepInterval, err := time.ParseDuration(getEnv("STEP_INTERVAL", "1500ms"))
	if err != nil || stepInterval <= 0 {
		stepInterval = 1500 * time.Millisecond
	}

	return Config{
		Port:          getEnv("PORT", "8081"),
		KitchenAPIURL: getEnv("KITCHEN_API_URL", "http://localhost:8080"),
		APIKey:        os.Getenv("PARTNER_API_KEY"),
		AutoCook:      autoCook,
		StepInterval:  stepInterval,
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// WebhookEvent represents an incoming notification from the Go.Kitchen service.
type WebhookEvent struct {
	Event     string          `json:"event"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// OrderData represents the minimal order payload received in webhooks.
type OrderData struct {
	ID           uuid.UUID `json:"id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	Status       string    `json:"status"`
	TotalPrice   int64     `json:"total_price_cents"`
}

// PartnerServer encapsulates partner restaurant state, webhook processing and kitchen simulator.
type PartnerServer struct {
	cfg        Config
	httpClient *http.Client
	lifetime   context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.RWMutex
	events     []WebhookEvent
}

// NewPartnerServer constructs a new PartnerServer.
func NewPartnerServer(ctx context.Context, cfg Config) *PartnerServer {
	serverCtx, cancel := context.WithCancel(ctx)
	return &PartnerServer{
		cfg:        cfg,
		lifetime:   serverCtx,
		cancel:     cancel,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		events:     make([]WebhookEvent, 0, 100),
	}
}

// Wait blocks until all ongoing cooking simulation routines complete.
func (s *PartnerServer) Wait() {
	s.wg.Wait()
}

// Close cancels active kitchen simulation routines and waits for them to complete.
func (s *PartnerServer) Close() {
	s.cancel()
	s.wg.Wait()
}

// HandleWebhook processes incoming webhook POST requests.
func (s *PartnerServer) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var evt WebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		slog.Warn("failed to parse webhook body", "error", err)
		http.Error(w, `{"error":"invalid payload"}`, http.StatusBadRequest)
		return
	}

	slog.Info("received webhook event", "event", evt.Event, "timestamp", evt.Timestamp)

	s.mu.Lock()
	if len(s.events) >= maxEventsHistory {
		s.events = s.events[1:]
	}
	s.events = append(s.events, evt)
	s.mu.Unlock()

	if evt.Event == "order.created" && s.cfg.AutoCook {
		var order OrderData
		if err := json.Unmarshal(evt.Data, &order); err == nil && order.ID != uuid.Nil {
			s.wg.Add(1)
			go func(orderID uuid.UUID) {
				defer s.wg.Done()
				s.simulateCookingLifecycle(s.lifetime, orderID)
			}(order.ID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"acknowledged"}`))
}

// cookingStep is one stage of the simulated kitchen workflow.
type cookingStep struct {
	status  string
	comment string
}

// simulateCookingLifecycle walks an order through the kitchen: accepted -> cooking -> ready.
func (s *PartnerServer) simulateCookingLifecycle(ctx context.Context, orderID uuid.UUID) {
	slog.Info("kitchen received order, initiating cooking lifecycle", "order_id", orderID)

	steps := []cookingStep{
		{"accepted", "Заказ подтвержден рестораном и передан шеф-повару"},
		{"cooking", "Повар начал приготовление блюд на горячей кухне"},
		{"ready", "Блюда упакованы в термопакеты и готовы к выдаче курьеру"},
	}

	for _, step := range steps {
		select {
		case <-ctx.Done():
			slog.Info("cooking lifecycle cancelled", "order_id", orderID, "pending_status", step.status)
			return
		case <-time.After(s.cfg.StepInterval):
		}

		if err := s.updateOrderStatusWithRetry(ctx, orderID, step.status, step.comment); err != nil {
			slog.Error("kitchen workflow stopped",
				"order_id", orderID, "status", step.status, "error", err)
			return
		}
		slog.Info("order status advanced", "order_id", orderID, "status", step.status)
	}
}

// updateOrderStatusWithRetry retries transient failures. A permanent rejection (4xx) means the
// order already moved on - somebody else advanced or cancelled it - so retrying is pointless.
func (s *PartnerServer) updateOrderStatusWithRetry(ctx context.Context, orderID uuid.UUID, status, comment string) error {
	var lastErr error

	for attempt := 1; attempt <= statusUpdateAttempts; attempt++ {
		err := s.updateOrderStatus(ctx, orderID, status, comment)
		if err == nil {
			return nil
		}
		if errors.Is(err, errPermanent) {
			return err
		}

		lastErr = err
		slog.Warn("status update failed, retrying",
			"order_id", orderID, "status", status, "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryBackoff * time.Duration(attempt)):
		}
	}

	return fmt.Errorf("giving up after %d attempts: %w", statusUpdateAttempts, lastErr)
}

// updateOrderStatus sends a PATCH request to the main Go.Kitchen service.
func (s *PartnerServer) updateOrderStatus(ctx context.Context, orderID uuid.UUID, status, comment string) error {
	url := fmt.Sprintf("%s/api/v1/partner/orders/%s/status", s.cfg.KitchenAPIURL, orderID.String())

	bodyData, err := json.Marshal(map[string]string{"status": status, "comment": comment})
	if err != nil {
		return fmt.Errorf("marshal status update: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(bodyData))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GoKitchen-Partner-Restaurant-Simulator/1.0")
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dispatch status update: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("status update returned status %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: status %d", errPermanent, resp.StatusCode)
	default:
		return nil
	}
}

// ListEvents returns recent received webhook events for debugging.
func (s *PartnerServer) ListEvents(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	eventsCopy := make([]WebhookEvent, len(s.events))
	copy(eventsCopy, s.events)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count":  len(eventsCopy),
		"events": eventsCopy,
	})
}

// Routes builds the Chi router for the partner restaurant service.
func (s *PartnerServer) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)

	healthHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"partner-restaurant"}`))
	}
	r.Get("/healthz", healthHandler)
	r.Head("/healthz", healthHandler)

	r.Post("/webhook", s.HandleWebhook)
	r.Get("/events", s.ListEvents)

	return r
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := run(); err != nil {
		slog.Error("partner service terminated with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := LoadConfig()
	slog.Info("starting partner restaurant simulator...",
		"port", cfg.Port,
		"kitchen_api_url", cfg.KitchenAPIURL,
		"auto_cook", cfg.AutoCook,
		"api_key_configured", cfg.APIKey != "",
	)
	if cfg.APIKey == "" {
		slog.Warn("PARTNER_API_KEY is not set: status updates will be rejected with 401")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := NewPartnerServer(ctx, cfg)
	defer server.Close()

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      server.Routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("partner restaurant listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("partner server error: %w", err)
	case <-ctx.Done():
		slog.Info("stopping partner restaurant simulator...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	slog.Info("partner restaurant stopped cleanly")
	return nil
}
