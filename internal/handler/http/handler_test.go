package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/repository/inmem"
	"github.com/arthkinq/go-kitchen/internal/service"
)

const (
	testAPIKey         = "test-partner-api-key"
	testBootstrapToken = "test-bootstrap-token"
)

type apiFixture struct {
	router   http.Handler
	restRepo *inmem.RestaurantRepository
	menuRepo *inmem.MenuRepository
	restID   uuid.UUID
}

// newAPIFixture wires the full router over in-memory repositories and registers one
// establishment holding testAPIKey.
func newAPIFixture() *apiFixture {
	restRepo := inmem.NewRestaurantRepository()
	menuRepo := inmem.NewMenuRepository()
	orderRepo := inmem.NewOrderRepository()

	restService := service.NewRestaurantService(restRepo)
	menuService := service.NewMenuService(menuRepo, restRepo)
	orderService := service.NewOrderService(orderRepo, menuRepo, restRepo, &inmem.TxManager{}, &inmem.WebhookRecorder{})

	restID := uuid.New()
	restRepo.Put(&domain.Restaurant{
		ID:         restID,
		Name:       "Go Pizza",
		Address:    "ul. Lenina, 1",
		IsActive:   true,
		APIKey:     testAPIKey,
		WebhookURL: "https://go-pizza.example.com/webhook",
	})

	router := NewRouter(
		NewClientHandler(restService, menuService, orderService),
		NewPartnerHandler(restService, menuService, orderService, testBootstrapToken),
		PartnerAuth(restService),
	)

	return &apiFixture{router: router, restRepo: restRepo, menuRepo: menuRepo, restID: restID}
}

// do performs a request; a non-empty apiKey is sent as a partner bearer token.
func (f *apiFixture) do(t *testing.T, method, path, apiKey string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// register puts a new establishment on the platform. It is a separate helper because registration
// is gated by the bootstrap token rather than by a partner key.
func (f *apiFixture) register(t *testing.T, body any) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/partner/restaurants", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(bootstrapHeader, testBootstrapToken)

	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func (f *apiFixture) addDish(name string, priceCents int64, stock int) uuid.UUID {
	catID := uuid.New()
	f.menuRepo.PutCategory(&domain.MenuCategory{ID: catID, RestaurantID: f.restID, Name: "Kategoriia"})

	itemID := uuid.New()
	f.menuRepo.PutItem(&domain.MenuItem{
		ID: itemID, RestaurantID: f.restID, CategoryID: catID,
		Name: name, PriceCents: priceCents, StockQuantity: stock, IsAvailable: true,
	})
	return itemID
}

// decodeData unwraps the {"data": ...} envelope into dst.
func decodeData(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v; body: %s", err, rr.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		t.Fatalf("decode data: %v; body: %s", err, rr.Body.String())
	}
}

func assertStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("expected %d, got %d; body: %s", want, rr.Code, rr.Body.String())
	}
}

func TestHealthCheck(t *testing.T) {
	f := newAPIFixture()
	assertStatus(t, f.do(t, http.MethodGet, "/healthz", "", nil), http.StatusOK)
}

func TestClientAPI_ListRestaurants_ReturnsTheCatalogue(t *testing.T) {
	f := newAPIFixture()
	f.restRepo.Put(&domain.Restaurant{ID: uuid.New(), Name: "Zakrytoe kafe", IsActive: false})

	rr := f.do(t, http.MethodGet, "/api/v1/restaurants", "", nil)
	assertStatus(t, rr, http.StatusOK)

	var restaurants []domain.Restaurant
	decodeData(t, rr, &restaurants)

	if len(restaurants) != 1 {
		t.Fatalf("expected only the active restaurant, got %d", len(restaurants))
	}
	if restaurants[0].Name != "Go Pizza" {
		t.Errorf("unexpected restaurant %q", restaurants[0].Name)
	}
	if restaurants[0].APIKey != "" {
		t.Errorf("api_key must not appear in the catalogue, got %q", restaurants[0].APIKey)
	}
}

