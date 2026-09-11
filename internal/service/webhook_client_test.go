package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestHTTPWebhookDispatcher_BoundsInFlightDeliveries: a partner whose endpoint hangs must not be
// able to spawn an unbounded number of deliveries. Under load an unbounded dispatcher grew the
// service from 30 MiB to 241 MiB in 24 seconds; the ceiling turns that into visible back-pressure.
func TestHTTPWebhookDispatcher_BoundsInFlightDeliveries(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	inFlight, peak := 0, 0

	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		<-release

		mu.Lock()
		inFlight--
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hanging.Close()

	const ceiling = 3
	d := NewHTTPWebhookDispatcher(5*time.Second, ceiling)

	var dropped int
	for i := 0; i < 50; i++ {
		if err := d.Dispatch(context.Background(), hanging.URL, "order.created", map[string]int{"n": i}); err != nil {
			dropped++
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		reached := peak
		mu.Unlock()
		if reached >= ceiling {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	observedPeak := peak
	mu.Unlock()

	if observedPeak > ceiling {
		t.Errorf("in-flight deliveries exceeded the ceiling: peak %d, ceiling %d", observedPeak, ceiling)
	}
	if dropped == 0 {
		t.Error("expected the dispatcher to report saturation instead of queueing every event")
	}

	close(release)
}

// TestHTTPWebhookDispatcher_SlotsAreReleased: once a delivery finishes its slot must return to
// the pool, otherwise the dispatcher would permanently wedge itself after the first burst.
func TestHTTPWebhookDispatcher_SlotsAreReleased(t *testing.T) {
	var served int
	var mu sync.Mutex
	done := make(chan struct{}, 10)

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		served++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		done <- struct{}{}
	}))
	defer fast.Close()

	d := NewHTTPWebhookDispatcher(2*time.Second, 1)

	dispatch := func() error {
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := d.Dispatch(context.Background(), fast.URL, "order.created", nil)
			if err == nil || time.Now().After(deadline) {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	for i := 0; i < 5; i++ {
		if err := dispatch(); err != nil {
			t.Fatalf("slot was never released: %v", err)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("delivery did not reach the server")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if served < 5 {
		t.Errorf("expected at least 5 deliveries, got %d", served)
	}
}
