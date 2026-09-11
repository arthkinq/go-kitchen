package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// OrderStatus defines the operational lifecycle status of an order.
type OrderStatus string

// OrderStatus definitions for state machine.
const (
	OrderStatusCreated    OrderStatus = "created"
	OrderStatusAccepted   OrderStatus = "accepted"
	OrderStatusCooking    OrderStatus = "cooking"
	OrderStatusReady      OrderStatus = "ready"
	OrderStatusDelivering OrderStatus = "delivering"
	OrderStatusCompleted  OrderStatus = "completed"
	OrderStatusCancelled  OrderStatus = "cancelled"
)

// IsValid checks if status is a known domain status.
func (s OrderStatus) IsValid() bool {
	switch s {
	case OrderStatusCreated, OrderStatusAccepted, OrderStatusCooking,
		OrderStatusReady, OrderStatusDelivering, OrderStatusCompleted, OrderStatusCancelled:
		return true
	default:
		return false
	}
}

// Actor identifies who initiates a change in the order lifecycle.
type Actor string

// Actor definitions.
const (
	ActorCustomer   Actor = "customer"
	ActorRestaurant Actor = "restaurant"
	ActorSystem     Actor = "system"
)

// forwardTransitions is the single source of truth for the forward order lifecycle.
// Cancellation is deliberately absent here: it depends on the actor and lives in CanBeCancelledBy.
var forwardTransitions = map[OrderStatus][]OrderStatus{
	OrderStatusCreated:    {OrderStatusAccepted},
	OrderStatusAccepted:   {OrderStatusCooking},
	OrderStatusCooking:    {OrderStatusReady},
	OrderStatusReady:      {OrderStatusDelivering},
	OrderStatusDelivering: {OrderStatusCompleted},
}

// cancellableBy is the single source of truth for who may cancel an order in a given status.
var cancellableBy = map[Actor][]OrderStatus{
	ActorCustomer:   {OrderStatusCreated},
	ActorRestaurant: {OrderStatusCreated, OrderStatusAccepted, OrderStatusCooking},
}

// CanTransitionTo reports whether the forward lifecycle allows moving from s to target.
// Use CanBeCancelledBy for transitions to OrderStatusCancelled.
func (s OrderStatus) CanTransitionTo(target OrderStatus) bool {
	for _, allowed := range forwardTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// CanBeCancelledBy reports whether actor is permitted to cancel an order currently in status s.
func (s OrderStatus) CanBeCancelledBy(actor Actor) bool {
	for _, allowed := range cancellableBy[actor] {
		if allowed == s {
			return true
		}
	}
	return false
}

// Order represents a customer food order.
type Order struct {
	ID              uuid.UUID            `json:"id"`
	UserID          uuid.UUID            `json:"user_id"`
	RestaurantID    uuid.UUID            `json:"restaurant_id"`
	Status          OrderStatus          `json:"status"`
	TotalPriceCents int64                `json:"total_price_cents"`
	DeliveryAddress string               `json:"delivery_address"`
	Comment         string               `json:"comment"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	Items           []OrderItem          `json:"items,omitempty"`
	StatusHistory   []OrderStatusHistory `json:"status_history,omitempty"`
}

// OrderItem is a snapshot of an ordered item at the moment of order placement.
type OrderItem struct {
	ID         uuid.UUID `json:"id"`
	OrderID    uuid.UUID `json:"order_id"`
	MenuItemID uuid.UUID `json:"menu_item_id"`
	ItemName   string    `json:"item_name"`
	PriceCents int64     `json:"price_cents"`
	Quantity   int       `json:"quantity"`
}

// OrderStatusHistory tracks state changes for auditing and timeline display.
type OrderStatusHistory struct {
	ID         uuid.UUID   `json:"id"`
	OrderID    uuid.UUID   `json:"order_id"`
	FromStatus OrderStatus `json:"from_status"`
	ToStatus   OrderStatus `json:"to_status"`
	Actor      Actor       `json:"actor"`
	Comment    string      `json:"comment"`
	CreatedAt  time.Time   `json:"created_at"`
}

// CreateOrderItemInput represents a single requested item when placing an order.
type CreateOrderItemInput struct {
	MenuItemID uuid.UUID `json:"menu_item_id"`
	Quantity   int       `json:"quantity"`
}

// CreateOrderInput represents payload from customer web client.
type CreateOrderInput struct {
	UserID          uuid.UUID              `json:"user_id"`
	RestaurantID    uuid.UUID              `json:"restaurant_id"`
	DeliveryAddress string                 `json:"delivery_address"`
	Comment         string                 `json:"comment"`
	Items           []CreateOrderItemInput `json:"items"`
}

// Validate verifies input data correctness before business processing.
func (in *CreateOrderInput) Validate() error {
	if in.UserID == uuid.Nil || in.RestaurantID == uuid.Nil {
		return ErrInvalidInput
	}
	if strings.TrimSpace(in.DeliveryAddress) == "" || len(in.DeliveryAddress) > MaxAddressLen {
		return ErrInvalidInput
	}
	if len(in.Comment) > MaxFreeTextLen {
		return ErrInvalidInput
	}
	if len(in.Items) == 0 {
		return ErrEmptyOrder
	}
	seen := make(map[uuid.UUID]struct{}, len(in.Items))
	for _, it := range in.Items {
		if it.MenuItemID == uuid.Nil || it.Quantity <= 0 {
			return ErrInvalidInput
		}
		if _, exists := seen[it.MenuItemID]; exists {
			return ErrInvalidInput
		}
		seen[it.MenuItemID] = struct{}{}
	}
	return nil
}

// UpdateOrderStatusInput holds request to change order status by restaurant or system.
type UpdateOrderStatusInput struct {
	Status  OrderStatus `json:"status"`
	Comment string      `json:"comment"`
}

// Validate checks if the requested status is valid.
func (in *UpdateOrderStatusInput) Validate() error {
	if !in.Status.IsValid() {
		return ErrInvalidStatusTransition
	}
	if len(in.Comment) > MaxFreeTextLen {
		return ErrInvalidInput
	}
	return nil
}

// OrderFilter contains filtering parameters for order lists.
type OrderFilter struct {
	UserID       *uuid.UUID
	RestaurantID *uuid.UUID
	Status       *OrderStatus
	Limit        int
	Offset       int
}
