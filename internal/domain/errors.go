package domain

import "errors"

var (
	// ErrNotFound is returned when requested entity is not found.
	ErrNotFound = errors.New("entity not found")

	// ErrRestaurantNotFound is returned when restaurant is not found.
	ErrRestaurantNotFound = errors.New("restaurant not found")

	// ErrRestaurantInactive is returned when operating on an inactive restaurant.
	ErrRestaurantInactive = errors.New("restaurant is inactive")

	// ErrCategoryNotFound is returned when menu category is not found.
	ErrCategoryNotFound = errors.New("menu category not found")

	// ErrMenuItemNotFound is returned when menu item is not found.
	ErrMenuItemNotFound = errors.New("menu item not found")

	// ErrItemUnavailable is returned when menu item is marked as not available.
	ErrItemUnavailable = errors.New("menu item is currently unavailable")

	// ErrOutOfStock is returned when requested quantity exceeds available stock.
	ErrOutOfStock = errors.New("menu item is out of stock")

	// ErrRestaurantMismatch is returned when order items belong to different restaurants.
	ErrRestaurantMismatch = errors.New("all order items must belong to the specified restaurant")

	// ErrEmptyOrder is returned when order contains no items.
	ErrEmptyOrder = errors.New("order must contain at least one item")

	// ErrInvalidStatusTransition is returned when an order transition is illegal.
	ErrInvalidStatusTransition = errors.New("invalid order status transition")

	// ErrOrderAlreadyProcessed is returned when trying to cancel an order that is already cooking or completed.
	ErrOrderAlreadyProcessed = errors.New("order cannot be cancelled in its current status")

	// ErrInvalidInput is returned when input validation fails.
	ErrInvalidInput = errors.New("invalid input data")

	// ErrConflict is returned when unique constraint or version check fails.
	ErrConflict = errors.New("resource conflict")

	// ErrUnauthorized is returned when a partner request carries no valid API key.
	ErrUnauthorized = errors.New("missing or invalid partner credentials")
)
