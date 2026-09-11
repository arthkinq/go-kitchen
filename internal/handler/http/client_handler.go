package http

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/service"
)

// Pagination and search bounds for list endpoints.
const (
	defaultPageLimit = 20
	maxPageLimit     = 100
	maxSearchLen     = 100
	minSearchLen     = 3
)

// ClientHandler handles customer-facing HTTP requests.
type ClientHandler struct {
	restService  *service.RestaurantService
	menuService  *service.MenuService
	orderService *service.OrderService
}

// NewClientHandler constructs a new ClientHandler.
func NewClientHandler(
	restService *service.RestaurantService,
	menuService *service.MenuService,
	orderService *service.OrderService,
) *ClientHandler {
	return &ClientHandler{
		restService:  restService,
		menuService:  menuService,
		orderService: orderService,
	}
}

// pagination holds validated list-window parameters.
type pagination struct {
	Limit  int
	Offset int
}

// parsePagination validates ?limit and ?offset. Values outside the allowed window are rejected
// rather than silently replaced, so a client never receives a page it did not ask for.
func parsePagination(r *http.Request) (pagination, error) {
	page := pagination{Limit: defaultPageLimit}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxPageLimit {
			return pagination{}, domain.ErrInvalidInput
		}
		page.Limit = limit
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return pagination{}, domain.ErrInvalidInput
		}
		page.Offset = offset
	}

	return page, nil
}

// parseSearch reads the optional ?search= parameter and enforces the bounds the trigram index
// can actually serve. An empty string means "no search filter".
func parseSearch(r *http.Request) (string, error) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if search == "" {
		return "", nil
	}

	if !isPostgresText(search) {
		return "", domain.ErrInvalidInput
	}

	if n := utf8.RuneCountInString(search); n < minSearchLen || n > maxSearchLen {
		return "", domain.ErrInvalidInput
	}

	return search, nil
}

// ListRestaurants handles GET /api/v1/restaurants
func (h *ClientHandler) ListRestaurants(w http.ResponseWriter, r *http.Request) {
	page, err := parsePagination(r)
	if err != nil {
		respondError(w, err)
		return
	}

	search, err := parseSearch(r)
	if err != nil {
		respondError(w, err)
		return
	}

	restaurants, err := h.restService.List(r.Context(), domain.RestaurantFilter{
		OnlyActive: true,
		Search:     search,
		Limit:      page.Limit,
		Offset:     page.Offset,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: restaurants})
}

// SearchItems handles GET /api/v1/items?search=...
//
// The assignment asks how a person finds a product, and a catalogue search answers only half of
// that: "шаурма" finds an establishment with the word in its name, but not the shawarma sitting on
// someone else's menu. This is the other half.
//
// Unlike the catalogue, search is mandatory here. A catalogue without a filter is a legitimate
// browse view served by an index; a global list of every dish on the platform is not, and it is the
// very sequential scan minSearchLen exists to prevent.
func (h *ClientHandler) SearchItems(w http.ResponseWriter, r *http.Request) {
	search, err := parseSearch(r)
	if err != nil {
		respondError(w, err)
		return
	}
	if search == "" {
		respondError(w, domain.ErrInvalidInput)
		return
	}

	page, err := parsePagination(r)
	if err != nil {
		respondError(w, err)
		return
	}

	items, err := h.menuService.SearchItems(r.Context(), domain.MenuItemSearchFilter{
		Search: search,
		Limit:  page.Limit,
		Offset: page.Offset,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: items})
}

// ListOrders handles GET /api/v1/orders?user_id=...
//
// The assignment leaves user authentication out of scope, so the caller states who it is and the
// endpoint takes that at face value - the same footing the order card and the cancel endpoint
// already stand on. user_id is required for exactly that reason: without it the endpoint would
// hand out every order in the system instead of one person's history.
func (h *ClientHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(r.URL.Query().Get("user_id"))
	if err != nil {
		respondError(w, domain.ErrInvalidInput)
		return
	}

	page, err := parsePagination(r)
	if err != nil {
		respondError(w, err)
		return
	}

	status, err := parseStatusFilter(r)
	if err != nil {
		respondError(w, err)
		return
	}

	orders, err := h.orderService.ListOrders(r.Context(), domain.OrderFilter{
		UserID: &userID,
		Status: status,
		Limit:  page.Limit,
		Offset: page.Offset,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: orders})
}

// GetRestaurant handles GET /api/v1/restaurants/{restaurant_id}
func (h *ClientHandler) GetRestaurant(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "restaurant_id")
	if err != nil {
		respondError(w, err)
		return
	}

	rest, err := h.restService.GetByID(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: rest})
}

// GetMenu handles GET /api/v1/restaurants/{restaurant_id}/menu
func (h *ClientHandler) GetMenu(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "restaurant_id")
	if err != nil {
		respondError(w, err)
		return
	}

	menu, err := h.menuService.GetFullMenu(r.Context(), id, true)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: menu})
}

// CreateOrder handles POST /api/v1/orders
func (h *ClientHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var in domain.CreateOrderInput
	if err := decodeJSON(w, r, &in); err != nil {
		respondError(w, err)
		return
	}

	order, err := h.orderService.CreateOrder(r.Context(), in)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, Response{Data: order})
}

// GetOrder handles GET /api/v1/orders/{order_id}?user_id=...
//
// user_id is required for the same reason it is required on the history endpoint: without user
// authentication the caller states who it is, and an order card carries an address and a receipt
// that belong to that person.
func (h *ClientHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	id, err := uuidParam(r, "order_id")
	if err != nil {
		respondError(w, err)
		return
	}

	userID, err := uuid.Parse(r.URL.Query().Get("user_id"))
	if err != nil {
		respondError(w, domain.ErrInvalidInput)
		return
	}

	order, err := h.orderService.GetOrderByID(r.Context(), id, userID)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: order})
}

// CancelOrderRequest holds input for customer order cancellation.
type CancelOrderRequest struct {
	UserID uuid.UUID `json:"user_id"`
	Reason string    `json:"reason"`
}

// CancelOrder handles POST /api/v1/orders/{order_id}/cancel
func (h *ClientHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := uuidParam(r, "order_id")
	if err != nil {
		respondError(w, err)
		return
	}

	var req CancelOrderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, err)
		return
	}

	order, err := h.orderService.CancelOrderByCustomer(r.Context(), orderID, req.UserID, req.Reason)
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, Response{Data: order})
}