func TestClientAPI_Pagination_RejectsOutOfRangeValues(t *testing.T) {
	f := newAPIFixture()

	for _, query := range []string{"?limit=200", "?limit=0", "?limit=abc", "?offset=-1", "?offset=x"} {
		rr := f.do(t, http.MethodGet, "/api/v1/restaurants"+query, "", nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", query, rr.Code)
		}
	}

	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/restaurants?limit=100&offset=0", "", nil), http.StatusOK)
}

func TestClientAPI_GetMenu_UnknownRestaurantIsNotFound(t *testing.T) {
	f := newAPIFixture()

	rr := f.do(t, http.MethodGet, "/api/v1/restaurants/"+uuid.New().String()+"/menu", "", nil)
	assertStatus(t, rr, http.StatusNotFound)
}

func TestClientAPI_CreateOrder_Flow(t *testing.T) {
	f := newAPIFixture()
	itemID := f.addDish("Cheeseburger", 25000, 5)

	rr := f.do(t, http.MethodPost, "/api/v1/orders", "", domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    f.restID,
		DeliveryAddress: "ul. Tverskaia, 10, kv 5",
		Comment:         "Ostavit' u dveri",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: itemID, Quantity: 2}},
	})
	assertStatus(t, rr, http.StatusCreated)

	var order domain.Order
	decodeData(t, rr, &order)

	if order.Status != domain.OrderStatusCreated {
		t.Errorf("expected status created, got %s", order.Status)
	}
	if order.TotalPriceCents != 50000 {
		t.Errorf("expected total 50000, got %d", order.TotalPriceCents)
	}
	if len(order.Items) != 1 || order.Items[0].Quantity != 2 {
		t.Errorf("unexpected receipt: %+v", order.Items)
	}
}

func TestClientAPI_CreateOrder_OutOfStockIsConflict(t *testing.T) {
	f := newAPIFixture()
	itemID := f.addDish("Cheeseburger", 25000, 1)

	rr := f.do(t, http.MethodPost, "/api/v1/orders", "", domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    f.restID,
		DeliveryAddress: "ul. Tverskaia, 10",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: itemID, Quantity: 5}},
	})
	assertStatus(t, rr, http.StatusConflict)
}

func TestClientAPI_GetRestaurant_NotFoundAndInvalidUUID(t *testing.T) {
	f := newAPIFixture()

	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/restaurants/"+uuid.New().String(), "", nil), http.StatusNotFound)
	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/restaurants/invalid-uuid", "", nil), http.StatusBadRequest)
}

func TestPartnerAPI_RegistrationIssuesAnAPIKey(t *testing.T) {
	f := newAPIFixture()

	rr := f.register(t, domain.CreateRestaurantInput{
		Name:       "New Partner Bistro",
		Address:    "ul. Arbat, 15",
		WebhookURL: "https://bistro.example.com/webhook",
	})
	assertStatus(t, rr, http.StatusCreated)

	var created domain.Restaurant
	decodeData(t, rr, &created)
	if created.APIKey == "" {
		t.Fatal("registration must return the api key exactly once")
	}
	if created.WebhookURL != "https://bistro.example.com/webhook" {
		t.Errorf("registration must echo the webhook url back, got %q", created.WebhookURL)
	}

	assertStatus(t, f.do(t, http.MethodGet,
		"/api/v1/partner/restaurants/"+created.ID.String()+"/orders", created.APIKey, nil), http.StatusOK)
}

// TestPartnerAPI_RequiresCredentials is the regression test for the closed partner area:
// without a valid key nothing in it may be reachable.
func TestPartnerAPI_RequiresCredentials(t *testing.T) {
	f := newAPIFixture()
	itemID := f.addDish("Margarita", 50000, 5)

	newPrice := int64(1)
	cases := []struct {
		name   string
		method string
		path   string
		key    string
		body   any
	}{
		{"no key at all", http.MethodPatch, "/api/v1/partner/items/" + itemID.String(), "", domain.UpdateMenuItemInput{PriceCents: &newPrice}},
		{"unknown key", http.MethodPatch, "/api/v1/partner/items/" + itemID.String(), "not-a-real-key", domain.UpdateMenuItemInput{PriceCents: &newPrice}},
		{"no key on the order queue", http.MethodGet, "/api/v1/partner/restaurants/" + uuid.New().String() + "/orders", "", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.do(t, tc.method, tc.path, tc.key, tc.body)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d; body: %s", rr.Code, rr.Body.String())
			}
		})
	}

	item, err := f.menuRepo.GetItemByID(t.Context(), itemID)
	if err != nil {
		t.Fatalf("read dish: %v", err)
	}
	if item.PriceCents != 50000 {
		t.Errorf("price must be untouched, got %d", item.PriceCents)
	}
}

