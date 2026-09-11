package inmem

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

// Package inmem provides in-memory implementations of the domain repositories.
//
// It exists so that the service and handler test suites share one set of doubles instead of
// maintaining three near-identical copies. The doubles deliberately do NOT emulate transactions
// or row locking: a fake that pretends to roll back would give false confidence. Real
// transactional and concurrency behaviour is covered by tests/integration against PostgreSQL.

// TxManager runs the callback directly - see the package comment.
type TxManager struct{}

// WithinTransaction executes fn without any transactional semantics.
func (m *TxManager) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// WebhookRecorder records dispatched events and their targets instead of sending them.
type WebhookRecorder struct {
	mu     sync.Mutex
	Events []string
	URLs   []string
}

// Dispatch records the event and where it was addressed, instead of sending it.
func (m *WebhookRecorder) Dispatch(_ context.Context, url, eventType string, _ any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, eventType)
	m.URLs = append(m.URLs, url)
	return nil
}

// DispatchedURLs returns a copy of the addresses the recorded events were sent to.
func (m *WebhookRecorder) DispatchedURLs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.URLs...)
}

// Dispatched returns a copy of the recorded event types.
func (m *WebhookRecorder) Dispatched() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.Events...)
}

// RestaurantRepository implements domain.RestaurantRepository in memory.
type RestaurantRepository struct {
	mu          sync.Mutex
	restaurants map[uuid.UUID]*domain.Restaurant
}

// NewRestaurantRepository builds an empty restaurant store.
func NewRestaurantRepository() *RestaurantRepository {
	return &RestaurantRepository{restaurants: make(map[uuid.UUID]*domain.Restaurant)}
}

// Put inserts a ready-made restaurant, bypassing business rules.
func (m *RestaurantRepository) Put(rest *domain.Restaurant) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restaurants[rest.ID] = rest
}

// Create stores a new restaurant.
func (m *RestaurantRepository) Create(_ context.Context, r *domain.Restaurant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	r.CreatedAt = time.Now()
	r.UpdatedAt = time.Now()
	stored := *r
	m.restaurants[r.ID] = &stored
	return nil
}

// GetByID returns a restaurant by identifier.
func (m *RestaurantRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.Restaurant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.restaurants[id]
	if !ok {
		return nil, domain.ErrRestaurantNotFound
	}
	return publicView(r), nil
}

// publicView mirrors restaurantColumns in the SQL repository, which selects neither the credential
// nor the partner's callback address. The double has to drop them too, otherwise a test would pass
// against a guarantee only the real database provides.
func publicView(r *domain.Restaurant) *domain.Restaurant {
	cp := *r
	cp.APIKey = ""
	cp.WebhookURL = ""
	return &cp
}

// GetWebhookURL returns the callback address the dispatcher needs, the way the SQL repository does.
func (m *RestaurantRepository) GetWebhookURL(_ context.Context, id uuid.UUID) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.restaurants[id]
	if !ok {
		return "", domain.ErrRestaurantNotFound
	}
	return r.WebhookURL, nil
}

// GetByAPIKey resolves a restaurant by its partner credential.
func (m *RestaurantRepository) GetByAPIKey(_ context.Context, apiKey string) (*domain.Restaurant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.restaurants {
		if r.APIKey != "" && r.APIKey == apiKey {
			return publicView(r), nil
		}
	}
	return nil, domain.ErrRestaurantNotFound
}

// List returns restaurants matching the filter.
func (m *RestaurantRepository) List(_ context.Context, filter domain.RestaurantFilter) ([]domain.Restaurant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]domain.Restaurant, 0, len(m.restaurants))
	for _, r := range m.restaurants {
		if filter.OnlyActive && !r.IsActive {
			continue
		}
		result = append(result, *publicView(r))
	}
	return result, nil
}

// Update applies the given patch.
func (m *RestaurantRepository) Update(_ context.Context, id uuid.UUID, in domain.UpdateRestaurantInput) (*domain.Restaurant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.restaurants[id]
	if !ok {
		return nil, domain.ErrRestaurantNotFound
	}
	if in.Name != nil {
		r.Name = *in.Name
	}
	if in.Description != nil {
		r.Description = *in.Description
	}
	if in.Address != nil {
		r.Address = *in.Address
	}
	if in.IsActive != nil {
		r.IsActive = *in.IsActive
	}
	if in.WebhookURL != nil {
		r.WebhookURL = *in.WebhookURL
	}
	return publicView(r), nil
}

// MenuRepository implements domain.MenuRepository in memory.
type MenuRepository struct {
	mu         sync.Mutex
	categories map[uuid.UUID]*domain.MenuCategory
	items      map[uuid.UUID]*domain.MenuItem
}

