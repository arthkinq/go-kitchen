package domain

import (
	"context"

	"github.com/google/uuid"
)

// TxManager defines the interface for managing atomic database transactions.
type TxManager interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// RestaurantRepository defines persistence methods for restaurants.
type RestaurantRepository interface {
	Create(ctx context.Context, r *Restaurant) error
	GetByID(ctx context.Context, id uuid.UUID) (*Restaurant, error)
	GetByAPIKey(ctx context.Context, apiKey string) (*Restaurant, error)
	GetWebhookURL(ctx context.Context, id uuid.UUID) (string, error)
	List(ctx context.Context, filter RestaurantFilter) ([]Restaurant, error)
	Update(ctx context.Context, id uuid.UUID, in UpdateRestaurantInput) (*Restaurant, error)
}

// MenuRepository defines persistence methods for categories and dishes.
type MenuRepository interface {
	CreateCategory(ctx context.Context, cat *MenuCategory) error
	GetCategoryByID(ctx context.Context, id uuid.UUID) (*MenuCategory, error)
	ListCategoriesByRestaurant(ctx context.Context, restaurantID uuid.UUID) ([]MenuCategory, error)
	UpdateCategory(ctx context.Context, id uuid.UUID, in UpdateMenuCategoryInput) (*MenuCategory, error)
	DeleteCategory(ctx context.Context, id uuid.UUID) error

	CreateItem(ctx context.Context, item *MenuItem) error
	GetItemByID(ctx context.Context, id uuid.UUID) (*MenuItem, error)
	GetItemsByIDsForUpdate(ctx context.Context, ids []uuid.UUID) ([]MenuItem, error)
	ListItemsByRestaurant(ctx context.Context, restaurantID uuid.UUID, onlyAvailable bool) ([]MenuItem, error)
	SearchItems(ctx context.Context, filter MenuItemSearchFilter) ([]MenuItemSearchResult, error)
	UpdateItem(ctx context.Context, id uuid.UUID, in UpdateMenuItemInput) (*MenuItem, error)
	DeleteItem(ctx context.Context, id uuid.UUID) error
	DecrementStock(ctx context.Context, itemID uuid.UUID, quantity int) error
	IncrementStock(ctx context.Context, itemID uuid.UUID, quantity int) error
}

// OrderRepository defines persistence methods for orders and status transitions.
type OrderRepository interface {
	CreateOrder(ctx context.Context, o *Order) error
	GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, error)
	GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*Order, error)
	ListOrders(ctx context.Context, filter OrderFilter) ([]Order, error)
	UpdateOrderStatus(ctx context.Context, id uuid.UUID, newStatus OrderStatus, comment string) error
	AddStatusHistory(ctx context.Context, h *OrderStatusHistory) error
}