// TestPartnerAPI_CannotTouchAnotherEstablishment covers ownership on top of authentication:
// a valid key only unlocks its own establishment.
func TestPartnerAPI_CannotTouchAnotherEstablishment(t *testing.T) {
	f := newAPIFixture()
	itemID := f.addDish("Margarita", 50000, 5)

	intruderID := uuid.New()
	const intruderKey = "intruder-api-key"
	f.restRepo.Put(&domain.Restaurant{ID: intruderID, Name: "Konkurent", IsActive: true, APIKey: intruderKey})

	newPrice := int64(1)
	rr := f.do(t, http.MethodPatch, "/api/v1/partner/items/"+itemID.String(), intruderKey,
		domain.UpdateMenuItemInput{PriceCents: &newPrice})
	assertStatus(t, rr, http.StatusNotFound)

	rr = f.do(t, http.MethodGet, "/api/v1/partner/restaurants/"+f.restID.String()+"/orders", intruderKey, nil)
	assertStatus(t, rr, http.StatusNotFound)

	item, err := f.menuRepo.GetItemByID(t.Context(), itemID)
	if err != nil {
		t.Fatalf("read dish: %v", err)
	}
	if item.PriceCents != 50000 {
		t.Errorf("price must be untouched, got %d", item.PriceCents)
	}
}

// TestPartnerAPI_CreateItemDefaultsToAvailable guards the trap where an omitted flag used to
// make a freshly added dish invisible in the customer's menu.
func TestPartnerAPI_CreateItemDefaultsToAvailable(t *testing.T) {
	f := newAPIFixture()

	catID := uuid.New()
	f.menuRepo.PutCategory(&domain.MenuCategory{ID: catID, RestaurantID: f.restID, Name: "Pitstsa"})

	rr := f.do(t, http.MethodPost, "/api/v1/partner/restaurants/"+f.restID.String()+"/items", testAPIKey,
		map[string]any{
			"category_id":    catID,
			"name":           "Margarita",
			"price_cents":    50000,
			"stock_quantity": 10,
		})
	assertStatus(t, rr, http.StatusCreated)

	var item domain.MenuItem
	decodeData(t, rr, &item)
	if !item.IsAvailable {
		t.Error("a dish created without is_available must default to available")
	}
}

func TestPartnerAPI_OrderQueue_RejectsUnknownStatusFilter(t *testing.T) {
	f := newAPIFixture()
	base := "/api/v1/partner/restaurants/" + f.restID.String() + "/orders"

	assertStatus(t, f.do(t, http.MethodGet, base+"?status=garbage", testAPIKey, nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, base+"?status=cooking", testAPIKey, nil), http.StatusOK)
	assertStatus(t, f.do(t, http.MethodGet, base, testAPIKey, nil), http.StatusOK)
}