// NewMenuRepository builds an empty menu store.
func NewMenuRepository() *MenuRepository {
	return &MenuRepository{
		categories: make(map[uuid.UUID]*domain.MenuCategory),
		items:      make(map[uuid.UUID]*domain.MenuItem),
	}
}

// PutCategory inserts a ready-made category.
func (m *MenuRepository) PutCategory(cat *domain.MenuCategory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.categories[cat.ID] = cat
}

// PutItem inserts a ready-made dish.
func (m *MenuRepository) PutItem(item *domain.MenuItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[item.ID] = item
}

// StockOf reports the current stock of a dish.
func (m *MenuRepository) StockOf(id uuid.UUID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[id]; ok {
		return it.StockQuantity
	}
	return -1
}

// CreateCategory stores a category.
func (m *MenuRepository) CreateCategory(_ context.Context, cat *domain.MenuCategory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cat.ID == uuid.Nil {
		cat.ID = uuid.New()
	}
	stored := *cat
	m.categories[cat.ID] = &stored
	return nil
}

// GetCategoryByID returns a category.
func (m *MenuRepository) GetCategoryByID(_ context.Context, id uuid.UUID) (*domain.MenuCategory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cat, ok := m.categories[id]
	if !ok {
		return nil, domain.ErrCategoryNotFound
	}
	cp := *cat
	return &cp, nil
}

// ListCategoriesByRestaurant returns the categories of a restaurant.
func (m *MenuRepository) ListCategoriesByRestaurant(_ context.Context, restaurantID uuid.UUID) ([]domain.MenuCategory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]domain.MenuCategory, 0, len(m.categories))
	for _, c := range m.categories {
		if c.RestaurantID == restaurantID {
			res = append(res, *c)
		}
	}
	return res, nil
}

// UpdateCategory applies a patch to a category.
func (m *MenuRepository) UpdateCategory(_ context.Context, id uuid.UUID, in domain.UpdateMenuCategoryInput) (*domain.MenuCategory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cat, ok := m.categories[id]
	if !ok {
		return nil, domain.ErrCategoryNotFound
	}
	if in.Name != nil {
		cat.Name = *in.Name
	}
	cp := *cat
	return &cp, nil
}

// DeleteCategory removes a category.
func (m *MenuRepository) DeleteCategory(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.categories[id]; !ok {
		return domain.ErrCategoryNotFound
	}
	delete(m.categories, id)
	return nil
}

// CreateItem stores a dish.
func (m *MenuRepository) CreateItem(_ context.Context, item *domain.MenuItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	stored := *item
	m.items[item.ID] = &stored
	return nil
}

// GetItemByID returns a dish.
func (m *MenuRepository) GetItemByID(_ context.Context, id uuid.UUID) (*domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return nil, domain.ErrMenuItemNotFound
	}
	cp := *it
	return &cp, nil
}

// GetItemsByIDsForUpdate returns the requested dishes.
func (m *MenuRepository) GetItemsByIDsForUpdate(_ context.Context, ids []uuid.UUID) ([]domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]domain.MenuItem, 0, len(ids))
	for _, id := range ids {
		if it, ok := m.items[id]; ok {
			res = append(res, *it)
		}
	}
	return res, nil
}

// ListItemsByRestaurant returns the dishes of a restaurant.
func (m *MenuRepository) ListItemsByRestaurant(_ context.Context, restaurantID uuid.UUID, onlyAvailable bool) ([]domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]domain.MenuItem, 0, len(m.items))
	for _, it := range m.items {
		if it.RestaurantID != restaurantID {
			continue
		}
		if onlyAvailable && !it.IsAvailable {
			continue
		}
		res = append(res, *it)
	}
	return res, nil
}

// SearchItems matches dishes by name or description, case-insensitively.
//
// Unlike the restaurant double this one really filters, so a handler test can prove the product
// claim - that searching for a dish finds the dish - without a database. What it deliberately does
// NOT model is the SQL join: RestaurantName stays empty and the establishment's is_active flag is
// not consulted, because the double holds no restaurants. Both are covered by the integration test
// against real PostgreSQL.
func (m *MenuRepository) SearchItems(_ context.Context, filter domain.MenuItemSearchFilter) ([]domain.MenuItemSearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	needle := strings.ToLower(filter.Search)
	res := make([]domain.MenuItemSearchResult, 0)
	for _, it := range m.items {
		if !it.IsAvailable {
			continue
		}
		if !strings.Contains(strings.ToLower(it.Name), needle) &&
			!strings.Contains(strings.ToLower(it.Description), needle) {
			continue
		}
		res = append(res, domain.MenuItemSearchResult{MenuItem: *it})
	}

	sort.Slice(res, func(i, j int) bool {
		if res[i].Name != res[j].Name {
			return res[i].Name < res[j].Name
		}
		return res[i].ID.String() < res[j].ID.String()
	})

	if filter.Offset >= len(res) {
		return res[:0], nil
	}
	res = res[filter.Offset:]
	if filter.Limit > 0 && filter.Limit < len(res) {
		res = res[:filter.Limit]
	}
	return res, nil
}

