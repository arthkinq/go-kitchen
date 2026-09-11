package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/arthkinq/go-kitchen/internal/domain"
	"github.com/arthkinq/go-kitchen/internal/service"
)

type partnerCtxKey struct{}

// bearerPrefix is the auth scheme accepted in the Authorization header.
const bearerPrefix = "Bearer "

// PartnerAuth turns a partner API key into the identity of the calling establishment.
//
// The assignment does not require user authentication, but partner access is explicitly closed:
// only the listed establishments may work through the platform. A key is issued once at
// registration and every partner endpoint is scoped to the restaurant behind it.
func PartnerAuth(restService *service.RestaurantService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := extractAPIKey(r)
			if apiKey == "" || !isPostgresText(apiKey) {
				respondError(w, domain.ErrUnauthorized)
				return
			}

			rest, err := restService.GetByAPIKey(r.Context(), apiKey)
			switch {
			case errors.Is(err, domain.ErrRestaurantNotFound):
				respondError(w, domain.ErrUnauthorized)
				return
			case err != nil:
				respondError(w, err)
				return
			}

			ctx := context.WithValue(r.Context(), partnerCtxKey{}, rest.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// partnerID returns the establishment authenticated for this request.
func partnerID(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(partnerCtxKey{}).(uuid.UUID)
	return id
}

// extractAPIKey accepts either `Authorization: Bearer <key>` or `X-Api-Key: <key>`.
// The auth scheme is matched case-insensitively, as RFC 7235 requires.
func extractAPIKey(r *http.Request) string {
	if raw := r.Header.Get("Authorization"); len(raw) > len(bearerPrefix) {
		if strings.EqualFold(raw[:len(bearerPrefix)], bearerPrefix) {
			return strings.TrimSpace(raw[len(bearerPrefix):])
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}
