package http

import (
	"crypto/subtle"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/service"
)

// PartnerHandler handles restaurant/partner-facing HTTP requests.
// Every handler below (except registration) runs behind PartnerAuth and acts strictly
// on behalf of the establishment resolved from the API key.
type PartnerHandler struct {
	restService    *service.RestaurantService
	menuService    *service.MenuService
	orderService   *service.OrderService
	bootstrapToken string
}

// bootstrapHeader carries the token that authorises putting a new establishment on the platform.
// It is deliberately not the Authorization header: that one already means "I am this establishment",
// and one header with two meanings is how access-control mistakes happen.
const bootstrapHeader = "X-Bootstrap-Token"

// NewPartnerHandler constructs a new PartnerHandler.
func NewPartnerHandler(
	restService *service.RestaurantService,
	menuService *service.MenuService,
	orderService *service.OrderService,
	bootstrapToken string,
) *PartnerHandler {
	return &PartnerHandler{
		restService:    restService,
		menuService:    menuService,
		orderService:   orderService,
		bootstrapToken: bootstrapToken,
	}
}

// CreateRestaurant handles POST /api/v1/partner/restaurants
func (h *PartnerHandler) CreateRestaurant(w http.ResponseWriter, r *http.Request) {
	if h.bootstrapToken == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get(bootstrapHeader)), []byte(h.bootstrapToken)) != 1 {
		respondError(w, domain.ErrUnauthorized)
		return
	}

	var in domain.CreateRestaurantInput
	if err := decodeJSON(w, r, &in); err != nil {
		respondError(w, err)
		return
	}

	rest, err := h.restService.Create(r.Context(), in)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, Response{Data: rest})
}

// UpdateRestaurant handles PATCH /api/v1/partner/restaurants/{restaurant_id}
func (h *PartnerHandler) UpdateRestaurant(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "restaurant_id")
	if err != nil {
		respondError(w, err)
		return
	}

	var in domain.UpdateRestaurantInput
	if err := decodeJSON(w, r, &in); err != nil {
		respondError(w, err)
		return
	}
	if err := in.Validate(); err != nil {
		respondError(w, err)
		return
	}

	rest, err := h.restService.Update(r.Context(), partnerID(r.Context()), id, in)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: rest})
}

// CreateCategoryRequest holds input for adding a category to the authenticated restaurant.
type CreateCategoryRequest struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

// CreateCategory handles POST /api/v1/partner/restaurants/{restaurant_id}/categories
func (h *PartnerHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	restID, err := h.ownRestaurantParam(r)
	if err != nil {
		respondError(w, err)
		return
	}

	var req CreateCategoryRequest
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, err)
		return
	}

	cat, err := h.menuService.CreateCategory(r.Context(), domain.CreateMenuCategoryInput{
		RestaurantID: restID,
		Name:         req.Name,
		SortOrder:    req.SortOrder,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, Response{Data: cat})
}

// UpdateCategory handles PATCH /api/v1/partner/categories/{category_id}
func (h *PartnerHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "category_id")
	if err != nil {
		respondError(w, err)
		return
	}

	var in domain.UpdateMenuCategoryInput
	if err := decodeJSON(w, r, &in); err != nil {
		respondError(w, err)
		return
	}
	if err := in.Validate(); err != nil {
		respondError(w, err)
		return
	}

	cat, err := h.menuService.UpdateCategory(r.Context(), partnerID(r.Context()), id, in)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: cat})
}

// DeleteCategory handles DELETE /api/v1/partner/categories/{category_id}
func (h *PartnerHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "category_id")
	if err != nil {
		respondError(w, err)
		return
	}

	if err := h.menuService.DeleteCategory(r.Context(), partnerID(r.Context()), id); err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusNoContent, nil)
}

