package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Restaurant represents a food establishment partner in Go.Kitchen.
type Restaurant struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Address     string    `json:"address"`
	IsActive    bool      `json:"is_active"`
	WebhookURL  string    `json:"webhook_url,omitempty"`
	APIKey      string    `json:"api_key,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateRestaurantInput holds data required to register a restaurant.
type CreateRestaurantInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Address     string `json:"address"`
	WebhookURL  string `json:"webhook_url"`
}

// Validate checks business rules for creating a restaurant.
func (in *CreateRestaurantInput) Validate() error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > MaxNameLen {
		return ErrInvalidInput
	}
	if strings.TrimSpace(in.Address) == "" || len(in.Address) > MaxAddressLen {
		return ErrInvalidInput
	}
	if len(in.Description) > MaxFreeTextLen {
		return ErrInvalidInput
	}
	return validateWebhookURL(in.WebhookURL)
}

// UpdateRestaurantInput holds optional fields to update a restaurant.
type UpdateRestaurantInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Address     *string `json:"address,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
	WebhookURL  *string `json:"webhook_url,omitempty"`
}

// Validate checks business rules for updating a restaurant.
func (in *UpdateRestaurantInput) Validate() error {
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > MaxNameLen {
			return ErrInvalidInput
		}
	}
	if in.Address != nil {
		if strings.TrimSpace(*in.Address) == "" || len(*in.Address) > MaxAddressLen {
			return ErrInvalidInput
		}
	}
	if in.Description != nil && len(*in.Description) > MaxFreeTextLen {
		return ErrInvalidInput
	}
	if in.WebhookURL != nil {
		if err := validateWebhookURL(*in.WebhookURL); err != nil {
			return err
		}
	}
	return nil
}

// RestaurantFilter holds query filtering parameters for restaurants list.
type RestaurantFilter struct {
	OnlyActive bool
	Search     string
	Limit      int
	Offset     int
}
