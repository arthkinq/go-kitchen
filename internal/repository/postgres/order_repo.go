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

// OrderRepository implements domain.OrderRepository using PostgreSQL.
type OrderRepository struct {
	pool *pgxpool.Pool
}

// NewOrderRepository constructs a new OrderRepository.
func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

// CreateOrder inserts order, snapshots of items, and the initial status history.
func (r *OrderRepository) CreateOrder(ctx context.Context, o *domain.Order) error {
	exec := getExecutor(ctx, r.pool)

	id, err := newID()
	if err != nil {
		return err
	}

	queryOrder := `
		INSERT INTO orders (id, user_id, restaurant_id, status, total_price_cents, delivery_address, comment)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at;
	`

	err = exec.QueryRow(ctx, queryOrder,
		id,
		o.UserID,
		o.RestaurantID,
		o.Status,
		o.TotalPriceCents,
		o.DeliveryAddress,
		o.Comment,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return mapPgError(fmt.Errorf("insert order: %w", err))
	}

	if err := r.insertOrderItems(ctx, exec, o); err != nil {
		return err
	}

	history := &domain.OrderStatusHistory{
		OrderID:    o.ID,
		FromStatus: "",
		ToStatus:   o.Status,
		Actor:      domain.ActorSystem,
		Comment:    "Заказ создан",
		CreatedAt:  o.CreatedAt,
	}

	if err := r.AddStatusHistory(ctx, history); err != nil {
		return fmt.Errorf("insert initial status history: %w", err)
	}
	o.StatusHistory = []domain.OrderStatusHistory{*history}

	return nil
}

// GetOrderByID fetches an order with all items and status history.
func (r *OrderRepository) GetOrderByID(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, user_id, restaurant_id, status, total_price_cents, delivery_address, comment, created_at, updated_at
		FROM orders
		WHERE id = $1;
	`

	var o domain.Order
	err := exec.QueryRow(ctx, query, id).Scan(
		&o.ID,
		&o.UserID,
		&o.RestaurantID,
		&o.Status,
		&o.TotalPriceCents,
		&o.DeliveryAddress,
		&o.Comment,
		&o.CreatedAt,
		&o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("select order by id: %w", err)
	}

	items, err := r.getOrderItems(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	o.Items = items

	history, err := r.getOrderHistory(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	o.StatusHistory = history

	return &o, nil
}

// GetOrderForUpdate locks an order row for update within a transaction.
func (r *OrderRepository) GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, user_id, restaurant_id, status, total_price_cents, delivery_address, comment, created_at, updated_at
		FROM orders
		WHERE id = $1
		FOR UPDATE;
	`

	var o domain.Order
	err := exec.QueryRow(ctx, query, id).Scan(
		&o.ID,
		&o.UserID,
		&o.RestaurantID,
		&o.Status,
		&o.TotalPriceCents,
		&o.DeliveryAddress,
		&o.Comment,
		&o.CreatedAt,
		&o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("select order for update: %w", err)
	}

	items, err := r.getOrderItems(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	o.Items = items

	return &o, nil
}

// ListOrders fetches orders according to filter and batch-loads order items in a single query (O(1) queries).
func (r *OrderRepository) ListOrders(ctx context.Context, filter domain.OrderFilter) ([]domain.Order, error) {
	exec := getExecutor(ctx, r.pool)

	var sb strings.Builder
	sb.WriteString(`
		SELECT id, user_id, restaurant_id, status, total_price_cents, delivery_address, comment, created_at, updated_at
		FROM orders
		WHERE 1=1
	`)

	args := make([]any, 0, 5)

	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		fmt.Fprintf(&sb, " AND user_id = $%d", len(args))
	}
	if filter.RestaurantID != nil {
		args = append(args, *filter.RestaurantID)
		fmt.Fprintf(&sb, " AND restaurant_id = $%d", len(args))
	}
	if filter.Status != nil {
		args = append(args, string(*filter.Status))
		fmt.Fprintf(&sb, " AND status = $%d", len(args))
	}

	sb.WriteString(" ORDER BY created_at DESC")

	args = append(args, filter.Limit)
	fmt.Fprintf(&sb, " LIMIT $%d", len(args))

	args = append(args, filter.Offset)
	fmt.Fprintf(&sb, " OFFSET $%d", len(args))

	rows, err := exec.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()

	orders := make([]domain.Order, 0)
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(
			&o.ID,
			&o.UserID,
			&o.RestaurantID,
			&o.Status,
			&o.TotalPriceCents,
			&o.DeliveryAddress,
			&o.Comment,
			&o.CreatedAt,
			&o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		orders = append(orders, o)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orders rows err: %w", err)
	}

	if len(orders) > 0 {
		orderIDs := make([]uuid.UUID, len(orders))
		orderIndexMap := make(map[uuid.UUID]int, len(orders))
		for i := range orders {
			orderIDs[i] = orders[i].ID
			orderIndexMap[orders[i].ID] = i
		}

		itemsQuery := `
			SELECT id, order_id, menu_item_id, item_name, price_cents, quantity
			FROM order_items
			WHERE order_id = ANY($1);
		`

		itemRows, err := exec.Query(ctx, itemsQuery, orderIDs)
		if err != nil {
			return nil, fmt.Errorf("batch query order items: %w", err)
		}
		defer itemRows.Close()

		for itemRows.Next() {
			var it domain.OrderItem
			if err := itemRows.Scan(
				&it.ID,
				&it.OrderID,
				&it.MenuItemID,
				&it.ItemName,
				&it.PriceCents,
				&it.Quantity,
			); err != nil {
				return nil, fmt.Errorf("scan batch order item: %w", err)
			}
			idx := orderIndexMap[it.OrderID]
			orders[idx].Items = append(orders[idx].Items, it)
		}

		if err := itemRows.Err(); err != nil {
			return nil, fmt.Errorf("batch order items rows err: %w", err)
		}
	}

	return orders, nil
}