// CreateItemRequest holds the payload for dish creation.
// IsAvailable is a pointer so that an omitted field means "available", matching the column
// default - a dish must not silently disappear from the menu because the flag was left out.
type CreateItemRequest struct {
	CategoryID    uuid.UUID `json:"category_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	PriceCents    int64     `json:"price_cents"`
	StockQuantity int       `json:"stock_quantity"`
	IsAvailable   *bool     `json:"is_available"`
}

// CreateItem handles POST /api/v1/partner/restaurants/{restaurant_id}/items
func (h *PartnerHandler) CreateItem(w http.ResponseWriter, r *http.Request) {
	restID, err := h.ownRestaurantParam(r)
	if err != nil {
		respondError(w, err)
		return
	}

	var req CreateItemRequest
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, err)
		return
	}

	isAvailable := true
	if req.IsAvailable != nil {
		isAvailable = *req.IsAvailable
	}

	item, err := h.menuService.CreateItem(r.Context(), domain.CreateMenuItemInput{
		RestaurantID:  restID,
		CategoryID:    req.CategoryID,
		Name:          req.Name,
		Description:   req.Description,
		PriceCents:    req.PriceCents,
		StockQuantity: req.StockQuantity,
		IsAvailable:   isAvailable,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, Response{Data: item})
}

// UpdateItem handles PATCH /api/v1/partner/items/{item_id}
func (h *PartnerHandler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "item_id")
	if err != nil {
		respondError(w, err)
		return
	}

	var in domain.UpdateMenuItemInput
	if err := decodeJSON(w, r, &in); err != nil {
		respondError(w, err)
		return
	}
	if err := in.Validate(); err != nil {
		respondError(w, err)
		return
	}

	item, err := h.menuService.UpdateItem(r.Context(), partnerID(r.Context()), id, in)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: item})
}

// DeleteItem handles DELETE /api/v1/partner/items/{item_id}
func (h *PartnerHandler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "item_id")
	if err != nil {
		respondError(w, err)
		return
	}

	if err := h.menuService.DeleteItem(r.Context(), partnerID(r.Context()), id); err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusNoContent, nil)
}

// ListOrders handles GET /api/v1/partner/restaurants/{restaurant_id}/orders
func (h *PartnerHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	restID, err := h.ownRestaurantParam(r)
	if err != nil {
		respondError(w, err)
		return
	}

	page, err := parsePagination(r)
	if err != nil {
		respondError(w, err)
		return
	}

	statusPtr, err := parseStatusFilter(r)
	if err != nil {
		respondError(w, err)
		return
	}

	orders, err := h.orderService.ListOrders(r.Context(), domain.OrderFilter{
		RestaurantID: &restID,
		Status:       statusPtr,
		Limit:        page.Limit,
		Offset:       page.Offset,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: orders})
}

// UpdateOrderStatus handles PATCH /api/v1/partner/orders/{order_id}/status
func (h *PartnerHandler) UpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "order_id")
	if err != nil {
		respondError(w, err)
		return
	}

	var in domain.UpdateOrderStatusInput
	if err := decodeJSON(w, r, &in); err != nil {
		respondError(w, err)
		return
	}

	order, err := h.orderService.UpdateOrderStatus(r.Context(), partnerID(r.Context()), id, in)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: order})
}

// ownRestaurantParam reads {restaurant_id} from the path and refuses paths addressing
// an establishment other than the authenticated one.
func (h *PartnerHandler) ownRestaurantParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuidParam(r, "restaurant_id")
	if err != nil {
		return uuid.Nil, err
	}
	if id != partnerID(r.Context()) {
		return uuid.Nil, domain.ErrRestaurantNotFound
	}
	return id, nil
}

// parseStatusFilter reads an optional ?status= filter, rejecting unknown values instead of
// silently returning the unfiltered list.
func parseStatusFilter(r *http.Request) (*domain.OrderStatus, error) {
	raw := r.URL.Query().Get("status")
	if raw == "" {
		return nil, nil
	}
	status := domain.OrderStatus(raw)
	if !status.IsValid() {
		return nil, domain.ErrInvalidInput
	}
	return &status, nil
}

// uuidParam parses a UUID path parameter.
func uuidParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, domain.ErrInvalidInput
	}
	return id, nil
}