// UpdateItem applies a patch to a dish.
func (m *MenuRepository) UpdateItem(_ context.Context, id uuid.UUID, in domain.UpdateMenuItemInput) (*domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return nil, domain.ErrMenuItemNotFound
	}
	if in.Name != nil {
		it.Name = *in.Name
	}
	if in.PriceCents != nil {
		it.PriceCents = *in.PriceCents
	}
	if in.StockQuantity != nil {
		it.StockQuantity = *in.StockQuantity
	}
	if in.IsAvailable != nil {
		it.IsAvailable = *in.IsAvailable
	}
	cp := *it
	return &cp, nil
}

// DeleteItem removes a dish.
func (m *MenuRepository) DeleteItem(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; !ok {
		return domain.ErrMenuItemNotFound
	}
	delete(m.items, id)
	return nil
}

// DecrementStock reduces stock, refusing to go negative.
func (m *MenuRepository) DecrementStock(_ context.Context, itemID uuid.UUID, quantity int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[itemID]
	if !ok {
		return domain.ErrMenuItemNotFound
	}
	if !it.IsAvailable || it.StockQuantity < quantity {
		return domain.ErrOutOfStock
	}
	it.StockQuantity -= quantity
	return nil
}

// IncrementStock returns stock to inventory.
func (m *MenuRepository) IncrementStock(_ context.Context, itemID uuid.UUID, quantity int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[itemID]
	if !ok {
		return domain.ErrMenuItemNotFound
	}
	it.StockQuantity += quantity
	return nil
}

// OrderRepository implements domain.OrderRepository in memory.
type OrderRepository struct {
	mu      sync.Mutex
	orders  map[uuid.UUID]*domain.Order
	history map[uuid.UUID][]domain.OrderStatusHistory
}

// NewOrderRepository builds an empty order store.
func NewOrderRepository() *OrderRepository {
	return &OrderRepository{
		orders:  make(map[uuid.UUID]*domain.Order),
		history: make(map[uuid.UUID][]domain.OrderStatusHistory),
	}
}

// CreateOrder stores an order plus its initial audit entry.
func (m *OrderRepository) CreateOrder(_ context.Context, o *domain.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID == uuid.Nil {
		o.ID = uuid.New()
	}
	o.CreatedAt = time.Now()
	o.UpdatedAt = time.Now()
	for i := range o.Items {
		o.Items[i].OrderID = o.ID
		if o.Items[i].ID == uuid.Nil {
			o.Items[i].ID = uuid.New()
		}
	}
	m.history[o.ID] = append(m.history[o.ID], domain.OrderStatusHistory{
		OrderID: o.ID, ToStatus: o.Status, Actor: domain.ActorSystem, CreatedAt: time.Now(),
	})
	stored := *o
	m.orders[o.ID] = &stored
	return nil
}

// GetOrderByID returns an order with its audit trail.
func (m *OrderRepository) GetOrderByID(_ context.Context, id uuid.UUID) (*domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *o
	cp.StatusHistory = append([]domain.OrderStatusHistory(nil), m.history[id]...)
	return &cp, nil
}

// GetOrderForUpdate mirrors GetOrderByID; row locking is only meaningful against a real database.
func (m *OrderRepository) GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	return m.GetOrderByID(ctx, id)
}

// ListOrders returns orders matching the filter.
func (m *OrderRepository) ListOrders(_ context.Context, filter domain.OrderFilter) ([]domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]domain.Order, 0, len(m.orders))
	for _, o := range m.orders {
		if filter.UserID != nil && o.UserID != *filter.UserID {
			continue
		}
		if filter.RestaurantID != nil && o.RestaurantID != *filter.RestaurantID {
			continue
		}
		if filter.Status != nil && o.Status != *filter.Status {
			continue
		}
		res = append(res, *o)
	}
	return res, nil
}

// UpdateOrderStatus writes the new status.
func (m *OrderRepository) UpdateOrderStatus(_ context.Context, id uuid.UUID, newStatus domain.OrderStatus, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return domain.ErrNotFound
	}
	o.Status = newStatus
	o.UpdatedAt = time.Now()
	return nil
}

// AddStatusHistory appends an audit entry.
func (m *OrderRepository) AddStatusHistory(_ context.Context, h *domain.OrderStatusHistory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h.CreatedAt = time.Now()
	m.history[h.OrderID] = append(m.history[h.OrderID], *h)
	return nil
}
