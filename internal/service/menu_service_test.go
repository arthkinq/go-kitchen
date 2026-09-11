package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/repository/inmem"
)

type menuFixture struct {
	svc      *MenuService
	restRepo *inmem.RestaurantRepository
	menuRepo *inmem.MenuRepository
	restID   uuid.UUID
}

func newMenuFixture() *menuFixture {
	restRepo := inmem.NewRestaurantRepository()
	menuRepo := inmem.NewMenuRepository()

	restID := uuid.New()
	restRepo.Put(&domain.Restaurant{ID: restID, Name: "Kafe", IsActive: true})

	return &menuFixture{
		svc:      NewMenuService(menuRepo, restRepo),
		restRepo: restRepo,
		menuRepo: menuRepo,
		restID:   restID,
	}
}

func TestMenuService_GetFullMenu_GroupsDishesByCategory(t *testing.T) {
	f := newMenuFixture()

	burgers := &domain.MenuCategory{ID: uuid.New(), RestaurantID: f.restID, Name: "Burgers", SortOrder: 1}
	drinks := &domain.MenuCategory{ID: uuid.New(), RestaurantID: f.restID, Name: "Drinks", SortOrder: 2}
	f.menuRepo.PutCategory(burgers)
	f.menuRepo.PutCategory(drinks)

	f.menuRepo.PutItem(&domain.MenuItem{
		ID: uuid.New(), RestaurantID: f.restID, CategoryID: burgers.ID,
		Name: "Cheeseburger", PriceCents: 35000, IsAvailable: true,
	})
	f.menuRepo.PutItem(&domain.MenuItem{
		ID: uuid.New(), RestaurantID: f.restID, CategoryID: drinks.ID,
		Name: "Cola", PriceCents: 12000, IsAvailable: true,
	})
	f.menuRepo.PutItem(&domain.MenuItem{
		ID: uuid.New(), RestaurantID: f.restID, CategoryID: burgers.ID,
		Name: "Trufel'nyi", PriceCents: 90000, IsAvailable: false,
	})

	menu, err := f.svc.GetFullMenu(context.Background(), f.restID, true)
	if err != nil {
		t.Fatalf("GetFullMenu: %v", err)
	}
	if len(menu) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(menu))
	}

	byName := make(map[string][]domain.MenuItem, len(menu))
	for _, c := range menu {
		byName[c.Name] = c.Items
	}
	if got := len(byName["Burgers"]); got != 1 {
		t.Errorf("expected 1 available burger, got %d", got)
	}
	if got := len(byName["Drinks"]); got != 1 {
		t.Errorf("expected 1 drink, got %d", got)
	}
}

// TestMenuService_GetFullMenu_UnknownRestaurant guards against reporting an empty menu
// for an establishment that does not exist.
func TestMenuService_GetFullMenu_UnknownRestaurant(t *testing.T) {
	f := newMenuFixture()

	_, err := f.svc.GetFullMenu(context.Background(), uuid.New(), true)
	if !errors.Is(err, domain.ErrRestaurantNotFound) {
		t.Fatalf("expected ErrRestaurantNotFound, got %v", err)
	}
}

// TestMenuService_CreateItem_HidesCategoryOfAnotherRestaurant: a category that exists but belongs
// to a competitor must be indistinguishable from one that does not exist at all. Two different
// answers here would turn the endpoint into a way of enumerating someone else's menu by UUID.
func TestMenuService_CreateItem_HidesCategoryOfAnotherRestaurant(t *testing.T) {
	f := newMenuFixture()

	otherID := uuid.New()
	f.restRepo.Put(&domain.Restaurant{ID: otherID, Name: "Drugoe kafe", IsActive: true})
	foreignCat := &domain.MenuCategory{ID: uuid.New(), RestaurantID: otherID, Name: "Chuzhaia kategoriia"}
	f.menuRepo.PutCategory(foreignCat)

	newItem := func(categoryID uuid.UUID) domain.CreateMenuItemInput {
		return domain.CreateMenuItemInput{
			RestaurantID: f.restID,
			CategoryID:   categoryID,
			Name:         "Bliudo",
			PriceCents:   1000,
			IsAvailable:  true,
		}
	}

	_, foreignErr := f.svc.CreateItem(context.Background(), newItem(foreignCat.ID))
	if !errors.Is(foreignErr, domain.ErrCategoryNotFound) {
		t.Fatalf("expected ErrCategoryNotFound for a foreign category, got %v", foreignErr)
	}

	_, unknownErr := f.svc.CreateItem(context.Background(), newItem(uuid.New()))
	if !errors.Is(unknownErr, domain.ErrCategoryNotFound) {
		t.Fatalf("expected ErrCategoryNotFound for an unknown category, got %v", unknownErr)
	}
}

// TestMenuService_OwnershipIsEnforced is the regression test for the closed partner area:
// one establishment must not be able to edit or delete another one's menu.
func TestMenuService_OwnershipIsEnforced(t *testing.T) {
	f := newMenuFixture()

	cat := &domain.MenuCategory{ID: uuid.New(), RestaurantID: f.restID, Name: "Pitstsa"}
	f.menuRepo.PutCategory(cat)
	item := &domain.MenuItem{
		ID: uuid.New(), RestaurantID: f.restID, CategoryID: cat.ID,
		Name: "Margarita", PriceCents: 50000, StockQuantity: 5, IsAvailable: true,
	}
	f.menuRepo.PutItem(item)

	intruder := uuid.New()
	f.restRepo.Put(&domain.Restaurant{ID: intruder, Name: "Konkurent", IsActive: true})

	ctx := context.Background()
	newPrice := int64(1)

	if _, err := f.svc.UpdateItem(ctx, intruder, item.ID, domain.UpdateMenuItemInput{PriceCents: &newPrice}); !errors.Is(err, domain.ErrMenuItemNotFound) {
		t.Errorf("UpdateItem by an intruder: expected ErrMenuItemNotFound, got %v", err)
	}
	if err := f.svc.DeleteItem(ctx, intruder, item.ID); !errors.Is(err, domain.ErrMenuItemNotFound) {
		t.Errorf("DeleteItem by an intruder: expected ErrMenuItemNotFound, got %v", err)
	}
	if _, err := f.svc.UpdateCategory(ctx, intruder, cat.ID, domain.UpdateMenuCategoryInput{}); !errors.Is(err, domain.ErrCategoryNotFound) {
		t.Errorf("UpdateCategory by an intruder: expected ErrCategoryNotFound, got %v", err)
	}
	if err := f.svc.DeleteCategory(ctx, intruder, cat.ID); !errors.Is(err, domain.ErrCategoryNotFound) {
		t.Errorf("DeleteCategory by an intruder: expected ErrCategoryNotFound, got %v", err)
	}

	stored, err := f.menuRepo.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("dish must still exist: %v", err)
	}
	if stored.PriceCents != 50000 {
		t.Errorf("price must be untouched, got %d", stored.PriceCents)
	}

	if _, err := f.svc.UpdateItem(ctx, f.restID, item.ID, domain.UpdateMenuItemInput{PriceCents: &newPrice}); err != nil {
		t.Errorf("the owner must be able to edit its own dish: %v", err)
	}
}
