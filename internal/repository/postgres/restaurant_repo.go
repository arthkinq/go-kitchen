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

// restaurantColumns is the public projection. Neither api_key nor webhook_url is selected here:
// one is a credential, the other is a partner's internal address, and neither is any of a client's
// business. Keeping them out of the projection means no response can leak them by accident - a
// field that was never selected cannot be serialised. Whoever genuinely needs the callback address
// asks for it by name, through GetWebhookURL.
const restaurantColumns = `id, name, description, address, is_active, created_at, updated_at`

// RestaurantRepository implements domain.RestaurantRepository using PostgreSQL.
type RestaurantRepository struct {
	pool *pgxpool.Pool
}

// NewRestaurantRepository constructs a new RestaurantRepository.
func NewRestaurantRepository(pool *pgxpool.Pool) *RestaurantRepository {
	return &RestaurantRepository{pool: pool}
}

func scanRestaurant(row pgx.Row, rest *domain.Restaurant) error {
	return row.Scan(
		&rest.ID,
		&rest.Name,
		&rest.Description,
		&rest.Address,
		&rest.IsActive,
		&rest.CreatedAt,
		&rest.UpdatedAt,
	)
}

// Create inserts a new restaurant together with the API key issued for its closed partner access.
func (r *RestaurantRepository) Create(ctx context.Context, rest *domain.Restaurant) error {
	exec := getExecutor(ctx, r.pool)

	id, err := newID()
	if err != nil {
		return err
	}

	query := `
		INSERT INTO restaurants (id, name, description, address, is_active, webhook_url, api_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at;
	`

	err = exec.QueryRow(ctx, query,
		id,
		rest.Name,
		rest.Description,
		rest.Address,
		rest.IsActive,
		rest.WebhookURL,
		rest.APIKey,
	).Scan(&rest.ID, &rest.CreatedAt, &rest.UpdatedAt)
	if err != nil {
		return mapPgError(fmt.Errorf("insert restaurant: %w", err))
	}

	return nil
}

// GetByID fetches a restaurant by its UUID.
func (r *RestaurantRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Restaurant, error) {
	exec := getExecutor(ctx, r.pool)

	query := `SELECT ` + restaurantColumns + ` FROM restaurants WHERE id = $1;`

	var rest domain.Restaurant
	if err := scanRestaurant(exec.QueryRow(ctx, query, id), &rest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrRestaurantNotFound
		}
		return nil, fmt.Errorf("select restaurant by id: %w", err)
	}

	return &rest, nil
}

// GetWebhookURL returns only the establishment's callback address.
//
// It is kept out of restaurantColumns on purpose, so the webhook dispatcher asks for it explicitly
// instead of a whole row travelling through the client code path carrying a partner-internal field.
func (r *RestaurantRepository) GetWebhookURL(ctx context.Context, id uuid.UUID) (string, error) {
	exec := getExecutor(ctx, r.pool)

	var url string
	err := exec.QueryRow(ctx, `SELECT webhook_url FROM restaurants WHERE id = $1;`, id).Scan(&url)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrRestaurantNotFound
		}
		return "", fmt.Errorf("select webhook url: %w", err)
	}

	return url, nil
}

// GetByAPIKey resolves the establishment behind a partner API key.
func (r *RestaurantRepository) GetByAPIKey(ctx context.Context, apiKey string) (*domain.Restaurant, error) {
	exec := getExecutor(ctx, r.pool)

	query := `SELECT ` + restaurantColumns + ` FROM restaurants WHERE api_key = $1;`

	var rest domain.Restaurant
	if err := scanRestaurant(exec.QueryRow(ctx, query, apiKey), &rest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrRestaurantNotFound
		}
		return nil, fmt.Errorf("select restaurant by api key: %w", err)
	}

	return &rest, nil
}

// likeEscaper neutralises the LIKE metacharacters. Replacer makes a single pass over the input,
// so the backslashes it inserts are never escaped a second time.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern wraps raw user input into an ILIKE pattern that matches it literally.
//
// To the person typing them, `%` and `_` in a search box are ordinary characters. Left unescaped
// they are wildcards: "скидка 50%" would match every establishment, and a three-character "___"
// would return the whole catalogue instead of nothing. Backslash is already PostgreSQL's default
// LIKE escape, so the queries spell ESCAPE '\' out only to say it outright: the SQL standard
// defines no default, and the pattern should not rely on a dialect's.
func likePattern(search string) string {
	return "%" + likeEscaper.Replace(search) + "%"
}

// List returns restaurants matching the filter, newest first.
func (r *RestaurantRepository) List(ctx context.Context, filter domain.RestaurantFilter) ([]domain.Restaurant, error) {
	exec := getExecutor(ctx, r.pool)

	var sb strings.Builder
	sb.WriteString(`SELECT ` + restaurantColumns + ` FROM restaurants WHERE 1 = 1`)

	args := make([]any, 0, 4)

	if filter.OnlyActive {
		sb.WriteString(" AND is_active = true")
	}
	if filter.Search != "" {
		args = append(args, likePattern(filter.Search))
		fmt.Fprintf(&sb, " AND (name ILIKE $%d ESCAPE '\\' OR description ILIKE $%d ESCAPE '\\')",
			len(args), len(args))
	}

	sb.WriteString(" ORDER BY created_at DESC")

	args = append(args, filter.Limit)
	fmt.Fprintf(&sb, " LIMIT $%d", len(args))

	args = append(args, filter.Offset)
	fmt.Fprintf(&sb, " OFFSET $%d", len(args))

	rows, err := exec.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query restaurants: %w", err)
	}
	defer rows.Close()

	restaurants := make([]domain.Restaurant, 0)
	for rows.Next() {
		var rest domain.Restaurant
		if err := scanRestaurant(rows, &rest); err != nil {
			return nil, fmt.Errorf("scan restaurant: %w", err)
		}
		restaurants = append(restaurants, rest)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}

	return restaurants, nil
}

// Update modifies restaurant fields dynamically and returns the updated entity.
func (r *RestaurantRepository) Update(ctx context.Context, id uuid.UUID, in domain.UpdateRestaurantInput) (*domain.Restaurant, error) {
	exec := getExecutor(ctx, r.pool)

	setClauses := make([]string, 0, 6)
	args := make([]any, 0, 6)

	addSet := func(column string, value any) {
		args = append(args, value)
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if in.Name != nil {
		addSet("name", *in.Name)
	}
	if in.Description != nil {
		addSet("description", *in.Description)
	}
	if in.Address != nil {
		addSet("address", *in.Address)
	}
	if in.IsActive != nil {
		addSet("is_active", *in.IsActive)
	}
	if in.WebhookURL != nil {
		addSet("webhook_url", *in.WebhookURL)
	}

	if len(setClauses) == 0 {
		return r.GetByID(ctx, id)
	}

	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, id)

	query := fmt.Sprintf(`UPDATE restaurants SET %s WHERE id = $%d RETURNING %s;`,
		strings.Join(setClauses, ", "), len(args), restaurantColumns)

	var rest domain.Restaurant
	if err := scanRestaurant(exec.QueryRow(ctx, query, args...), &rest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrRestaurantNotFound
		}
		return nil, mapPgError(fmt.Errorf("update restaurant: %w", err))
	}

	return &rest, nil
}
