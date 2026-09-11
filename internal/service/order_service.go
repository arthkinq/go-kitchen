package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

// OrderService orchestrates business workflows for customer orders, inventory reservations, and partner notifications.
type OrderService struct {
	orderRepo         domain.OrderRepository
	menuRepo          domain.MenuRepository
	restRepo          domain.RestaurantRepository
	txManager         domain.TxManager
	webhookDispatcher WebhookDispatcher
}

// NewOrderService constructs a new OrderService.
func NewOrderService(
	orderRepo domain.OrderRepository,
	menuRepo domain.MenuRepository,
	restRepo domain.RestaurantRepository,
	txManager domain.TxManager,
	webhookDispatcher WebhookDispatcher,
) *OrderService {
	return &OrderService{
		orderRepo:         orderRepo,
		menuRepo:          menuRepo,
		restRepo:          restRepo,
		txManager:         txManager,
		webhookDispatcher: webhookDispatcher,
	}
}

// CreateOrder handles atomic checkout: verifies the restaurant, locks the requested menu items,
// decrements stock, snapshots prices and persists the order - all inside one transaction.
func (s *OrderService) CreateOrder(ctx context.Context, in domain.CreateOrderInput) (*domain.Order, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	var (
		createdOrder domain.Order
		webhookURL   string
	)

	err := s.txManager.WithinTransaction(ctx, func(txCtx context.Context) error {
		rest, err := s.restRepo.GetByID(txCtx, in.RestaurantID)
		if err != nil {
			return err
		}
		if !rest.IsActive {
			return domain.ErrRestaurantInactive
		}

		webhookURL, err = s.restRepo.GetWebhookURL(txCtx, in.RestaurantID)
		if err != nil {
			return err
		}

		itemIDs := make([]uuid.UUID, len(in.Items))
		requestedQty := make(map[uuid.UUID]int, len(in.Items))
		for i, it := range in.Items {
			itemIDs[i] = it.MenuItemID
			requestedQty[it.MenuItemID] = it.Quantity
		}

		lockedItems, err := s.menuRepo.GetItemsByIDsForUpdate(txCtx, itemIDs)
		if err != nil {
			return fmt.Errorf("lock menu items: %w", err)
		}
		if len(lockedItems) != len(in.Items) {
			return domain.ErrMenuItemNotFound
		}

		var totalPriceCents int64
		orderItems := make([]domain.OrderItem, 0, len(lockedItems))

		for i := range lockedItems {
			item := &lockedItems[i]
			if item.RestaurantID != in.RestaurantID {
				return domain.ErrRestaurantMismatch
			}
			if !item.IsAvailable {
				return domain.ErrItemUnavailable
			}

			qty := requestedQty[item.ID]
			if item.StockQuantity < qty {
				return domain.ErrOutOfStock
			}

			if err := s.menuRepo.DecrementStock(txCtx, item.ID, qty); err != nil {
				return err
			}

			totalPriceCents += item.PriceCents * int64(qty)
			orderItems = append(orderItems, domain.OrderItem{
				MenuItemID: item.ID,
				ItemName:   item.Name,
				PriceCents: item.PriceCents,
				Quantity:   qty,
			})
		}

		createdOrder = domain.Order{
			UserID:          in.UserID,
			RestaurantID:    in.RestaurantID,
			Status:          domain.OrderStatusCreated,
			TotalPriceCents: totalPriceCents,
			DeliveryAddress: in.DeliveryAddress,
			Comment:         in.Comment,
			Items:           orderItems,
		}

		if err := s.orderRepo.CreateOrder(txCtx, &createdOrder); err != nil {
			return fmt.Errorf("persist order: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	s.notifyPartner(ctx, webhookURL, "order.created", &createdOrder)

	return &createdOrder, nil
}

// GetOrderByID returns an order to the customer who placed it.
//
// The order belongs to someone: it carries a delivery address, a comment and a receipt. Without
// this check the identifier alone would be the only thing protecting them, and an identifier is
// not a credential. Someone else's order is reported as missing rather than forbidden, the same
// way cancellation does it, so the endpoint cannot be used to confirm that an order exists.
func (s *OrderService) GetOrderByID(ctx context.Context, id, userID uuid.UUID) (*domain.Order, error) {
	if id == uuid.Nil || userID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}

	order, err := s.orderRepo.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, domain.ErrNotFound
	}

	return order, nil
}

// ListOrders returns orders based on filtering criteria.
func (s *OrderService) ListOrders(ctx context.Context, filter domain.OrderFilter) ([]domain.Order, error) {
	return s.orderRepo.ListOrders(ctx, filter)
}

// UpdateOrderStatus moves an order through its lifecycle on behalf of the authenticated restaurant.
// An order belonging to another establishment is reported as not found, so the endpoint cannot be
// used to probe for foreign order identifiers.
func (s *OrderService) UpdateOrderStatus(
	ctx context.Context,
	restaurantID, orderID uuid.UUID,
	in domain.UpdateOrderStatusInput,
) (*domain.Order, error) {
	if orderID == uuid.Nil || restaurantID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}

	var (
		updatedOrder *domain.Order
		webhookURL   string
	)

	err := s.txManager.WithinTransaction(ctx, func(txCtx context.Context) error {
		order, err := s.orderRepo.GetOrderForUpdate(txCtx, orderID)
		if err != nil {
			return err
		}
		if order.RestaurantID != restaurantID {
			return domain.ErrNotFound
		}

		switch {
		case in.Status == domain.OrderStatusCancelled:
			if !order.Status.CanBeCancelledBy(domain.ActorRestaurant) {
				return domain.ErrOrderAlreadyProcessed
			}
			for _, it := range order.Items {
				if err := s.menuRepo.IncrementStock(txCtx, it.MenuItemID, it.Quantity); err != nil {
					return fmt.Errorf("increment stock for item %s: %w", it.MenuItemID, err)
				}
			}
		case !order.Status.CanTransitionTo(in.Status):
			return domain.ErrInvalidStatusTransition
		}

		comment := in.Comment
		if comment == "" {
			comment = fmt.Sprintf("Статус изменен на %s", in.Status)
		}

		if err := s.applyStatusChange(txCtx, order, in.Status, domain.ActorRestaurant, comment); err != nil {
			return err
		}

		updatedOrder, err = s.orderRepo.GetOrderByID(txCtx, orderID)
		if err != nil {
			return err
		}

		webhookURL, err = s.restRepo.GetWebhookURL(txCtx, order.RestaurantID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	s.notifyPartner(ctx, webhookURL, "order.status_updated", updatedOrder)

	return updatedOrder, nil
}

// CancelOrderByCustomer allows customers to cancel their own order before the kitchen commits to it.
func (s *OrderService) CancelOrderByCustomer(ctx context.Context, orderID, userID uuid.UUID, reason string) (*domain.Order, error) {
	if orderID == uuid.Nil || userID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	if len(reason) > domain.MaxFreeTextLen {
		return nil, domain.ErrInvalidInput
	}

	var (
		cancelledOrder *domain.Order
		webhookURL     string
	)

	err := s.txManager.WithinTransaction(ctx, func(txCtx context.Context) error {
		order, err := s.orderRepo.GetOrderForUpdate(txCtx, orderID)
		if err != nil {
			return err
		}
		if order.UserID != userID {
			return domain.ErrNotFound
		}
		if !order.Status.CanBeCancelledBy(domain.ActorCustomer) {
			return domain.ErrOrderAlreadyProcessed
		}

		for _, it := range order.Items {
			if err := s.menuRepo.IncrementStock(txCtx, it.MenuItemID, it.Quantity); err != nil {
				return fmt.Errorf("increment stock: %w", err)
			}
		}

		if reason == "" {
			reason = "Отменено покупателем"
		}

		if err := s.applyStatusChange(txCtx, order, domain.OrderStatusCancelled, domain.ActorCustomer, reason); err != nil {
			return err
		}

		cancelledOrder, err = s.orderRepo.GetOrderByID(txCtx, orderID)
		if err != nil {
			return err
		}

		webhookURL, err = s.restRepo.GetWebhookURL(txCtx, order.RestaurantID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	s.notifyPartner(ctx, webhookURL, "order.cancelled", cancelledOrder)

	return cancelledOrder, nil
}

// applyStatusChange persists the new status together with its audit entry.
func (s *OrderService) applyStatusChange(
	ctx context.Context,
	order *domain.Order,
	newStatus domain.OrderStatus,
	actor domain.Actor,
	comment string,
) error {
	if err := s.orderRepo.UpdateOrderStatus(ctx, order.ID, newStatus, comment); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	history := &domain.OrderStatusHistory{
		OrderID:    order.ID,
		FromStatus: order.Status,
		ToStatus:   newStatus,
		Actor:      actor,
		Comment:    comment,
	}
	if err := s.orderRepo.AddStatusHistory(ctx, history); err != nil {
		return fmt.Errorf("add status history: %w", err)
	}

	return nil
}

// notifyPartner sends a webhook without blocking the caller. Delivery is best-effort in the MVP,
// so a failure is logged instead of being swallowed - see the README for the outbox plan.
func (s *OrderService) notifyPartner(ctx context.Context, webhookURL, event string, order *domain.Order) {
	if webhookURL == "" || order == nil {
		return
	}
	if err := s.webhookDispatcher.Dispatch(ctx, webhookURL, event, order); err != nil {
		slog.ErrorContext(ctx, "webhook dispatch failed",
			"event", event, "order_id", order.ID, "error", err)
	}
}
