// Package integration exercises the persistence layer against a real PostgreSQL instance.
//
// These tests are the ones that can prove what mocks cannot: that the transaction really rolls
// back, that SELECT ... FOR UPDATE really serialises competing checkouts, and that the schema
// constraints really hold. They are skipped unless TEST_DATABASE_URL points at a database with
// the migrations applied:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/go_kitchen?sslmode=disable" go test ./tests/integration/...
//
// `make test-integration` does exactly that.
package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/repository/postgres"
	"github.com/arthkinq/go-kitchen/internal/service"
)

// env holds everything a test needs to talk to the real database.
type env struct {
	pool      *pgxpool.Pool
	restRepo  *postgres.RestaurantRepository
	menuRepo  *postgres.MenuRepository
	orderRepo *postgres.OrderRepository
	orderSvc  *service.OrderService
	menuSvc   *service.MenuService
	restSvc   *service.RestaurantService
}

// noopDispatcher keeps the tests free of outbound HTTP.
type noopDispatcher struct{}

func (noopDispatcher) Dispatch(context.Context, string, string, any) error { return nil }

func newEnv(t *testing.T) *env {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration tests")
	}

	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	restRepo := postgres.NewRestaurantRepository(pool)
	menuRepo := postgres.NewMenuRepository(pool)
	orderRepo := postgres.NewOrderRepository(pool)
	txManager := postgres.NewTxManager(pool)

	return &env{
		pool:      pool,
		restRepo:  restRepo,
		menuRepo:  menuRepo,
		orderRepo: orderRepo,
		orderSvc:  service.NewOrderService(orderRepo, menuRepo, restRepo, txManager, noopDispatcher{}),
		menuSvc:   service.NewMenuService(menuRepo, restRepo),
		restSvc:   service.NewRestaurantService(restRepo),
	}
}

// newRestaurant registers an establishment and removes it (and everything it owns) afterwards.
func (e *env) newRestaurant(t *testing.T, name string) *domain.Restaurant {
	t.Helper()
	ctx := context.Background()

	rest, err := e.restSvc.Create(ctx, domain.CreateRestaurantInput{
		Name:    name,
		Address: "Moskva, testovaia, 1",
	})
	if err != nil {
		t.Fatalf("create restaurant: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = e.pool.Exec(cleanupCtx, `DELETE FROM orders WHERE restaurant_id = $1`, rest.ID)
		_, _ = e.pool.Exec(cleanupCtx, `DELETE FROM restaurants WHERE id = $1`, rest.ID)
	})

	return rest
}

func (e *env) newDish(t *testing.T, restID uuid.UUID, name string, priceCents int64, stock int) *domain.MenuItem {
	t.Helper()
	ctx := context.Background()

	cat := &domain.MenuCategory{RestaurantID: restID, Name: "Kategoriia " + name}
	if err := e.menuRepo.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("create category: %v", err)
	}

	item, err := e.menuSvc.CreateItem(ctx, domain.CreateMenuItemInput{
		RestaurantID:  restID,
		CategoryID:    cat.ID,
		Name:          name,
		PriceCents:    priceCents,
		StockQuantity: stock,
		IsAvailable:   true,
	})
	if err != nil {
		t.Fatalf("create dish: %v", err)
	}
	return item
}

func (e *env) stockOf(t *testing.T, itemID uuid.UUID) int {
	t.Helper()
	item, err := e.menuRepo.GetItemByID(context.Background(), itemID)
	if err != nil {
		t.Fatalf("read dish: %v", err)
	}
	return item.StockQuantity
}

