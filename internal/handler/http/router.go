package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/arthkinq/go-kitchen/internal/handler/http/middleware"
)

// requestTimeout must stay below the server's WriteTimeout, otherwise the connection is
// torn down before the handler can turn the cancelled context into a response.
const requestTimeout = 10 * time.Second

// NewRouter sets up the Chi HTTP router with all middlewares and API routes.
// partnerAuth guards the closed partner area. Registration sits outside that group because the
// caller has no API key yet - it is gated separately, by the bootstrap token checked inside
// PartnerHandler.CreateRestaurant.
func NewRouter(
	clientHandler *ClientHandler,
	partnerHandler *PartnerHandler,
	partnerAuth func(http.Handler) http.Handler,
) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(middleware.StructuredLogger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(requestTimeout))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Api-Key", "X-CSRF-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"Link", "X-Request-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	healthHandler := func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	r.Get("/healthz", healthHandler)
	r.Head("/healthz", healthHandler)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/restaurants", clientHandler.ListRestaurants)
		r.Get("/restaurants/{restaurant_id}", clientHandler.GetRestaurant)
		r.Get("/restaurants/{restaurant_id}/menu", clientHandler.GetMenu)
		r.Get("/items", clientHandler.SearchItems)
		r.Post("/orders", clientHandler.CreateOrder)
		r.Get("/orders", clientHandler.ListOrders)
		r.Get("/orders/{order_id}", clientHandler.GetOrder)
		r.Post("/orders/{order_id}/cancel", clientHandler.CancelOrder)

		r.Route("/partner", func(r chi.Router) {
			r.Post("/restaurants", partnerHandler.CreateRestaurant)

			r.Group(func(r chi.Router) {
				r.Use(partnerAuth)

				r.Patch("/restaurants/{restaurant_id}", partnerHandler.UpdateRestaurant)
				r.Post("/restaurants/{restaurant_id}/categories", partnerHandler.CreateCategory)
				r.Patch("/categories/{category_id}", partnerHandler.UpdateCategory)
				r.Delete("/categories/{category_id}", partnerHandler.DeleteCategory)
				r.Post("/restaurants/{restaurant_id}/items", partnerHandler.CreateItem)
				r.Patch("/items/{item_id}", partnerHandler.UpdateItem)
				r.Delete("/items/{item_id}", partnerHandler.DeleteItem)
				r.Get("/restaurants/{restaurant_id}/orders", partnerHandler.ListOrders)
				r.Patch("/orders/{order_id}/status", partnerHandler.UpdateOrderStatus)
			})
		})
	})

	return r
}
