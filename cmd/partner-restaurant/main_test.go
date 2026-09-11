package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPartnerServer_HealthCheck(t *testing.T) {
	cfg := Config{Port: "8081", AutoCook: false}
	server := NewPartnerServer(context.Background(), cfg)
	defer server.Close()
	router := server.Routes()

	req := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestPartnerServer_HandleWebhook_Success(t *testing.T) {
	cfg := Config{Port: "8081", AutoCook: false}
	server := NewPartnerServer(context.Background(), cfg)
	defer server.Close()
	router := server.Routes()

	orderID := uuid.New()
	orderData, _ := json.Marshal(OrderData{
		ID:           orderID,
		RestaurantID: uuid.New(),
		Status:       "created",
		TotalPrice:   50000,
	})

	evt := WebhookEvent{
		Event:     "order.created",
		Timestamp: time.Now(),
		Data:      orderData,
	}

	body, _ := json.Marshal(evt)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d; body: %s", rr.Code, rr.Body.String())
	}

	reqEvents := httptest.NewRequest(http.MethodGet, "/events", http.NoBody)
	rrEvents := httptest.NewRecorder()
	router.ServeHTTP(rrEvents, reqEvents)

	if rrEvents.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /events, got %d", rrEvents.Code)
	}
}

func TestPartnerServer_HandleWebhook_MalformedJSON(t *testing.T) {
	cfg := Config{Port: "8081", AutoCook: false}
	server := NewPartnerServer(context.Background(), cfg)
	defer server.Close()
	router := server.Routes()

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("invalid-json{"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", rr.Code)
	}
}

func TestPartnerServer_UpdateOrderStatus_Call(t *testing.T) {
	receivedMethod := ""
	receivedPath := ""
	receivedStatus := ""

	mockKitchenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path

		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		receivedStatus = payload["status"]

		w.WriteHeader(http.StatusOK)
	}))
	defer mockKitchenServer.Close()

	cfg := Config{
		Port:          "8081",
		KitchenAPIURL: mockKitchenServer.URL,
		AutoCook:      false,
	}
	server := NewPartnerServer(context.Background(), cfg)
	defer server.Close()

	orderID := uuid.New()
	err := server.updateOrderStatus(context.Background(), orderID, "cooking", "Начали готовить")
	if err != nil {
		t.Fatalf("unexpected error updating status: %v", err)
	}

	if receivedMethod != http.MethodPatch {
		t.Errorf("expected PATCH method, got %s", receivedMethod)
	}
	expectedPath := "/api/v1/partner/orders/" + orderID.String() + "/status"
	if receivedPath != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, receivedPath)
	}
	if receivedStatus != "cooking" {
		t.Errorf("expected status 'cooking', got %s", receivedStatus)
	}
}

func TestPartnerServer_AutoCook_Lifecycle(t *testing.T) {
	var mu sync.Mutex
	statusesReceived := make([]string, 0, 3)

	mockKitchenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)

		mu.Lock()
		statusesReceived = append(statusesReceived, payload["status"])
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer mockKitchenServer.Close()

	cfg := Config{
		Port:          "8081",
		KitchenAPIURL: mockKitchenServer.URL,
		AutoCook:      true,
		StepInterval:  10 * time.Millisecond, // Fast interval for testing
	}
	server := NewPartnerServer(context.Background(), cfg)
	defer server.Close()
	router := server.Routes()

	orderID := uuid.New()
	orderData, _ := json.Marshal(OrderData{ID: orderID, Status: "created"})
	evt := WebhookEvent{Event: "order.created", Timestamp: time.Now(), Data: orderData}
	body, _ := json.Marshal(evt)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}

	server.Wait()

	mu.Lock()
	defer mu.Unlock()

	expected := []string{"accepted", "cooking", "ready"}
	if len(statusesReceived) != len(expected) {
		t.Fatalf("expected %d status transitions, got %d: %v", len(expected), len(statusesReceived), statusesReceived)
	}
	for i, st := range expected {
		if statusesReceived[i] != st {
			t.Errorf("step %d: expected %s, got %s", i, st, statusesReceived[i])
		}
	}
}

// TestPartnerServer_UpdateOrderStatus_SendsAPIKey checks that the simulator authenticates
// against the closed partner area.
func TestPartnerServer_UpdateOrderStatus_SendsAPIKey(t *testing.T) {
	var gotAuth string

	kitchen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer kitchen.Close()

	server := NewPartnerServer(context.Background(), Config{
		KitchenAPIURL: kitchen.URL,
		APIKey:        "secret-key",
	})
	defer server.Close()

	if err := server.updateOrderStatus(context.Background(), uuid.New(), "cooking", ""); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("expected the api key in the Authorization header, got %q", gotAuth)
	}
}

// TestPartnerServer_RetriesTransientFailures covers the reliability gap where a single failed
// call used to strand an order forever.
func TestPartnerServer_RetriesTransientFailures(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	kitchen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()

		if current == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer kitchen.Close()

	server := NewPartnerServer(context.Background(), Config{KitchenAPIURL: kitchen.URL})
	defer server.Close()

	if err := server.updateOrderStatusWithRetry(context.Background(), uuid.New(), "accepted", ""); err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Errorf("expected exactly 2 attempts, got %d", attempts)
	}
}

// TestPartnerServer_DoesNotRetryPermanentRejections: a 4xx means the platform refused the
// request itself, so repeating it would only add load.
func TestPartnerServer_DoesNotRetryPermanentRejections(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	kitchen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer kitchen.Close()

	server := NewPartnerServer(context.Background(), Config{KitchenAPIURL: kitchen.URL})
	defer server.Close()

	err := server.updateOrderStatusWithRetry(context.Background(), uuid.New(), "accepted", "")
	if !errors.Is(err, errPermanent) {
		t.Fatalf("expected a permanent rejection, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt, got %d", attempts)
	}
}