// TestPostgres_OrderRoundTrip checks that an order, its receipt snapshot and its audit trail
// all survive a write/read cycle through real SQL.
func TestPostgres_OrderRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rest := e.newRestaurant(t, "Round Trip Pizza")
	dish := e.newDish(t, rest.ID, "Margarita", 59000, 10)

	userID := uuid.New()
	created, err := e.orderSvc.CreateOrder(ctx, domain.CreateOrderInput{
		UserID:          userID,
		RestaurantID:    rest.ID,
		DeliveryAddress: "Moskva, Arbat, 5",
		Comment:         "domofon 12",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: dish.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if created.TotalPriceCents != 118000 {
		t.Errorf("expected total 118000, got %d", created.TotalPriceCents)
	}
	if got := e.stockOf(t, dish.ID); got != 8 {
		t.Errorf("expected stock 8 after checkout, got %d", got)
	}

	newPrice := int64(99000)
	if _, err := e.menuSvc.UpdateItem(ctx, rest.ID, dish.ID, domain.UpdateMenuItemInput{PriceCents: &newPrice}); err != nil {
		t.Fatalf("reprice dish: %v", err)
	}

	loaded, err := e.orderRepo.GetOrderByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if len(loaded.Items) != 1 || loaded.Items[0].PriceCents != 59000 {
		t.Errorf("receipt must snapshot the original price, got %+v", loaded.Items)
	}
	if loaded.TotalPriceCents != 118000 {
		t.Errorf("order total must not follow the menu, got %d", loaded.TotalPriceCents)
	}
	if len(loaded.StatusHistory) != 1 || loaded.StatusHistory[0].Actor != domain.ActorSystem {
		t.Errorf("expected one system-authored audit entry, got %+v", loaded.StatusHistory)
	}

	if _, err := e.orderSvc.UpdateOrderStatus(ctx, rest.ID, created.ID,
		domain.UpdateOrderStatusInput{Status: domain.OrderStatusAccepted}); err != nil {
		t.Fatalf("accept order: %v", err)
	}
	accepted, err := e.orderRepo.GetOrderByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("re-read order: %v", err)
	}
	last := accepted.StatusHistory[len(accepted.StatusHistory)-1]
	if last.ToStatus != domain.OrderStatusAccepted || last.Actor != domain.ActorRestaurant {
		t.Errorf("expected a restaurant-authored 'accepted' entry, got %+v", last)
	}
}

// TestPostgres_CheckoutRollsBackOnFailure is the test an in-memory mock cannot express:
// when the second line of an order is rejected, the stock already taken from the first line
// must be given back by the transaction.
func TestPostgres_CheckoutRollsBackOnFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	ours := e.newRestaurant(t, "Rollback Kitchen")
	theirs := e.newRestaurant(t, "Someone Else")

	goodDish := e.newDish(t, ours.ID, "Sup", 20000, 5)
	foreignDish := e.newDish(t, theirs.ID, "Salat", 15000, 5)

	_, err := e.orderSvc.CreateOrder(ctx, domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    ours.ID,
		DeliveryAddress: "Moskva, Mira, 1",
		Items: []domain.CreateOrderItemInput{
			{MenuItemID: goodDish.ID, Quantity: 2},
			{MenuItemID: foreignDish.ID, Quantity: 1},
		},
	})
	if !errors.Is(err, domain.ErrRestaurantMismatch) {
		t.Fatalf("expected ErrRestaurantMismatch, got %v", err)
	}

	if got := e.stockOf(t, goodDish.ID); got != 5 {
		t.Errorf("the transaction must have rolled the decrement back, stock = %d, want 5", got)
	}
}

// TestPostgres_NoOversellUnderConcurrency drives real concurrent checkouts through
// SELECT ... FOR UPDATE. Exactly as many orders as there are units in stock may succeed.
func TestPostgres_NoOversellUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rest := e.newRestaurant(t, "High Concurrency Burger")

	const (
		initialStock  = 5
		totalAttempts = 40
	)
	dish := e.newDish(t, rest.ID, "Limitirovannyi burger", 50000, initialStock)

	var success, outOfStock, unexpected int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < totalAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			_, err := e.orderSvc.CreateOrder(ctx, domain.CreateOrderInput{
				UserID:          uuid.New(),
				RestaurantID:    rest.ID,
				DeliveryAddress: "Moskva, Tverskaia, 1",
				Items:           []domain.CreateOrderItemInput{{MenuItemID: dish.ID, Quantity: 1}},
			})
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, domain.ErrOutOfStock):
				atomic.AddInt64(&outOfStock, 1)
			default:
				atomic.AddInt64(&unexpected, 1)
				t.Errorf("unexpected checkout error: %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("%d checkouts failed for an unexpected reason", unexpected)
	}
	if success != initialStock {
		t.Errorf("expected exactly %d successful orders, got %d", initialStock, success)
	}
	if outOfStock != totalAttempts-initialStock {
		t.Errorf("expected %d out-of-stock rejections, got %d", totalAttempts-initialStock, outOfStock)
	}
	if got := e.stockOf(t, dish.ID); got != 0 {
		t.Errorf("expected the stock to be drained to 0, got %d", got)
	}
}

