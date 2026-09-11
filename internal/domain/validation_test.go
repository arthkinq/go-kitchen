package domain

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCreateRestaurantInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   CreateRestaurantInput
		wantErr bool
	}{
		{
			name: "valid restaurant",
			input: CreateRestaurantInput{
				Name:        "Додо Пицца",
				Description: "Пиццерия",
				Address:     "Москва, ул. Тверская 1",
				WebhookURL:  "https://dodo.example.com/webhook",
			},
			wantErr: false,
		},
		{
			name: "empty name",
			input: CreateRestaurantInput{
				Name:    "   ",
				Address: "Москва, ул. Тверская 1",
			},
			wantErr: true,
		},
		{
			name: "name too long",
			input: CreateRestaurantInput{
				Name:    strings.Repeat("a", 256),
				Address: "Москва, ул. Тверская 1",
			},
			wantErr: true,
		},
		{
			name: "invalid webhook schema",
			input: CreateRestaurantInput{
				Name:       "Додо Пицца",
				Address:    "Москва, ул. Тверская 1",
				WebhookURL: "ftp://invalid-webhook.com",
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

func TestCreateMenuItemInput_Validate(t *testing.T) {
	restID := uuid.New()
	catID := uuid.New()

	tests := []struct {
		name    string
		input   CreateMenuItemInput
		wantErr bool
	}{
		{
			name: "valid menu item",
			input: CreateMenuItemInput{
				RestaurantID:  restID,
				CategoryID:    catID,
				Name:          "Маргарита",
				Description:   "Вкусная пицца",
				PriceCents:    45000,
				StockQuantity: 10,
				IsAvailable:   true,
			},
			wantErr: false,
		},
		{
			name: "negative price",
			input: CreateMenuItemInput{
				RestaurantID:  restID,
				CategoryID:    catID,
				Name:          "Маргарита",
				PriceCents:    -100,
				StockQuantity: 10,
			},
			wantErr: true,
		},
		{
			name: "negative stock",
			input: CreateMenuItemInput{
				RestaurantID:  restID,
				CategoryID:    catID,
				Name:          "Маргарита",
				PriceCents:    45000,
				StockQuantity: -1,
			},
			wantErr: true,
		},
		{
			name: "missing restaurant id",
			input: CreateMenuItemInput{
				RestaurantID: uuid.Nil,
				CategoryID:   catID,
				Name:         "Маргарита",
				PriceCents:   45000,
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

// TestValidate_FreeTextIsBounded: before this bound an order comment of 60 000 characters was
// accepted and stored in full. Every free-text field a client can fill is capped at the same
// number, in the same unit the CHECK constraints use, so validation and the schema agree.
func TestValidate_FreeTextIsBounded(t *testing.T) {
	tooLong := strings.Repeat("a", MaxFreeTextLen+1)
	atLimit := strings.Repeat("a", MaxFreeTextLen)

	restID, catID, itemID := uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name  string
		build func(text string) interface{ Validate() error }
	}{
		{"order comment", func(text string) interface{ Validate() error } {
			return &CreateOrderInput{
				UserID: uuid.New(), RestaurantID: restID, DeliveryAddress: "Москва, Арбат, 5",
				Comment: text,
				Items:   []CreateOrderItemInput{{MenuItemID: itemID, Quantity: 1}},
			}
		}},
		{"status change comment", func(text string) interface{ Validate() error } {
			return &UpdateOrderStatusInput{Status: OrderStatusAccepted, Comment: text}
		}},
		{"restaurant description", func(text string) interface{ Validate() error } {
			return &CreateRestaurantInput{Name: "Кафе", Address: "Москва", Description: text}
		}},
		{"restaurant description on update", func(text string) interface{ Validate() error } {
			return &UpdateRestaurantInput{Description: &text}
		}},
		{"dish description", func(text string) interface{ Validate() error } {
			return &CreateMenuItemInput{
				RestaurantID: restID, CategoryID: catID, Name: "Блюдо", PriceCents: 100, Description: text,
			}
		}},
		{"dish description on update", func(text string) interface{ Validate() error } {
			return &UpdateMenuItemInput{Description: &text}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build(tooLong).Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("%s over the limit must be rejected, got %v", tc.name, err)
			}
			if err := tc.build(atLimit).Validate(); err != nil {
				t.Errorf("%s exactly at the limit must be accepted, got %v", tc.name, err)
			}
		})
	}
}

// TestValidate_BoundsMeasureTheStoredValue: every bound is measured on the raw string, because the
// raw string is what the repository binds. Measuring a trimmed copy let a name of 253 characters
// behind three spaces pass validation and then overflow VARCHAR(255) — a client mistake answered
// with 500 instead of 400.
func TestValidate_BoundsMeasureTheStoredValue(t *testing.T) {
	padded := "   " + strings.Repeat("a", MaxNameLen)         // blank-trims to the limit, stores over it
	paddedAddr := strings.Repeat(" ", 3000) + "Москва, Арбат" // trims short, stores far over the limit

	cases := []struct {
		name  string
		input interface{ Validate() error }
	}{
		{"restaurant name", &CreateRestaurantInput{Name: padded, Address: "Москва"}},
		{"restaurant name on update", &UpdateRestaurantInput{Name: &padded}},
		{"restaurant address", &CreateRestaurantInput{Name: "Кафе", Address: paddedAddr}},
		{"restaurant address on update", &UpdateRestaurantInput{Address: &paddedAddr}},
		{"category name", &CreateMenuCategoryInput{RestaurantID: uuid.New(), Name: padded}},
		{"category name on update", &UpdateMenuCategoryInput{Name: &padded}},
		{"dish name", &CreateMenuItemInput{RestaurantID: uuid.New(), CategoryID: uuid.New(), Name: padded}},
		{"dish name on update", &UpdateMenuItemInput{Name: &padded}},
		{"delivery address", &CreateOrderInput{
			UserID: uuid.New(), RestaurantID: uuid.New(), DeliveryAddress: paddedAddr,
			Items: []CreateOrderItemInput{{MenuItemID: uuid.New(), Quantity: 1}},
		}},
	}

	for _, tc := range cases {
		if err := tc.input.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: padding must not smuggle a value past the bound, got %v", tc.name, err)
		}
	}
}

// TestValidate_IntegersFitTheirColumn: sort_order and stock_quantity are int4 in the schema while
// Go's int is 64 bits, so an oversized number used to be refused by the driver and reported as a
// 500. It is a client error and must answer 400.
func TestValidate_IntegersFitTheirColumn(t *testing.T) {
	tooBig := math.MaxInt32 + 1
	tooSmall := math.MinInt32 - 1
	edge := math.MaxInt32

	restID, catID := uuid.New(), uuid.New()

	rejected := []struct {
		name  string
		input interface{ Validate() error }
	}{
		{"category sort_order too big", &CreateMenuCategoryInput{RestaurantID: restID, Name: "Горячее", SortOrder: tooBig}},
		{"category sort_order too small", &CreateMenuCategoryInput{RestaurantID: restID, Name: "Горячее", SortOrder: tooSmall}},
		{"sort_order on update", &UpdateMenuCategoryInput{SortOrder: &tooBig}},
		{"dish stock too big", &CreateMenuItemInput{RestaurantID: restID, CategoryID: catID, Name: "Плов", StockQuantity: tooBig}},
		{"stock on update", &UpdateMenuItemInput{StockQuantity: &tooBig}},
	}
	for _, tc := range rejected {
		if err := tc.input.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: expected ErrInvalidInput, got %v", tc.name, err)
		}
	}

	if err := (&CreateMenuCategoryInput{RestaurantID: restID, Name: "Горячее", SortOrder: edge}).Validate(); err != nil {
		t.Errorf("sort_order at the int4 maximum must be accepted, got %v", err)
	}
	if err := (&CreateMenuItemInput{RestaurantID: restID, CategoryID: catID, Name: "Плов", StockQuantity: edge}).Validate(); err != nil {
		t.Errorf("stock_quantity at the int4 maximum must be accepted, got %v", err)
	}
}
