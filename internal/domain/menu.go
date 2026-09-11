package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// MenuCategory represents a logical grouping of dishes inside a restaurant's menu.
type MenuCategory struct {
	ID           uuid.UUID  `json:"id"`
	RestaurantID uuid.UUID  `json:"restaurant_id"`
	Name         string     `json:"name"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Items        []MenuItem `json:"items,omitempty"`
}

// MenuItem represents a specific dish or product offered by a restaurant.
type MenuItem struct {
	ID            uuid.UUID `json:"id"`
	RestaurantID  uuid.UUID `json:"restaurant_id"`
	CategoryID    uuid.UUID `json:"category_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	PriceCents    int64     `json:"price_cents"`
	StockQuantity int       `json:"stock_quantity"`
	IsAvailable   bool      `json:"is_available"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// MenuItemSearchResult is a dish found by search, together with the establishment selling it.
//
// MenuItem is embedded, so the JSON is the dish shape the client already knows plus one field.
// That is all it takes to answer "where can I order this": restaurant_id is already part of
// MenuItem, and the name is what a result list has to render.
type MenuItemSearchResult struct {
	MenuItem
	RestaurantName string `json:"restaurant_name"`
}

// MenuItemSearchFilter holds the parameters of a dish search across the whole platform.
type MenuItemSearchFilter struct {
	Search string
	Limit  int
	Offset int
}

// CreateMenuCategoryInput holds parameters to create a new category.
type CreateMenuCategoryInput struct {
	RestaurantID uuid.UUID `json:"restaurant_id"`
	Name         string    `json:"name"`
	SortOrder    int       `json:"sort_order"`
}

// Validate checks business rules for creating a menu category.
func (in *CreateMenuCategoryInput) Validate() error {
	if in.RestaurantID == uuid.Nil {
		return ErrInvalidInput
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > MaxNameLen {
		return ErrInvalidInput
	}
	if !fitsInt32(in.SortOrder) {
		return ErrInvalidInput
	}
	return nil
}

// UpdateMenuCategoryInput holds parameters to update an existing menu category.
type UpdateMenuCategoryInput struct {
	Name      *string `json:"name,omitempty"`
	SortOrder *int    `json:"sort_order,omitempty"`
}

// Validate checks business rules for updating a category.
func (in *UpdateMenuCategoryInput) Validate() error {
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > MaxNameLen {
			return ErrInvalidInput
		}
	}
	if in.SortOrder != nil && !fitsInt32(*in.SortOrder) {
		return ErrInvalidInput
	}
	return nil
}

// CreateMenuItemInput holds parameters to create a new dish in the menu.
type CreateMenuItemInput struct {
	RestaurantID  uuid.UUID `json:"restaurant_id"`
	CategoryID    uuid.UUID `json:"category_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	PriceCents    int64     `json:"price_cents"`
	StockQuantity int       `json:"stock_quantity"`
	IsAvailable   bool      `json:"is_available"`
}

// Validate checks business rules for creating a menu item.
func (in *CreateMenuItemInput) Validate() error {
	if in.RestaurantID == uuid.Nil || in.CategoryID == uuid.Nil {
		return ErrInvalidInput
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > MaxNameLen {
		return ErrInvalidInput
	}
	if len(in.Description) > MaxFreeTextLen {
		return ErrInvalidInput
	}
	if in.PriceCents < 0 {
		return ErrInvalidInput
	}
	if in.StockQuantity < 0 || !fitsInt32(in.StockQuantity) {
		return ErrInvalidInput
	}
	return nil
}

// UpdateMenuItemInput holds optional parameters to update an existing menu item.
type UpdateMenuItemInput struct {
	CategoryID    *uuid.UUID `json:"category_id,omitempty"`
	Name          *string    `json:"name,omitempty"`
	Description   *string    `json:"description,omitempty"`
	PriceCents    *int64     `json:"price_cents,omitempty"`
	StockQuantity *int       `json:"stock_quantity,omitempty"`
	IsAvailable   *bool      `json:"is_available,omitempty"`
}

// Validate checks business rules for updating a menu item.
func (in *UpdateMenuItemInput) Validate() error {
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > MaxNameLen {
			return ErrInvalidInput
		}
	}
	if in.Description != nil && len(*in.Description) > MaxFreeTextLen {
		return ErrInvalidInput
	}
	if in.PriceCents != nil && *in.PriceCents < 0 {
		return ErrInvalidInput
	}
	if in.StockQuantity != nil && (*in.StockQuantity < 0 || !fitsInt32(*in.StockQuantity)) {
		return ErrInvalidInput
	}
	if in.CategoryID != nil && *in.CategoryID == uuid.Nil {
		return ErrInvalidInput
	}
	return nil
}
