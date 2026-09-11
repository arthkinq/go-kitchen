package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/repository/inmem"
)

type orderFixture struct {
	svc      *OrderService
	restRepo *inmem.RestaurantRepository
	menuRepo *inmem.MenuRepository
	webhook  *inmem.WebhookRecorder
	restID   uuid.UUID
}

// newOrderFixture wires an OrderService over in-memory repositories with one active restaurant.
func newOrderFixture() *orderFixture {
	restRepo := inmem.NewRestaurantRepository()
	menuRepo := inmem.NewMenuRepository()
	orderRepo := inmem.NewOrderRepository()
	webhook := &inmem.WebhookRecorder{}

	restID := uuid.New()
	restRepo.Put(&domain.Restaurant{
		ID:         restID,
		Name:       "Burger House",
		IsActive:   true,
		WebhookURL: "https://burger.example.com/webhook",
	})

	return &orderFixture{
		svc:      NewOrderService(orderRepo, menuRepo, restRepo, &inmem.TxManager{}, webhook),
		restRepo: restRepo,
		menuRepo: menuRepo,
		webhook:  webhook,
		restID:   restID,
	}
}

// addDish registers an available dish with the given price and stock.
func (f *orderFixture) addDish(name string, priceCents int64, stock int) uuid.UUID {
	id := uuid.New()
	f.menuRepo.PutItem(&domain.MenuItem{
		ID:            id,
		RestaurantID:  f.restID,
		Name:          name,
		PriceCents:    priceCents,
		StockQuantity: stock,
		IsAvailable:   true,
	})
	return id
}

func (f *orderFixture) order(t *testing.T, userID uuid.UUID, items ...domain.CreateOrderItemInput) *domain.Order {
	t.Helper()
	order, err := f.svc.CreateOrder(context.Background(), domain.CreateOrderInput{
		UserID:          userID,
		RestaurantID:    f.restID,
		DeliveryAddress: "ul. Pushkina, 1",
		Items:           items,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	return order
}

func TestOrderService_CreateOrder_MultiItem_Success(t *testing.T) {
	f := newOrderFixture()

	burger := f.addDish("Burger", 30000, 10)
	fries := f.addDish("Fries", 15000, 20)

	order := f.order(t, uuid.New(),
		domain.CreateOrderItemInput{MenuItemID: burger, Quantity: 2},
		domain.CreateOrderItemInput{MenuItemID: fries, Quantity: 3},
	)

	if order.TotalPriceCents != 105000 {
		t.Errorf("expected TotalPriceCents 105000, got %d", order.TotalPriceCents)
	}
	if got := f.menuRepo.StockOf(burger); got != 8 {
		t.Errorf("expected burger stock 8, got %d", got)
	}
	if got := f.menuRepo.StockOf(fries); got != 17 {
		t.Errorf("expected fries stock 17, got %d", got)
	}

	if len(order.Items) != 2 {
		t.Fatalf("expected 2 receipt lines, got %d", len(order.Items))
	}

	if events := f.webhook.Dispatched(); len(events) != 1 || events[0] != "order.created" {
		t.Errorf("expected a single order.created webhook, got %v", events)
	}
	if urls := f.webhook.DispatchedURLs(); len(urls) != 1 || urls[0] != "https://burger.example.com/webhook" {
		t.Errorf("webhook must be addressed to the establishment's own url, got %v", urls)
	}
}

func TestOrderService_CreateOrder_OutOfStock(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Cheeseburger", 35000, 1)

	_, err := f.svc.CreateOrder(context.Background(), domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    f.restID,
		DeliveryAddress: "ul. Pushkina, 1",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: itemID, Quantity: 5}},
	})
	if !errors.Is(err, domain.ErrOutOfStock) {
		t.Fatalf("expected ErrOutOfStock, got %v", err)
	}
	if got := f.menuRepo.StockOf(itemID); got != 1 {
		t.Errorf("stock must stay untouched on a rejected order, got %d", got)
	}
}