// TestPostgres_MultiItemCheckoutIsDeadlockFree runs concurrent multi-item checkouts over the same
// two dishes and asserts that none of them dies and that the stock adds up.
//
// The two halves pass the dish ids in opposite order, but that alone proves nothing: `WHERE
// id = ANY($1)` returns rows in scan order, not in the order of the array, so the caller's order
// never reaches the lock. What makes the lock order a contract instead of an accident of the plan
// is the ORDER BY id ASC in GetItemsByIDsForUpdate - and the plan confirms it applies, because
// LockRows sits above Sort, so rows are locked after sorting rather than as they are scanned.
func TestPostgres_MultiItemCheckoutIsDeadlockFree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rest := e.newRestaurant(t, "Deadlock Free Kitchen")
	dishA := e.newDish(t, rest.ID, "Bliudo A", 10000, 20)
	dishB := e.newDish(t, rest.ID, "Bliudo B", 20000, 20)

	const buyers = 20
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start

			items := []domain.CreateOrderItemInput{
				{MenuItemID: dishA.ID, Quantity: 1},
				{MenuItemID: dishB.ID, Quantity: 1},
			}
			if idx%2 == 1 {
				items[0], items[1] = items[1], items[0]
			}

			if _, err := e.orderSvc.CreateOrder(ctx, domain.CreateOrderInput{
				UserID:          uuid.New(),
				RestaurantID:    rest.ID,
				DeliveryAddress: "Moskva, Lenina, 1",
				Items:           items,
			}); err != nil {
				t.Errorf("checkout %d failed: %v", idx, err)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if got := e.stockOf(t, dishA.ID); got != 0 {
		t.Errorf("dish A: expected stock 0, got %d", got)
	}
	if got := e.stockOf(t, dishB.ID); got != 0 {
		t.Errorf("dish B: expected stock 0, got %d", got)
	}
}

// TestPostgres_PartnerAPIKeyResolvesItsOwner covers the credential lookup behind the closed
// partner area, including the uniqueness guarantee of the issued keys.
func TestPostgres_PartnerAPIKeyResolvesItsOwner(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	first := e.newRestaurant(t, "Key Owner One")
	second := e.newRestaurant(t, "Key Owner Two")

	if first.APIKey == "" || first.APIKey == second.APIKey {
		t.Fatalf("registration must issue a distinct api key per establishment")
	}

	resolved, err := e.restSvc.GetByAPIKey(ctx, first.APIKey)
	if err != nil {
		t.Fatalf("resolve by api key: %v", err)
	}
	if resolved.ID != first.ID {
		t.Errorf("expected restaurant %s, got %s", first.ID, resolved.ID)
	}
	if resolved.APIKey != "" {
		t.Errorf("api key must not be selected outside registration, got %q", resolved.APIKey)
	}

	if _, err := e.restSvc.GetByAPIKey(ctx, "definitely-not-a-key"); !errors.Is(err, domain.ErrRestaurantNotFound) {
		t.Errorf("expected ErrRestaurantNotFound for an unknown key, got %v", err)
	}
}

// TestPostgres_SchemaRejectsCrossRestaurantCategory verifies the composite foreign key
// (restaurant_id, category_id): a dish cannot be moved into another establishment's category
// even if the service layer were bypassed.
func TestPostgres_SchemaRejectsCrossRestaurantCategory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	ours := e.newRestaurant(t, "Schema Guard Ours")
	theirs := e.newRestaurant(t, "Schema Guard Theirs")

	dish := e.newDish(t, ours.ID, "Nashe bliudo", 10000, 1)
	foreign := e.newDish(t, theirs.ID, "Chuzhoe bliudo", 10000, 1)

	_, err := e.menuRepo.UpdateItem(ctx, dish.ID, domain.UpdateMenuItemInput{CategoryID: &foreign.CategoryID})
	if err == nil {
		t.Fatal("expected the composite foreign key to reject a cross-restaurant category")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict from the constraint violation, got %v", err)
	}
}

