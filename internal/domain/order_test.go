package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestOrderStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name    string
		from    OrderStatus
		to      OrderStatus
		allowed bool
	}{
		{"created to accepted", OrderStatusCreated, OrderStatusAccepted, true},
		{"created to cooking (invalid skip)", OrderStatusCreated, OrderStatusCooking, false},
		{"accepted to cooking", OrderStatusAccepted, OrderStatusCooking, true},
		{"cooking to ready", OrderStatusCooking, OrderStatusReady, true},
		{"cooking to delivering (invalid skip)", OrderStatusCooking, OrderStatusDelivering, false},
		{"ready to delivering", OrderStatusReady, OrderStatusDelivering, true},
		{"ready to completed (must go through delivering)", OrderStatusReady, OrderStatusCompleted, false},
		{"delivering to completed", OrderStatusDelivering, OrderStatusCompleted, true},
		{"completed is terminal", OrderStatusCompleted, OrderStatusDelivering, false},
		{"cancelled is terminal", OrderStatusCancelled, OrderStatusAccepted, false},
		{"created to cancelled is not a forward transition", OrderStatusCreated, OrderStatusCancelled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.from.CanTransitionTo(tt.to)
			if got != tt.allowed {
				t.Errorf("OrderStatus(%s).CanTransitionTo(%s) = %v; want %v", tt.from, tt.to, got, tt.allowed)
			}
		})
	}
}

func TestOrderStatus_CanBeCancelledBy(t *testing.T) {
	tests := []struct {
		name    string
		status  OrderStatus
		actor   Actor
		allowed bool
	}{
		{"customer cancels a fresh order", OrderStatusCreated, ActorCustomer, true},
		{"customer cannot cancel once accepted", OrderStatusAccepted, ActorCustomer, false},
		{"customer cannot cancel while cooking", OrderStatusCooking, ActorCustomer, false},
		{"customer cannot cancel a ready order", OrderStatusReady, ActorCustomer, false},

		{"restaurant rejects a fresh order", OrderStatusCreated, ActorRestaurant, true},
		{"restaurant rejects an accepted order", OrderStatusAccepted, ActorRestaurant, true},
		{"restaurant aborts while cooking", OrderStatusCooking, ActorRestaurant, true},
		{"restaurant cannot cancel a ready order", OrderStatusReady, ActorRestaurant, false},
		{"restaurant cannot cancel during delivery", OrderStatusDelivering, ActorRestaurant, false},
		{"restaurant cannot cancel a completed order", OrderStatusCompleted, ActorRestaurant, false},

		{"an unknown actor cancels nothing", OrderStatusCreated, Actor("courier"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.CanBeCancelledBy(tt.actor); got != tt.allowed {
				t.Errorf("OrderStatus(%s).CanBeCancelledBy(%s) = %v; want %v",
					tt.status, tt.actor, got, tt.allowed)
			}
		})
	}
}

func TestCreateOrderInput_Validate(t *testing.T) {
	validUserID := uuid.New()
	validRestID := uuid.New()
	validItemID1 := uuid.New()
	validItemID2 := uuid.New()

	tests := []struct {
		name    string
		input   CreateOrderInput
		wantErr bool
	}{
		{
			name: "valid order",
			input: CreateOrderInput{
				UserID:          validUserID,
				RestaurantID:    validRestID,
				DeliveryAddress: "ул. Ленина, 10, кв. 5",
				Items: []CreateOrderItemInput{
					{MenuItemID: validItemID1, Quantity: 2},
					{MenuItemID: validItemID2, Quantity: 1},
				},
			},
			wantErr: false,
		},
		{
			name: "duplicate item in order",
			input: CreateOrderInput{
				UserID:          validUserID,
				RestaurantID:    validRestID,
				DeliveryAddress: "ул. Ленина, 10, кв. 5",
				Items: []CreateOrderItemInput{
					{MenuItemID: validItemID1, Quantity: 2},
					{MenuItemID: validItemID1, Quantity: 1}, // duplicate!
				},
			},
			wantErr: true,
		},
		{
			name: "empty address",
			input: CreateOrderInput{
				UserID:          validUserID,
				RestaurantID:    validRestID,
				DeliveryAddress: "   ",
				Items: []CreateOrderItemInput{
					{MenuItemID: validItemID1, Quantity: 1},
				},
			},
			wantErr: true,
		},
		{
			name: "empty items",
			input: CreateOrderInput{
				UserID:          validUserID,
				RestaurantID:    validRestID,
				DeliveryAddress: "ул. Ленина, 10",
				Items:           []CreateOrderItemInput{},
			},
			wantErr: true,
		},
		{
			name: "invalid quantity",
			input: CreateOrderInput{
				UserID:          validUserID,
				RestaurantID:    validRestID,
				DeliveryAddress: "ул. Ленина, 10",
				Items: []CreateOrderItemInput{
					{MenuItemID: validItemID1, Quantity: 0},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
