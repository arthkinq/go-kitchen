package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

// MenuRepository implements domain.MenuRepository using PostgreSQL.
type MenuRepository struct {
	pool *pgxpool.Pool
}

// NewMenuRepository constructs a new MenuRepository.
func NewMenuRepository(pool *pgxpool.Pool) *MenuRepository {
	return &MenuRepository{pool: pool}
}

// CreateCategory inserts a new menu category.
func (r *MenuRepository) CreateCategory(ctx context.Context, cat *domain.MenuCategory) error {
	exec := getExecutor(ctx, r.pool)

	id, err := newID()
	if err != nil {
		return err
	}

	query := `
		INSERT INTO menu_categories (id, restaurant_id, name, sort_order)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at;
	`

	err = exec.QueryRow(ctx, query,
		id,
		cat.RestaurantID,
		cat.Name,
		cat.SortOrder,
	).Scan(&cat.ID, &cat.CreatedAt, &cat.UpdatedAt)
	if err != nil {
		return mapPgError(fmt.Errorf("insert menu category: %w", err))
	}

	return nil
}

// GetCategoryByID fetches a category by ID.
func (r *MenuRepository) GetCategoryByID(ctx context.Context, id uuid.UUID) (*domain.MenuCategory, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, restaurant_id, name, sort_order, created_at, updated_at
		FROM menu_categories
		WHERE id = $1;
	`

	var cat domain.MenuCategory
	err := exec.QueryRow(ctx, query, id).Scan(
		&cat.ID,
		&cat.RestaurantID,
		&cat.Name,
		&cat.SortOrder,
		&cat.CreatedAt,
		&cat.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrCategoryNotFound
		}
		return nil, fmt.Errorf("select category by id: %w", err)
	}

	return &cat, nil
}

// ListCategoriesByRestaurant returns all categories of a restaurant ordered by sort_order.
func (r *MenuRepository) ListCategoriesByRestaurant(ctx context.Context, restaurantID uuid.UUID) ([]domain.MenuCategory, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, restaurant_id, name, sort_order, created_at, updated_at
		FROM menu_categories
		WHERE restaurant_id = $1
		ORDER BY sort_order ASC, name ASC;
	`

	rows, err := exec.Query(ctx, query, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()

	categories := make([]domain.MenuCategory, 0)
	for rows.Next() {
		var cat domain.MenuCategory
		if err := rows.Scan(
			&cat.ID,
			&cat.RestaurantID,
			&cat.Name,
			&cat.SortOrder,
			&cat.CreatedAt,
			&cat.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		categories = append(categories, cat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("categories rows err: %w", err)
	}

	return categories, nil
}

// UpdateCategory updates category attributes.
func (r *MenuRepository) UpdateCategory(ctx context.Context, id uuid.UUID, in domain.UpdateMenuCategoryInput) (*domain.MenuCategory, error) {
	exec := getExecutor(ctx, r.pool)

	setClauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	addSet := func(column string, value any) {
		args = append(args, value)
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if in.Name != nil {
		addSet("name", *in.Name)
	}
	if in.SortOrder != nil {
		addSet("sort_order", *in.SortOrder)
	}

	if len(setClauses) == 0 {
		return r.GetCategoryByID(ctx, id)
	}

	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE menu_categories
		SET %s
		WHERE id = $%d
		RETURNING id, restaurant_id, name, sort_order, created_at, updated_at;
	`, strings.Join(setClauses, ", "), len(args))

	var cat domain.MenuCategory
	err := exec.QueryRow(ctx, query, args...).Scan(
		&cat.ID,
		&cat.RestaurantID,
		&cat.Name,
		&cat.SortOrder,
		&cat.CreatedAt,
		&cat.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrCategoryNotFound
		}
		return nil, mapPgError(fmt.Errorf("update category: %w", err))
	}

	return &cat, nil
}

// DeleteCategory removes a menu category.
func (r *MenuRepository) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	exec := getExecutor(ctx, r.pool)

	cmd, err := exec.Exec(ctx, `DELETE FROM menu_categories WHERE id = $1;`, id)
	if err != nil {
		return mapPgError(fmt.Errorf("delete category: %w", err))
	}
	if cmd.RowsAffected() == 0 {
		return domain.ErrCategoryNotFound
	}
	return nil
}

// CreateItem inserts a new menu item.
func (r *MenuRepository) CreateItem(ctx context.Context, item *domain.MenuItem) error {
	exec := getExecutor(ctx, r.pool)

	id, err := newID()
	if err != nil {
		return err
	}

	query := `
		INSERT INTO menu_items (id, restaurant_id, category_id, name, description, price_cents, stock_quantity, is_available)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at;
	`

	err = exec.QueryRow(ctx, query,
		id,
		item.RestaurantID,
		item.CategoryID,
		item.Name,
		item.Description,
		item.PriceCents,
		item.StockQuantity,
		item.IsAvailable,
	).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return mapPgError(fmt.Errorf("insert menu item: %w", err))
	}

	return nil
}

// GetItemByID fetches a menu item by ID.
func (r *MenuRepository) GetItemByID(ctx context.Context, id uuid.UUID) (*domain.MenuItem, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, restaurant_id, category_id, name, description, price_cents, stock_quantity, is_available, created_at, updated_at
		FROM menu_items
		WHERE id = $1;
	`

	var item domain.MenuItem
	err := exec.QueryRow(ctx, query, id).Scan(
		&item.ID,
		&item.RestaurantID,
		&item.CategoryID,
		&item.Name,
		&item.Description,
		&item.PriceCents,
		&item.StockQuantity,
		&item.IsAvailable,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrMenuItemNotFound
		}
		return nil, fmt.Errorf("select menu item by id: %w", err)
	}

	return &item, nil
}

// GetItemsByIDsForUpdate locks and retrieves menu items by their IDs, deterministically sorted by ID to prevent deadlocks.
func (r *MenuRepository) GetItemsByIDsForUpdate(ctx context.Context, ids []uuid.UUID) ([]domain.MenuItem, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, restaurant_id, category_id, name, description, price_cents, stock_quantity, is_available, created_at, updated_at
		FROM menu_items
		WHERE id = ANY($1)
		ORDER BY id ASC
		FOR UPDATE;
	`

	rows, err := exec.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("query items for update: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MenuItem, 0, len(ids))
	for rows.Next() {
		var item domain.MenuItem
		if err := rows.Scan(
			&item.ID,
			&item.RestaurantID,
			&item.CategoryID,
			&item.Name,
			&item.Description,
			&item.PriceCents,
			&item.StockQuantity,
			&item.IsAvailable,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan item for update: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("items for update rows err: %w", err)
	}

	return items, nil
}

// ListItemsByRestaurant returns dishes for a restaurant.
func (r *MenuRepository) ListItemsByRestaurant(ctx context.Context, restaurantID uuid.UUID, onlyAvailable bool) ([]domain.MenuItem, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, restaurant_id, category_id, name, description, price_cents, stock_quantity, is_available, created_at, updated_at
		FROM menu_items
		WHERE restaurant_id = $1
		ORDER BY name ASC;
	`
	if onlyAvailable {
		query = `
			SELECT id, restaurant_id, category_id, name, description, price_cents, stock_quantity, is_available, created_at, updated_at
			FROM menu_items
			WHERE restaurant_id = $1 AND is_available = true
			ORDER BY name ASC;
		`
	}

	rows, err := exec.Query(ctx, query, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("query menu items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MenuItem, 0)
	for rows.Next() {
		var item domain.MenuItem
		if err := rows.Scan(
			&item.ID,
			&item.RestaurantID,
			&item.CategoryID,
			&item.Name,
			&item.Description,
			&item.PriceCents,
			&item.StockQuantity,
			&item.IsAvailable,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("items rows err: %w", err)
	}

	return items, nil
}

// SearchItems finds dishes by name or description across every establishment on the platform.
//
// Only what a customer could actually order is returned - an available dish of an active
// establishment. That predicate is part of the query rather than a filter flag because there is no
// caller for whom the opposite would make sense: a person looking for something to eat has no use
// for a dish nobody can sell them.
//
// The pattern goes through likePattern, so `%` and `_` typed into a search box stay ordinary
// characters. The sort adds id as a tiebreaker: the platform can easily hold two dishes with the
// same name, and without it a row could repeat or vanish between two pages.
func (r *MenuRepository) SearchItems(ctx context.Context, filter domain.MenuItemSearchFilter) ([]domain.MenuItemSearchResult, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT mi.id, mi.restaurant_id, mi.category_id, mi.name, mi.description,
		       mi.price_cents, mi.stock_quantity, mi.is_available, mi.created_at, mi.updated_at,
		       r.name
		FROM menu_items mi
		JOIN restaurants r ON r.id = mi.restaurant_id
		WHERE mi.is_available = true
		  AND r.is_active = true
		  AND (mi.name ILIKE $1 ESCAPE '\' OR mi.description ILIKE $1 ESCAPE '\')
		ORDER BY mi.name ASC, mi.id ASC
		LIMIT $2 OFFSET $3;
	`

	rows, err := exec.Query(ctx, query, likePattern(filter.Search), filter.Limit, filter.Offset)
	if err != nil {
		return nil, fmt.Errorf("search menu items: %w", err)
	}
	defer rows.Close()

	results := make([]domain.MenuItemSearchResult, 0)
	for rows.Next() {
		var res domain.MenuItemSearchResult
		if err := rows.Scan(
			&res.ID,
			&res.RestaurantID,
			&res.CategoryID,
			&res.Name,
			&res.Description,
			&res.PriceCents,
			&res.StockQuantity,
			&res.IsAvailable,
			&res.CreatedAt,
			&res.UpdatedAt,
			&res.RestaurantName,
		); err != nil {
			return nil, fmt.Errorf("scan menu item search result: %w", err)
		}
		results = append(results, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search rows err: %w", err)
	}

	return results, nil
}

// UpdateItem updates menu item attributes.
func (r *MenuRepository) UpdateItem(ctx context.Context, id uuid.UUID, in domain.UpdateMenuItemInput) (*domain.MenuItem, error) {
	exec := getExecutor(ctx, r.pool)

	setClauses := make([]string, 0, 7)
	args := make([]any, 0, 7)
	addSet := func(column string, value any) {
		args = append(args, value)
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if in.CategoryID != nil {
		addSet("category_id", *in.CategoryID)
	}
	if in.Name != nil {
		addSet("name", *in.Name)
	}
	if in.Description != nil {
		addSet("description", *in.Description)
	}
	if in.PriceCents != nil {
		addSet("price_cents", *in.PriceCents)
	}
	if in.StockQuantity != nil {
		addSet("stock_quantity", *in.StockQuantity)
	}
	if in.IsAvailable != nil {
		addSet("is_available", *in.IsAvailable)
	}

	if len(setClauses) == 0 {
		return r.GetItemByID(ctx, id)
	}

	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE menu_items
		SET %s
		WHERE id = $%d
		RETURNING id, restaurant_id, category_id, name, description, price_cents, stock_quantity, is_available, created_at, updated_at;
	`, strings.Join(setClauses, ", "), len(args))

	var item domain.MenuItem
	err := exec.QueryRow(ctx, query, args...).Scan(
		&item.ID,
		&item.RestaurantID,
		&item.CategoryID,
		&item.Name,
		&item.Description,
		&item.PriceCents,
		&item.StockQuantity,
		&item.IsAvailable,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrMenuItemNotFound
		}
		return nil, mapPgError(fmt.Errorf("update menu item: %w", err))
	}

	return &item, nil
}

// DeleteItem removes a menu item.
func (r *MenuRepository) DeleteItem(ctx context.Context, id uuid.UUID) error {
	exec := getExecutor(ctx, r.pool)

	cmd, err := exec.Exec(ctx, `DELETE FROM menu_items WHERE id = $1;`, id)
	if err != nil {
		return mapPgError(fmt.Errorf("delete menu item: %w", err))
	}
	if cmd.RowsAffected() == 0 {
		return domain.ErrMenuItemNotFound
	}
	return nil
}

// DecrementStock atomically decreases stock quantity for an item.
func (r *MenuRepository) DecrementStock(ctx context.Context, itemID uuid.UUID, quantity int) error {
	exec := getExecutor(ctx, r.pool)

	query := `
		UPDATE menu_items
		SET stock_quantity = stock_quantity - $2, updated_at = NOW()
		WHERE id = $1 AND stock_quantity >= $2 AND is_available = true;
	`

	cmd, err := exec.Exec(ctx, query, itemID, quantity)
	if err != nil {
		return mapPgError(fmt.Errorf("decrement stock: %w", err))
	}
	if cmd.RowsAffected() == 0 {
		return domain.ErrOutOfStock
	}

	return nil
}

// IncrementStock returns stock back to inventory (used on order cancellation).
func (r *MenuRepository) IncrementStock(ctx context.Context, itemID uuid.UUID, quantity int) error {
	exec := getExecutor(ctx, r.pool)

	query := `
		UPDATE menu_items
		SET stock_quantity = stock_quantity + $2, updated_at = NOW()
		WHERE id = $1;
	`

	cmd, err := exec.Exec(ctx, query, itemID, quantity)
	if err != nil {
		return mapPgError(fmt.Errorf("increment stock: %w", err))
	}
	if cmd.RowsAffected() == 0 {
		return domain.ErrMenuItemNotFound
	}

	return nil
}