func TestOrderService_CreateOrder_InactiveRestaurant(t *testing.T) {
	f := newOrderFixture()
	closedID := uuid.New()
	f.restRepo.Put(&domain.Restaurant{ID: closedID, Name: "Closed", IsActive: false})

	_, err := f.svc.CreateOrder(context.Background(), domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    closedID,
		DeliveryAddress: "ul. Pushkina, 1",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: uuid.New(), Quantity: 1}},
	})
	if !errors.Is(err, domain.ErrRestaurantInactive) {
		t.Fatalf("expected ErrRestaurantInactive, got %v", err)
	}
}

func TestOrderService_StatusLifecycleAndStockRefund(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Pizza", 50000, 10)
	order := f.order(t, uuid.New(), domain.CreateOrderItemInput{MenuItemID: itemID, Quantity: 3})

	if got := f.menuRepo.StockOf(itemID); got != 7 {
		t.Fatalf("expected stock 7, got %d", got)
	}

	ctx := context.Background()
	accepted, err := f.svc.UpdateOrderStatus(ctx, f.restID, order.ID,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusAccepted})
	if err != nil {
		t.Fatalf("accept order: %v", err)
	}
	if accepted.Status != domain.OrderStatusAccepted {
		t.Errorf("expected status accepted, got %s", accepted.Status)
	}

	cancelled, err := f.svc.UpdateOrderStatus(ctx, f.restID, order.ID,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusCancelled, Comment: "Kukhnia peregruzhena"})
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if got := f.menuRepo.StockOf(itemID); got != 10 {
		t.Errorf("expected stock refunded to 10, got %d", got)
	}

	var sawRestaurantCancel bool
	for _, h := range cancelled.StatusHistory {
		if h.ToStatus == domain.OrderStatusCancelled && h.Actor == domain.ActorRestaurant {
			sawRestaurantCancel = true
		}
	}
	if !sawRestaurantCancel {
		t.Errorf("expected an audit entry attributing the cancellation to the restaurant, got %+v",
			cancelled.StatusHistory)
	}
}

func TestOrderService_RestaurantCannotCancelReadyOrder(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Pizza", 50000, 10)
	order := f.order(t, uuid.New(), domain.CreateOrderItemInput{MenuItemID: itemID, Quantity: 1})

	ctx := context.Background()
	for _, st := range []domain.OrderStatus{domain.OrderStatusAccepted, domain.OrderStatusCooking, domain.OrderStatusReady} {
		if _, err := f.svc.UpdateOrderStatus(ctx, f.restID, order.ID, domain.UpdateOrderStatusInput{Status: st}); err != nil {
			t.Fatalf("advance to %s: %v", st, err)
		}
	}

	_, err := f.svc.UpdateOrderStatus(ctx, f.restID, order.ID,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusCancelled})
	if !errors.Is(err, domain.ErrOrderAlreadyProcessed) {
		t.Fatalf("expected ErrOrderAlreadyProcessed for cancelling a ready order, got %v", err)
	}
	if got := f.menuRepo.StockOf(itemID); got != 9 {
		t.Errorf("a refused cancellation must not refund stock, got %d", got)
	}
}

func TestOrderService_UpdateOrderStatus_SkippingAStageIsRejected(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Pizza", 50000, 5)
	order := f.order(t, uuid.New(), domain.CreateOrderItemInput{MenuItemID: itemID, Quantity: 1})

	_, err := f.svc.UpdateOrderStatus(context.Background(), f.restID, order.ID,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusReady})
	if !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Fatalf("expected ErrInvalidStatusTransition for created -> ready, got %v", err)
	}
}