// TestPostgres_SearchTreatsWildcardsAsText: `%` and `_` reach the catalogue from a search box, where
// the person typing them means the characters themselves. As LIKE metacharacters they would do the
// exact opposite of what the minimum length guard exists for — "___" would return the whole
// catalogue instead of nothing. Only real SQL can prove this: the in-memory double does not
// implement search at all.
func TestPostgres_SearchTreatsWildcardsAsText(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	discount := e.newRestaurant(t, "Skidka 50% na vsio")
	e.newRestaurant(t, "Obychnaia pitstseriia bez skidok")

	search := func(pattern string) []domain.Restaurant {
		t.Helper()
		found, err := e.restSvc.List(ctx, domain.RestaurantFilter{Search: pattern, Limit: 100})
		if err != nil {
			t.Fatalf("search %q: %v", pattern, err)
		}
		return found
	}

	contains := func(list []domain.Restaurant, id uuid.UUID) bool {
		for _, r := range list {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	byDiscount := search("50%")
	if !contains(byDiscount, discount.ID) {
		t.Errorf(`search "50%%" did not find the establishment whose name contains it`)
	}
	if len(byDiscount) != 1 {
		t.Errorf(`search "50%%" matched %d establishments, want exactly 1`, len(byDiscount))
	}

	if wild := search("___"); len(wild) != 0 {
		t.Errorf(`search "___" matched %d establishments, want 0`, len(wild))
	}

	if bs := search(`\`); len(bs) != 0 {
		t.Errorf(`search "\" matched %d establishments, want 0`, len(bs))
	}

	if plain := search("Skidka"); !contains(plain, discount.ID) {
		t.Error("an ordinary search stopped matching after escaping")
	}
}

// TestPostgres_ItemSearchFindsDishesTheCatalogueCannot is the assignment's own question answered
// against real SQL: a person hunting for shaurma has to find the dish, and the catalogue - which
// only ever matched an establishment's own name - cannot tell them where it is sold.
func TestPostgres_ItemSearchFindsDishesTheCatalogueCannot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rest := e.newRestaurant(t, "Testovaia Kulinariia")
	dish := e.newDish(t, rest.ID, "Shaurma testovaia", 32000, 5)

	inCatalogue, err := e.restSvc.List(ctx, domain.RestaurantFilter{Search: "Shaurma", Limit: 100})
	if err != nil {
		t.Fatalf("catalogue search: %v", err)
	}
	if len(inCatalogue) != 0 {
		t.Errorf("the catalogue must not match a dish name, got %d establishments", len(inCatalogue))
	}

	search := func(pattern string) []domain.MenuItemSearchResult {
		t.Helper()
		found, err := e.menuSvc.SearchItems(ctx, domain.MenuItemSearchFilter{Search: pattern, Limit: 100})
		if err != nil {
			t.Fatalf("item search %q: %v", pattern, err)
		}
		return found
	}

	found := search("shaurma")
	if len(found) != 1 || found[0].ID != dish.ID {
		t.Fatalf("expected exactly the seeded shawarma, got %+v", found)
	}
	if found[0].RestaurantName != "Testovaia Kulinariia" {
		t.Errorf("expected the establishment name from the join, got %q", found[0].RestaurantName)
	}
	if found[0].RestaurantID != rest.ID {
		t.Errorf("result points at the wrong establishment: %s", found[0].RestaurantID)
	}

	unavailable := false
	if _, err := e.menuRepo.UpdateItem(ctx, dish.ID, domain.UpdateMenuItemInput{IsAvailable: &unavailable}); err != nil {
		t.Fatalf("stop-list the dish: %v", err)
	}
	if got := search("shaurma"); len(got) != 0 {
		t.Errorf("a stop-listed dish must not be searchable, got %d", len(got))
	}

	available := true
	if _, err := e.menuRepo.UpdateItem(ctx, dish.ID, domain.UpdateMenuItemInput{IsAvailable: &available}); err != nil {
		t.Fatalf("restore the dish: %v", err)
	}
	inactive := false
	if _, err := e.restRepo.Update(ctx, rest.ID, domain.UpdateRestaurantInput{IsActive: &inactive}); err != nil {
		t.Fatalf("deactivate the establishment: %v", err)
	}
	if got := search("shaurma"); len(got) != 0 {
		t.Errorf("a closed establishment must not offer dishes, got %d", len(got))
	}
}

// TestPostgres_PublicProjectionOmitsPartnerFields is the SQL-layer guard for the claim the README
// makes: api_key and webhook_url are physically not selected, so no response can leak them by
// accident. Only real SQL can prove it - the in-memory double blanks both fields itself, so a
// handler test keeps passing even with the columns put back into the projection.
func TestPostgres_PublicProjectionOmitsPartnerFields(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	const hookURL = "https://projection-guard.example.com/webhook"

	rest, err := e.restSvc.Create(ctx, domain.CreateRestaurantInput{
		Name:       "Zavedenie s vebkhukom",
		Address:    "Moskva, testovaia, 2",
		WebhookURL: hookURL,
	})
	if err != nil {
		t.Fatalf("register restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM restaurants WHERE id = $1`, rest.ID)
	})

	if rest.WebhookURL != hookURL {
		t.Errorf("registration must echo the webhook url, got %q", rest.WebhookURL)
	}
	if rest.APIKey == "" {
		t.Fatal("registration must issue an api key")
	}

	assertClean := func(label string, got *domain.Restaurant) {
		t.Helper()
		if got == nil {
			t.Fatalf("%s: no restaurant returned", label)
		}
		if got.WebhookURL != "" {
			t.Errorf("%s: webhook_url must not be selected, got %q", label, got.WebhookURL)
		}
		if got.APIKey != "" {
			t.Errorf("%s: api_key must not be selected, got %q", label, got.APIKey)
		}
	}

	byID, err := e.restSvc.GetByID(ctx, rest.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	assertClean("GetByID", byID)

	byKey, err := e.restSvc.GetByAPIKey(ctx, rest.APIKey)
	if err != nil {
		t.Fatalf("get by api key: %v", err)
	}
	assertClean("GetByAPIKey", byKey)

	listed, err := e.restSvc.List(ctx, domain.RestaurantFilter{Search: "Zavedenie s vebkhukom", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected the freshly registered establishment in the catalogue, got %d", len(listed))
	}
	assertClean("List", &listed[0])

	newAddress := "Moskva, testovaia, 3"
	updated, err := e.restRepo.Update(ctx, rest.ID, domain.UpdateRestaurantInput{Address: &newAddress})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	assertClean("Update", updated)

	url, err := e.restRepo.GetWebhookURL(ctx, rest.ID)
	if err != nil {
		t.Fatalf("get webhook url: %v", err)
	}
	if url != hookURL {
		t.Errorf("the dispatcher must still get the address, got %q", url)
	}
}

// TestPostgres_TextLengthConstraintsMatchValidation: the CHECK constraints added with the text
// limits are a second line of defence behind domain validation, and they are only useful if the two
// agree exactly. A schema stricter than the validator would be worse than no constraint at all: a
// value the API accepted would fail on write, turning a client mistake into a 500.
func TestPostgres_TextLengthConstraintsMatchValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rest := e.newRestaurant(t, "Granitsy teksta")
	dish := e.newDish(t, rest.ID, "Bliudo s opisaniem", 10000, 5)

	atLimit := strings.Repeat("a", domain.MaxFreeTextLen)
	overLimit := strings.Repeat("a", domain.MaxFreeTextLen+1)

	if _, err := e.menuRepo.UpdateItem(ctx, dish.ID, domain.UpdateMenuItemInput{Description: &atLimit}); err != nil {
		t.Errorf("a description exactly at the limit must be storable, got %v", err)
	}

	order, err := e.orderSvc.CreateOrder(ctx, domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    rest.ID,
		DeliveryAddress: "Moskva, Arbat, 5",
		Comment:         atLimit,
		Items:           []domain.CreateOrderItemInput{{MenuItemID: dish.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("an order comment exactly at the limit must be storable, got %v", err)
	}

	guarded := []struct {
		name  string
		query string
		id    uuid.UUID
	}{
		{"restaurants.description", `UPDATE restaurants SET description = $1 WHERE id = $2`, rest.ID},
		{"menu_items.description", `UPDATE menu_items SET description = $1 WHERE id = $2`, dish.ID},
		{"orders.comment", `UPDATE orders SET comment = $1 WHERE id = $2`, order.ID},
		{"order_status_history.comment", `UPDATE order_status_history SET comment = $1 WHERE order_id = $2`, order.ID},
	}

	for _, g := range guarded {
		if _, err := e.pool.Exec(ctx, g.query, overLimit, g.id); err == nil {
			t.Errorf("%s: the schema accepted a value over the limit", g.name)
		}
		if _, err := e.pool.Exec(ctx, g.query, atLimit, g.id); err != nil {
			t.Errorf("%s: the schema refused a value the validator accepts: %v", g.name, err)
		}
	}
}

// TestPostgres_MenuAndOrderQueriesAgainstRealSQL covers the read and write paths that the handler
// suite can only reach through doubles: listing categories and dishes, patching and deleting them,
// filtering the order queue, and returning stock on cancellation. The doubles answer all of these
// from maps, so only real SQL shows whether the queries, the joins and the foreign keys behave.
func TestPostgres_MenuAndOrderQueriesAgainstRealSQL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rest := e.newRestaurant(t, "Meniu na zhivom SQL")
	sold := e.newDish(t, rest.ID, "Plov", 42000, 5)
	spare := e.newDish(t, rest.ID, "Kompot", 9000, 3)

	cats, err := e.menuRepo.ListCategoriesByRestaurant(ctx, rest.ID)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(cats) != 2 {
		t.Errorf("expected both categories, got %d", len(cats))
	}

	items, err := e.menuRepo.ListItemsByRestaurant(ctx, rest.ID, true)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected both dishes, got %d", len(items))
	}

	renamed := "Goriachee"
	patched, err := e.menuRepo.UpdateCategory(ctx, sold.CategoryID, domain.UpdateMenuCategoryInput{Name: &renamed})
	if err != nil {
		t.Fatalf("update category: %v", err)
	}
	if patched.Name != renamed {
		t.Errorf("category was not renamed, got %q", patched.Name)
	}

	order, err := e.orderSvc.CreateOrder(ctx, domain.CreateOrderInput{
		UserID:          uuid.New(),
		RestaurantID:    rest.ID,
		DeliveryAddress: "Moskva, Arbat, 5",
		Items:           []domain.CreateOrderItemInput{{MenuItemID: sold.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	created := domain.OrderStatusCreated
	queue, err := e.orderRepo.ListOrders(ctx, domain.OrderFilter{RestaurantID: &rest.ID, Status: &created, Limit: 10})
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	if len(queue) != 1 || queue[0].ID != order.ID {
		t.Fatalf("the queue must hold exactly the fresh order, got %+v", queue)
	}

	ready := domain.OrderStatusReady
	if other, err := e.orderRepo.ListOrders(ctx, domain.OrderFilter{RestaurantID: &rest.ID, Status: &ready, Limit: 10}); err != nil {
		t.Fatalf("list orders by status: %v", err)
	} else if len(other) != 0 {
		t.Errorf("the status filter let through an order in another status: %+v", other)
	}

	if got := e.stockOf(t, sold.ID); got != 3 {
		t.Fatalf("checkout should have reserved 2 of 5, stock is %d", got)
	}
	if _, err := e.orderSvc.CancelOrderByCustomer(ctx, order.ID, order.UserID, "ne segodnia"); err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if got := e.stockOf(t, sold.ID); got != 5 {
		t.Errorf("cancellation must return the stock, got %d", got)
	}

	if err := e.menuRepo.DeleteItem(ctx, spare.ID); err != nil {
		t.Fatalf("delete dish: %v", err)
	}
	if err := e.menuRepo.DeleteCategory(ctx, spare.CategoryID); err != nil {
		t.Fatalf("delete category: %v", err)
	}

	left, err := e.menuRepo.ListItemsByRestaurant(ctx, rest.ID, false)
	if err != nil {
		t.Fatalf("list items after deletion: %v", err)
	}
	if len(left) != 1 || left[0].ID != sold.ID {
		t.Errorf("only the sold dish should remain, got %+v", left)
	}

	if err := e.menuRepo.DeleteItem(ctx, sold.ID); err == nil {
		t.Error("a dish that appears in an order must not be deletable")
	}
}
