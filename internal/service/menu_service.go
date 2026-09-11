package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

// MenuService handles business logic for menu categories and dishes.
//
// Every partner-facing method takes the identifier of the authenticated establishment and refuses
// to touch data owned by anyone else. A foreign resource is reported as not found rather than
// forbidden, so the API cannot be used to enumerate a competitor's menu.
type MenuService struct {
	menuRepo domain.MenuRepository
	restRepo domain.RestaurantRepository
}

// NewMenuService creates a new MenuService instance.
func NewMenuService(menuRepo domain.MenuRepository, restRepo domain.RestaurantRepository) *MenuService {
	return &MenuService{
		menuRepo: menuRepo,
		restRepo: restRepo,
	}
}

// SearchItems finds orderable dishes across the platform. Bounds on the search pattern and on
// pagination are enforced at the HTTP edge, the same way the catalogue does it.
func (s *MenuService) SearchItems(ctx context.Context, filter domain.MenuItemSearchFilter) ([]domain.MenuItemSearchResult, error) {
	return s.menuRepo.SearchItems(ctx, filter)
}

// CreateCategory adds a category to a restaurant's menu.
func (s *MenuService) CreateCategory(ctx context.Context, in domain.CreateMenuCategoryInput) (*domain.MenuCategory, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	if _, err := s.restRepo.GetByID(ctx, in.RestaurantID); err != nil {
		return nil, err
	}

	cat := &domain.MenuCategory{
		RestaurantID: in.RestaurantID,
		Name:         in.Name,
		SortOrder:    in.SortOrder,
	}

	if err := s.menuRepo.CreateCategory(ctx, cat); err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}

	return cat, nil
}

// UpdateCategory updates a category owned by the given restaurant.
func (s *MenuService) UpdateCategory(
	ctx context.Context,
	restaurantID, categoryID uuid.UUID,
	in domain.UpdateMenuCategoryInput,
) (*domain.MenuCategory, error) {
	if err := s.assertCategoryOwned(ctx, restaurantID, categoryID); err != nil {
		return nil, err
	}
	return s.menuRepo.UpdateCategory(ctx, categoryID, in)
}

// DeleteCategory removes a category owned by the given restaurant.
func (s *MenuService) DeleteCategory(ctx context.Context, restaurantID, categoryID uuid.UUID) error {
	if err := s.assertCategoryOwned(ctx, restaurantID, categoryID); err != nil {
		return err
	}
	return s.menuRepo.DeleteCategory(ctx, categoryID)
}

// CreateItem adds a dish to the restaurant's menu.
func (s *MenuService) CreateItem(ctx context.Context, in domain.CreateMenuItemInput) (*domain.MenuItem, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	if _, err := s.restRepo.GetByID(ctx, in.RestaurantID); err != nil {
		return nil, err
	}

	cat, err := s.menuRepo.GetCategoryByID(ctx, in.CategoryID)
	if err != nil {
		return nil, err
	}
	if cat.RestaurantID != in.RestaurantID {
		return nil, domain.ErrCategoryNotFound
	}

	item := &domain.MenuItem{
		RestaurantID:  in.RestaurantID,
		CategoryID:    in.CategoryID,
		Name:          in.Name,
		Description:   in.Description,
		PriceCents:    in.PriceCents,
		StockQuantity: in.StockQuantity,
		IsAvailable:   in.IsAvailable,
	}

	if err := s.menuRepo.CreateItem(ctx, item); err != nil {
		return nil, fmt.Errorf("create menu item: %w", err)
	}

	return item, nil
}

// GetFullMenu returns categories with their dishes nested inside.
// An unknown restaurant yields ErrRestaurantNotFound rather than an empty menu.
func (s *MenuService) GetFullMenu(ctx context.Context, restaurantID uuid.UUID, onlyAvailable bool) ([]domain.MenuCategory, error) {
	if restaurantID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}

	if _, err := s.restRepo.GetByID(ctx, restaurantID); err != nil {
		return nil, err
	}

	categories, err := s.menuRepo.ListCategoriesByRestaurant(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	items, err := s.menuRepo.ListItemsByRestaurant(ctx, restaurantID, onlyAvailable)
	if err != nil {
		return nil, err
	}

	categoryIndex := make(map[uuid.UUID]int, len(categories))
	for i := range categories {
		categories[i].Items = make([]domain.MenuItem, 0)
		categoryIndex[categories[i].ID] = i
	}

	for i := range items {
		if idx, ok := categoryIndex[items[i].CategoryID]; ok {
			categories[idx].Items = append(categories[idx].Items, items[i])
		}
	}

	return categories, nil
}

// UpdateItem updates a dish owned by the given restaurant (price, availability, stock, description).
func (s *MenuService) UpdateItem(
	ctx context.Context,
	restaurantID, itemID uuid.UUID,
	in domain.UpdateMenuItemInput,
) (*domain.MenuItem, error) {
	if err := s.assertItemOwned(ctx, restaurantID, itemID); err != nil {
		return nil, err
	}
	if in.CategoryID != nil {
		if err := s.assertCategoryOwned(ctx, restaurantID, *in.CategoryID); err != nil {
			return nil, err
		}
	}
	return s.menuRepo.UpdateItem(ctx, itemID, in)
}

// DeleteItem removes a dish owned by the given restaurant.
func (s *MenuService) DeleteItem(ctx context.Context, restaurantID, itemID uuid.UUID) error {
	if err := s.assertItemOwned(ctx, restaurantID, itemID); err != nil {
		return err
	}
	return s.menuRepo.DeleteItem(ctx, itemID)
}

func (s *MenuService) assertCategoryOwned(ctx context.Context, restaurantID, categoryID uuid.UUID) error {
	if restaurantID == uuid.Nil || categoryID == uuid.Nil {
		return domain.ErrInvalidInput
	}
	cat, err := s.menuRepo.GetCategoryByID(ctx, categoryID)
	if err != nil {
		return err
	}
	if cat.RestaurantID != restaurantID {
		return domain.ErrCategoryNotFound
	}
	return nil
}

func (s *MenuService) assertItemOwned(ctx context.Context, restaurantID, itemID uuid.UUID) error {
	if restaurantID == uuid.Nil || itemID == uuid.Nil {
		return domain.ErrInvalidInput
	}
	item, err := s.menuRepo.GetItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	if item.RestaurantID != restaurantID {
		return domain.ErrMenuItemNotFound
	}
	return nil
}