func TestSecurity_TrailingJunkInJSON(t *testing.T) {
	f := newAPIFixture()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/partner/restaurants",
		strings.NewReader(`{"name":"Bistro","address":"Moscow"} {"extra":"junk"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(bootstrapHeader, testBootstrapToken)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)

	assertStatus(t, rr, http.StatusBadRequest)
}

// TestSecurity_WebhookURLMustNotTargetInternalHosts is the SSRF regression test.
func TestSecurity_WebhookURLMustNotTargetInternalHosts(t *testing.T) {
	f := newAPIFixture()

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/webhook",
		"http://10.0.0.5/webhook",
		"http://192.168.1.10/webhook",
		"ftp://example.com/webhook",
		"not-a-url",
	}

	for _, url := range blocked {
		t.Run(url, func(t *testing.T) {
			rr := f.register(t, domain.CreateRestaurantInput{
				Name: "SSRF probe", Address: "nowhere", WebhookURL: url,
			})
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for %q, got %d", url, rr.Code)
			}
		})
	}

	assertStatus(t, f.register(t, domain.CreateRestaurantInput{
		Name: "Legit", Address: "Moscow", WebhookURL: "https://partner.example.com/webhook",
	}), http.StatusCreated)
}

// brokenRestaurantRepo simulates an unavailable database.
type brokenRestaurantRepo struct {
	*inmem.RestaurantRepository
	err error
}

func (b brokenRestaurantRepo) GetByAPIKey(context.Context, string) (*domain.Restaurant, error) {
	return nil, b.err
}

// TestPartnerAuth_InfrastructureFailureIsNot401 pins down the difference between "your key is
// wrong" and "we are broken". Reporting an outage as 401 sends the partner hunting for a key
// problem that does not exist and hides the incident from 5xx alerting.
func TestPartnerAuth_InfrastructureFailureIsNot401(t *testing.T) {
	repo := brokenRestaurantRepo{
		RestaurantRepository: inmem.NewRestaurantRepository(),
		err:                  errors.New("dial tcp: connection refused"),
	}
	restService := service.NewRestaurantService(repo)
	menuService := service.NewMenuService(inmem.NewMenuRepository(), repo)
	orderService := service.NewOrderService(
		inmem.NewOrderRepository(), inmem.NewMenuRepository(), repo,
		&inmem.TxManager{}, &inmem.WebhookRecorder{},
	)

	router := NewRouter(
		NewClientHandler(restService, menuService, orderService),
		NewPartnerHandler(restService, menuService, orderService, testBootstrapToken),
		PartnerAuth(restService),
	)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/partner/restaurants/"+uuid.New().String()+"/orders", http.NoBody)
	req.Header.Set("Authorization", "Bearer any-key")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a database outage must surface as 500, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

// TestPartnerAuth_AcceptsBothCredentialHeaders: RFC 7235 makes the auth scheme
// case-insensitive, and X-Api-Key is documented as the alternative.
func TestPartnerAuth_AcceptsBothCredentialHeaders(t *testing.T) {
	path := "/api/v1/partner/restaurants/"

	cases := []struct {
		name   string
		header string
		value  string
		want   int
	}{
		{"canonical bearer", "Authorization", "Bearer " + testAPIKey, http.StatusOK},
		{"lowercase scheme", "Authorization", "bearer " + testAPIKey, http.StatusOK},
		{"uppercase scheme", "Authorization", "BEARER " + testAPIKey, http.StatusOK},
		{"api key header", "X-Api-Key", testAPIKey, http.StatusOK},
		{"wrong scheme", "Authorization", "Basic " + testAPIKey, http.StatusUnauthorized},
		{"empty bearer", "Authorization", "Bearer ", http.StatusUnauthorized},
		{"malformed utf-8 bearer", "Authorization", "Bearer key\xff\xfe", http.StatusUnauthorized},
		{"malformed utf-8 api key", "X-Api-Key", "abc\xc3(", http.StatusUnauthorized},
		{"nul byte in api key", "X-Api-Key", "abc\x00def", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture()
			req := httptest.NewRequest(http.MethodGet, path+f.restID.String()+"/orders", http.NoBody)
			req.Header.Set(tc.header, tc.value)
			rr := httptest.NewRecorder()
			f.router.ServeHTTP(rr, req)

			if rr.Code != tc.want {
				t.Errorf("expected %d, got %d; body: %s", tc.want, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestClientAPI_Search_BoundsMatchTheIndex: a pattern shorter than the trigram index can serve
// would turn the catalogue endpoint into a full table scan, so it is rejected outright.
func TestClientAPI_Search_BoundsMatchTheIndex(t *testing.T) {
	f := newAPIFixture()

	tooShort := []string{"a", "ab", "pi"}
	for _, q := range tooShort {
		rr := f.do(t, http.MethodGet, "/api/v1/restaurants?search="+url.QueryEscape(q), "", nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("search=%q: expected 400, got %d", q, rr.Code)
		}
	}

	for _, q := range []string{"abc", "pits", "Avi"} {
		rr := f.do(t, http.MethodGet, "/api/v1/restaurants?search="+url.QueryEscape(q), "", nil)
		if rr.Code != http.StatusOK {
			t.Errorf("search=%q: expected 200, got %d", q, rr.Code)
		}
	}

	malformed := []string{"\xc3(\xc3(", "abc\xc3(", "\xff\xff\xff", "pits\xff", "abc\x00", "\x00\x00\x00"}
	for _, q := range malformed {
		rr := f.do(t, http.MethodGet, "/api/v1/restaurants?search="+url.QueryEscape(q), "", nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("search=%q: expected 400, got %d", q, rr.Code)
		}
	}

	rr := f.do(t, http.MethodGet, "/api/v1/restaurants?search="+strings.Repeat("a", 101), "", nil)
	assertStatus(t, rr, http.StatusBadRequest)
}

// TestClientAPI_PartnerInternalFieldsNeverReachAClient is the regression test for the public
// projection. api_key is a credential and webhook_url is the establishment's internal address:
// neither has any business in a response a customer can ask for. The assertion is on the raw body
// rather than on a decoded struct on purpose — a renamed or re-added field would still be caught.
func TestClientAPI_PartnerInternalFieldsNeverReachAClient(t *testing.T) {
	f := newAPIFixture()
	f.addDish("Margarita", 50000, 5)

	paths := []string{
		"/api/v1/restaurants",
		"/api/v1/restaurants/" + f.restID.String(),
		"/api/v1/restaurants/" + f.restID.String() + "/menu",
	}

	for _, path := range paths {
		rr := f.do(t, http.MethodGet, path, "", nil)
		assertStatus(t, rr, http.StatusOK)

		for _, secret := range []string{"webhook_url", "go-pizza.example.com", "api_key", testAPIKey} {
			if strings.Contains(rr.Body.String(), secret) {
				t.Errorf("%s leaked %q; body: %s", path, secret, rr.Body.String())
			}
		}
	}
}

// TestClientAPI_ListOrders_ReturnsOnlyTheCallersHistory covers the "moi zakazy" endpoint. The
// assignment puts user authentication out of scope, so user_id comes from the query - which makes
// it mandatory: an unfiltered list would hand out everyone's orders.
func TestClientAPI_ListOrders_ReturnsOnlyTheCallersHistory(t *testing.T) {
	f := newAPIFixture()
	itemID := f.addDish("Margarita", 25000, 10)

	me, someoneElse := uuid.New(), uuid.New()
	place := func(user uuid.UUID) {
		t.Helper()
		rr := f.do(t, http.MethodPost, "/api/v1/orders", "", domain.CreateOrderInput{
			UserID:          user,
			RestaurantID:    f.restID,
			DeliveryAddress: "ul. Tverskaia, 10",
			Items:           []domain.CreateOrderItemInput{{MenuItemID: itemID, Quantity: 1}},
		})
		assertStatus(t, rr, http.StatusCreated)
	}
	place(me)
	place(me)
	place(someoneElse)

	rr := f.do(t, http.MethodGet, "/api/v1/orders?user_id="+me.String(), "", nil)
	assertStatus(t, rr, http.StatusOK)

	var mine []domain.Order
	decodeData(t, rr, &mine)
	if len(mine) != 2 {
		t.Fatalf("expected the caller's two orders, got %d", len(mine))
	}
	for _, o := range mine {
		if o.UserID != me {
			t.Errorf("another customer's order leaked into the history: %s", o.ID)
		}
	}

	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/orders", "", nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/orders?user_id=not-a-uuid", "", nil), http.StatusBadRequest)

	assertStatus(t, f.do(t, http.MethodGet,
		"/api/v1/orders?user_id="+me.String()+"&status=garbage", "", nil), http.StatusBadRequest)

	rr = f.do(t, http.MethodGet, "/api/v1/orders?user_id="+me.String()+"&status=created", "", nil)
	assertStatus(t, rr, http.StatusOK)
	decodeData(t, rr, &mine)
	if len(mine) != 2 {
		t.Errorf("expected both freshly created orders, got %d", len(mine))
	}
}

// TestClientAPI_SearchItems_FindsTheDishNotJustTheEstablishment covers the question the assignment
// asks in its own words - how a person finds a product. Catalogue search answers only half of it:
// it matches an establishment's name, so shaurma on someone's menu stays invisible.
func TestClientAPI_SearchItems_FindsTheDishNotJustTheEstablishment(t *testing.T) {
	f := newAPIFixture() // the fixture establishment is called "Go Pizza"
	f.addDish("Shaurma klassicheskaia", 32000, 5)
	f.addDish("Margarita", 50000, 5)

	rr := f.do(t, http.MethodGet, "/api/v1/items?search="+url.QueryEscape("shaurma"), "", nil)
	assertStatus(t, rr, http.StatusOK)
	var found []domain.MenuItemSearchResult
	decodeData(t, rr, &found)
	if len(found) != 1 || found[0].Name != "Shaurma klassicheskaia" {
		t.Fatalf("expected exactly the shawarma, got %+v", found)
	}
	if found[0].RestaurantID != f.restID {
		t.Errorf("a result must point at the establishment selling it, got %s", found[0].RestaurantID)
	}
}

// TestClientAPI_SearchItems_BoundsAndStopList pins down the rules around the dish search: a dish on
// the stop list is not orderable and must not be offered, and the search term is mandatory because
// a global list of every dish is exactly the full scan the length guard exists to prevent.
func TestClientAPI_SearchItems_BoundsAndStopList(t *testing.T) {
	f := newAPIFixture()
	f.addDish("Pepperoni ostraia", 55000, 5)

	f.menuRepo.PutItem(&domain.MenuItem{
		ID: uuid.New(), RestaurantID: f.restID, CategoryID: uuid.New(),
		Name: "Pepperoni sniataia", PriceCents: 55000, StockQuantity: 5, IsAvailable: false,
	})

	rr := f.do(t, http.MethodGet, "/api/v1/items?search="+url.QueryEscape("pepperoni"), "", nil)
	assertStatus(t, rr, http.StatusOK)
	var found []domain.MenuItemSearchResult
	decodeData(t, rr, &found)
	if len(found) != 1 || found[0].Name != "Pepperoni ostraia" {
		t.Fatalf("the stop list must be respected, got %+v", found)
	}

	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/items", "", nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/items?search=ab", "", nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/items?search="+strings.Repeat("a", 101), "", nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/items?search=abc%00", "", nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, "/api/v1/items?search=abc&limit=999", "", nil), http.StatusBadRequest)
}

// TestPartnerAPI_MenuLifecycle walks one establishment through the whole job the partner API
// exists for: register, fill the menu, take an order, move it along, and clear the menu again.
// Each step is checked from the customer's side too, because a partner action that the catalogue
// does not reflect is not done.
func TestPartnerAPI_MenuLifecycle(t *testing.T) {
	f := newAPIFixture()

	rr := f.register(t, domain.CreateRestaurantInput{
		Name:       "Kukhnia polnogo tsikla",
		Address:    "ul. Polevaia, 7",
		WebhookURL: "https://lifecycle.example.com/webhook",
	})
	assertStatus(t, rr, http.StatusCreated)
	var rest domain.Restaurant
	decodeData(t, rr, &rest)
	key, restPath := rest.APIKey, "/api/v1/partner/restaurants/"+rest.ID.String()

	newDescription := "Gotovim pri vas"
	rr = f.do(t, http.MethodPatch, restPath, key, domain.UpdateRestaurantInput{Description: &newDescription})
	assertStatus(t, rr, http.StatusOK)
	var updated domain.Restaurant
	decodeData(t, rr, &updated)
	if updated.Description != newDescription {
		t.Errorf("description was not updated, got %q", updated.Description)
	}

	rr = f.do(t, http.MethodPost, restPath+"/categories", key, CreateCategoryRequest{Name: "Garniry", SortOrder: 2})
	assertStatus(t, rr, http.StatusCreated)
	var cat domain.MenuCategory
	decodeData(t, rr, &cat)

	renamed := "Goriachee"
	rr = f.do(t, http.MethodPatch, "/api/v1/partner/categories/"+cat.ID.String(), key,
		domain.UpdateMenuCategoryInput{Name: &renamed})
	assertStatus(t, rr, http.StatusOK)
	var recat domain.MenuCategory
	decodeData(t, rr, &recat)
	if recat.Name != renamed {
		t.Errorf("category was not renamed, got %q", recat.Name)
	}

	rr = f.do(t, http.MethodPost, restPath+"/items", key, CreateItemRequest{
		CategoryID: cat.ID, Name: "Plov", Description: "S baraninoi", PriceCents: 42000, StockQuantity: 4,
	})
	assertStatus(t, rr, http.StatusCreated)
	var dish domain.MenuItem
	decodeData(t, rr, &dish)

	rr = f.do(t, http.MethodGet, "/api/v1/restaurants/"+rest.ID.String()+"/menu", "", nil)
	assertStatus(t, rr, http.StatusOK)
	var menu []domain.MenuCategory
	decodeData(t, rr, &menu)
	if len(menu) != 1 || len(menu[0].Items) != 1 || menu[0].Items[0].ID != dish.ID {
		t.Fatalf("the customer does not see the dish the partner added: %+v", menu)
	}

	customer := uuid.New()
	rr = f.do(t, http.MethodPost, "/api/v1/orders", "", domain.CreateOrderInput{
		UserID: customer, RestaurantID: rest.ID, DeliveryAddress: "ul. Polevaia, 8",
		Items: []domain.CreateOrderItemInput{{MenuItemID: dish.ID, Quantity: 1}},
	})
	assertStatus(t, rr, http.StatusCreated)
	var order domain.Order
	decodeData(t, rr, &order)

	rr = f.do(t, http.MethodGet, "/api/v1/orders/"+order.ID.String()+"?user_id="+customer.String(), "", nil)
	assertStatus(t, rr, http.StatusOK)
	var fetched domain.Order
	decodeData(t, rr, &fetched)
	if fetched.Status != domain.OrderStatusCreated {
		t.Errorf("expected a freshly created order, got %s", fetched.Status)
	}

	rr = f.do(t, http.MethodPatch, "/api/v1/partner/orders/"+order.ID.String()+"/status", key,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusAccepted, Comment: "Vziali v rabotu"})
	assertStatus(t, rr, http.StatusOK)

	rr = f.do(t, http.MethodPost, "/api/v1/orders/"+order.ID.String()+"/cancel", "",
		CancelOrderRequest{UserID: customer, Reason: "peredumal"})
	assertStatus(t, rr, http.StatusUnprocessableEntity)

	rr = f.do(t, http.MethodPost, "/api/v1/orders", "", domain.CreateOrderInput{
		UserID: customer, RestaurantID: rest.ID, DeliveryAddress: "ul. Polevaia, 8",
		Items: []domain.CreateOrderItemInput{{MenuItemID: dish.ID, Quantity: 1}},
	})
	assertStatus(t, rr, http.StatusCreated)
	var second domain.Order
	decodeData(t, rr, &second)

	rr = f.do(t, http.MethodPost, "/api/v1/orders/"+second.ID.String()+"/cancel", "",
		CancelOrderRequest{UserID: customer, Reason: "peredumal"})
	assertStatus(t, rr, http.StatusOK)

	assertStatus(t, f.do(t, http.MethodDelete, "/api/v1/partner/items/"+dish.ID.String(), key, nil), http.StatusNoContent)
	assertStatus(t, f.do(t, http.MethodDelete, "/api/v1/partner/categories/"+cat.ID.String(), key, nil), http.StatusNoContent)

	rr = f.do(t, http.MethodGet, "/api/v1/restaurants/"+rest.ID.String()+"/menu", "", nil)
	assertStatus(t, rr, http.StatusOK)
	decodeData(t, rr, &menu)
	if len(menu) != 0 {
		t.Errorf("the menu should be empty after the partner cleared it, got %+v", menu)
	}
}

// TestPartnerAPI_RegistrationIsGated: the assignment asks for closed access for a certain list of
// establishments. Registration is what puts an establishment on that list, so it is gated too —
// otherwise the list is "everyone who asked".
func TestPartnerAPI_RegistrationIsGated(t *testing.T) {
	f := newAPIFixture()
	body := domain.CreateRestaurantInput{Name: "Kafe s ulitsy", Address: "Moskva"}

	assertStatus(t, f.do(t, http.MethodPost, "/api/v1/partner/restaurants", "", body), http.StatusUnauthorized)

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/partner/restaurants", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(bootstrapHeader, "not-the-token")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assertStatus(t, rr, http.StatusUnauthorized)

	assertStatus(t, f.do(t, http.MethodPost, "/api/v1/partner/restaurants", testAPIKey, body), http.StatusUnauthorized)

	ok := f.register(t, body)
	assertStatus(t, ok, http.StatusCreated)
	var created domain.Restaurant
	decodeData(t, ok, &created)
	if created.APIKey == "" {
		t.Error("registration must still issue an api key")
	}
}

// TestPartnerAPI_RegistrationClosedWithoutAToken: with nothing configured the endpoint stays shut
// rather than falling open, so a deployment that forgot the variable does not silently invite
// everyone onto the platform.
func TestPartnerAPI_RegistrationClosedWithoutAToken(t *testing.T) {
	restRepo := inmem.NewRestaurantRepository()
	menuRepo := inmem.NewMenuRepository()
	orderRepo := inmem.NewOrderRepository()

	restService := service.NewRestaurantService(restRepo)
	menuService := service.NewMenuService(menuRepo, restRepo)
	orderService := service.NewOrderService(orderRepo, menuRepo, restRepo, &inmem.TxManager{}, &inmem.WebhookRecorder{})

	router := NewRouter(
		NewClientHandler(restService, menuService, orderService),
		NewPartnerHandler(restService, menuService, orderService, ""),
		PartnerAuth(restService),
	)

	raw, err := json.Marshal(domain.CreateRestaurantInput{Name: "Kafe", Address: "Moskva"})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/partner/restaurants", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(bootstrapHeader, "anything")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assertStatus(t, rr, http.StatusUnauthorized)
}

// TestClientAPI_GetOrder_OnlyItsOwnerCanRead: an order card carries a delivery address, a comment
// and a receipt. The identifier is not a credential, so reading someone else's order must fail —
// and fail as 404, so the endpoint cannot be used to confirm that an order exists.
func TestClientAPI_GetOrder_OnlyItsOwnerCanRead(t *testing.T) {
	f := newAPIFixture()
	itemID := f.addDish("Margarita", 25000, 5)

	owner := uuid.New()
	rr := f.do(t, http.MethodPost, "/api/v1/orders", "", domain.CreateOrderInput{
		UserID:          owner,
		RestaurantID:    f.restID,
		DeliveryAddress: "Secret address, kv 42",
		Comment:         "domofon 4242",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: itemID, Quantity: 1}},
	})
	assertStatus(t, rr, http.StatusCreated)

	var order domain.Order
	decodeData(t, rr, &order)
	path := "/api/v1/orders/" + order.ID.String()

	rr = f.do(t, http.MethodGet, path+"?user_id="+owner.String(), "", nil)
	assertStatus(t, rr, http.StatusOK)

	assertStatus(t, f.do(t, http.MethodGet, path+"?user_id="+uuid.New().String(), "", nil), http.StatusNotFound)
	assertStatus(t, f.do(t, http.MethodGet,
		"/api/v1/orders/"+uuid.New().String()+"?user_id="+owner.String(), "", nil), http.StatusNotFound)

	assertStatus(t, f.do(t, http.MethodGet, path, "", nil), http.StatusBadRequest)
	assertStatus(t, f.do(t, http.MethodGet, path+"?user_id=not-a-uuid", "", nil), http.StatusBadRequest)

	body := f.do(t, http.MethodGet, path+"?user_id="+uuid.New().String(), "", nil).Body.String()
	if strings.Contains(body, "Secret address") {
		t.Errorf("a refused read leaked the delivery address: %s", body)
	}
}
