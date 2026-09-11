package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

// apiKeyBytes is the entropy of an issued partner key (32 bytes -> 64 hex characters).
const apiKeyBytes = 32

// RestaurantService handles business logic for restaurant establishments.
type RestaurantService struct {
	repo domain.RestaurantRepository
}

// NewRestaurantService creates a new RestaurantService instance.
func NewRestaurantService(repo domain.RestaurantRepository) *RestaurantService {
	return &RestaurantService{repo: repo}
}

// Create registers a new partner establishment and issues the API key that unlocks its
// closed partner endpoints. The key is returned exactly once, in this response.
func (s *RestaurantService) Create(ctx context.Context, in domain.CreateRestaurantInput) (*domain.Restaurant, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("issue api key: %w", err)
	}

	rest := &domain.Restaurant{
		Name:        in.Name,
		Description: in.Description,
		Address:     in.Address,
		IsActive:    true,
		WebhookURL:  in.WebhookURL,
		APIKey:      apiKey,
	}

	if err := s.repo.Create(ctx, rest); err != nil {
		return nil, fmt.Errorf("create restaurant: %w", err)
	}

	return rest, nil
}

// GetByID retrieves a restaurant by ID.
func (s *RestaurantService) GetByID(ctx context.Context, id uuid.UUID) (*domain.Restaurant, error) {
	if id == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.GetByID(ctx, id)
}

// GetByAPIKey resolves the establishment behind a partner API key.
func (s *RestaurantService) GetByAPIKey(ctx context.Context, apiKey string) (*domain.Restaurant, error) {
	if apiKey == "" {
		return nil, domain.ErrUnauthorized
	}
	rest, err := s.repo.GetByAPIKey(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	return rest, nil
}

// List returns restaurants matching filter criteria (e.g. only active ones for clients).
func (s *RestaurantService) List(ctx context.Context, filter domain.RestaurantFilter) ([]domain.Restaurant, error) {
	return s.repo.List(ctx, filter)
}

// Update modifies the establishment identified by id on behalf of actorRestaurantID.
// A partner may only edit its own card.
func (s *RestaurantService) Update(
	ctx context.Context,
	actorRestaurantID, id uuid.UUID,
	in domain.UpdateRestaurantInput,
) (*domain.Restaurant, error) {
	if id == uuid.Nil || actorRestaurantID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	if id != actorRestaurantID {
		return nil, domain.ErrRestaurantNotFound
	}
	return s.repo.Update(ctx, id, in)
}

// generateAPIKey produces a cryptographically random partner credential.
func generateAPIKey() (string, error) {
	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