// TestOrderService_UpdateOrderStatus_ForeignRestaurant covers the closed partner area: an
// establishment must not be able to touch another establishment's orders.
func TestOrderService_UpdateOrderStatus_ForeignRestaurant(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Pizza", 50000, 5)
	order := f.order(t, uuid.New(), domain.CreateOrderItemInput{MenuItemID: itemID, Quantity: 1})

	intruderID := uuid.New()
	f.restRepo.Put(&domain.Restaurant{ID: intruderID, Name: "Intruder", IsActive: true})

	_, err := f.svc.UpdateOrderStatus(context.Background(), intruderID, order.ID,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusAccepted})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a foreign order, got %v", err)
	}
}

func TestOrderService_CancelOrderByCustomer_IDORDefence(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Wok", 40000, 5)

	userA, userB := uuid.New(), uuid.New()
	order := f.order(t, userA, domain.CreateOrderItemInput{MenuItemID: itemID, Quantity: 1})

	_, err := f.svc.CancelOrderByCustomer(context.Background(), order.ID, userB, "chuzhoi zakaz")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for another customer's order, got %v", err)
	}
	if got := f.menuRepo.StockOf(itemID); got != 4 {
		t.Errorf("a refused cancellation must not refund stock, got %d", got)
	}
}

func TestOrderService_CancelOrderByCustomer_RefundsAndIsNotRepeatable(t *testing.T) {
	f := newOrderFixture()
	itemID := f.addDish("Chizburger", 45000, 10)
	userID := uuid.New()
	order := f.order(t, userID, domain.CreateOrderItemInput{MenuItemID: itemID, Quantity: 4})

	ctx := context.Background()
	cancelled, err := f.svc.CancelOrderByCustomer(ctx, order.ID, userID, "peredumal")
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if cancelled.Status != domain.OrderStatusCancelled {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if got := f.menuRepo.StockOf(itemID); got != 10 {
		t.Errorf("expected stock restored to 10, got %d", got)
	}

	if _, err := f.svc.CancelOrderByCustomer(ctx, order.ID, userID, "eshchio raz"); !errors.Is(err, domain.ErrOrderAlreadyProcessed) {
		t.Fatalf("expected ErrOrderAlreadyProcessed on the second cancel, got %v", err)
	}
	if got := f.menuRepo.StockOf(itemID); got != 10 {
		t.Errorf("a repeated cancellation must not refund twice, got %d", got)
	}
}

func TestOrderService_CreateOrder_RejectsItemsOfAnotherRestaurant(t *testing.T) {
	f := newOrderFixture()
	ourDish := f.addDish("Sup", 20000, 5)

	otherID := uuid.New()
	f.restRepo.Put(&domain.Restaurant{ID: otherID, Name: "Kafe 2", IsActive: true})
	foreignDish := uuid.New()
	f.menuRepo.PutItem(&domain.MenuItem{
		ID: foreignDish, RestaurantID: otherID, Name: "Salat",
		PriceCents: 15000, StockQuantity: 5, IsAvailable: true,
	})

	_, err := f.svc.CreateOrder(context.Background(), domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    f.restID,
		DeliveryAddress: "ul. Mira, 1",
		Items: []domain.CreateOrderItemInput{
			{MenuItemID: ourDish, Quantity: 1},
			{MenuItemID: foreignDish, Quantity: 1},
		},
	})
	if !errors.Is(err, domain.ErrRestaurantMismatch) {
		t.Fatalf("expected ErrRestaurantMismatch, got %v", err)
	}
}

func TestOrderService_CreateOrder_RejectsStopListedDish(t *testing.T) {
	f := newOrderFixture()
	stopped := uuid.New()
	f.menuRepo.PutItem(&domain.MenuItem{
		ID: stopped, RestaurantID: f.restID, Name: "Stop-bliudo",
		PriceCents: 15000, StockQuantity: 5, IsAvailable: false,
	})

	_, err := f.svc.CreateOrder(context.Background(), domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    f.restID,
		DeliveryAddress: "ul. Mira, 1",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: stopped, Quantity: 1}},
	})
	if !errors.Is(err, domain.ErrItemUnavailable) {
		t.Fatalf("expected ErrItemUnavailable, got %v", err)
	}
}