// UpdateOrderStatus updates order status and timestamp.
func (r *OrderRepository) UpdateOrderStatus(ctx context.Context, id uuid.UUID, newStatus domain.OrderStatus, comment string) error {
	exec := getExecutor(ctx, r.pool)

	query := `
		UPDATE orders
		SET status = $1, updated_at = NOW()
		WHERE id = $2;
	`

	cmd, err := exec.Exec(ctx, query, string(newStatus), id)
	if err != nil {
		return fmt.Errorf("update order status: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// AddStatusHistory writes an entry to order_status_history.
func (r *OrderRepository) AddStatusHistory(ctx context.Context, h *domain.OrderStatusHistory) error {
	exec := getExecutor(ctx, r.pool)

	id, err := newID()
	if err != nil {
		return err
	}

	query := `
		INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, comment)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at;
	`

	err = exec.QueryRow(ctx, query,
		id,
		h.OrderID,
		string(h.FromStatus),
		string(h.ToStatus),
		string(h.Actor),
		h.Comment,
	).Scan(&h.ID, &h.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert order status history: %w", err)
	}

	return nil
}

func (r *OrderRepository) getOrderItems(ctx context.Context, orderID uuid.UUID) ([]domain.OrderItem, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, order_id, menu_item_id, item_name, price_cents, quantity
		FROM order_items
		WHERE order_id = $1
		ORDER BY menu_item_id ASC;
	`

	rows, err := exec.Query(ctx, query, orderID)
	if err != nil {
		return nil, fmt.Errorf("query order items: %w", err)
	}
	defer rows.Close()

	var items []domain.OrderItem
	for rows.Next() {
		var item domain.OrderItem
		if err := rows.Scan(
			&item.ID,
			&item.OrderID,
			&item.MenuItemID,
			&item.ItemName,
			&item.PriceCents,
			&item.Quantity,
		); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("order items rows err: %w", err)
	}

	return items, nil
}

func (r *OrderRepository) getOrderHistory(ctx context.Context, orderID uuid.UUID) ([]domain.OrderStatusHistory, error) {
	exec := getExecutor(ctx, r.pool)

	query := `
		SELECT id, order_id, from_status, to_status, actor, comment, created_at
		FROM order_status_history
		WHERE order_id = $1
		ORDER BY created_at ASC;
	`

	rows, err := exec.Query(ctx, query, orderID)
	if err != nil {
		return nil, fmt.Errorf("query order history: %w", err)
	}
	defer rows.Close()

	var history []domain.OrderStatusHistory
	for rows.Next() {
		var h domain.OrderStatusHistory
		if err := rows.Scan(
			&h.ID,
			&h.OrderID,
			&h.FromStatus,
			&h.ToStatus,
			&h.Actor,
			&h.Comment,
			&h.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order history: %w", err)
		}
		history = append(history, h)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("order history rows err: %w", err)
	}

	return history, nil
}

// insertOrderItems writes the whole receipt in a single round trip instead of one query per line.
func (r *OrderRepository) insertOrderItems(ctx context.Context, exec DBExecutor, o *domain.Order) error {
	if len(o.Items) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for i := range o.Items {
		item := &o.Items[i]
		item.OrderID = o.ID

		id, err := newID()
		if err != nil {
			return err
		}

		batch.Queue(`
			INSERT INTO order_items (id, order_id, menu_item_id, item_name, price_cents, quantity)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id;
		`, id, item.OrderID, item.MenuItemID, item.ItemName, item.PriceCents, item.Quantity)
	}

	results := exec.SendBatch(ctx, batch)
	defer func() {
		_ = results.Close()
	}()

	for i := range o.Items {
		if err := results.QueryRow().Scan(&o.Items[i].ID); err != nil {
			return mapPgError(fmt.Errorf("insert order item: %w", err))
		}
	}

	return nil
}
