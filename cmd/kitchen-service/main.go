package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/arthkinq/go-kitchen/internal/config"
	apphttp "github.com/arthkinq/go-kitchen/internal/handler/http"
	"github.com/arthkinq/go-kitchen/internal/repository/postgres"
	"github.com/arthkinq/go-kitchen/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application terminated with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})))
	slog.Info("starting go.kitchen core service...", "log_level", cfg.LogLevel.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()
	slog.Info("connected to postgres pool successfully")

	txManager := postgres.NewTxManager(pool)
	restRepo := postgres.NewRestaurantRepository(pool)
	menuRepo := postgres.NewMenuRepository(pool)
	orderRepo := postgres.NewOrderRepository(pool)

	webhookDispatcher := service.NewHTTPWebhookDispatcher(cfg.WebhookTimeout, cfg.WebhookMaxInFlight)
	restService := service.NewRestaurantService(restRepo)
	menuService := service.NewMenuService(menuRepo, restRepo)
	orderService := service.NewOrderService(orderRepo, menuRepo, restRepo, txManager, webhookDispatcher)

	clientHandler := apphttp.NewClientHandler(restService, menuService, orderService)
	partnerHandler := apphttp.NewPartnerHandler(restService, menuService, orderService, cfg.AdminBootstrapToken)
	router := apphttp.NewRouter(clientHandler, partnerHandler, apphttp.PartnerAuth(restService))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received, commencing graceful shutdown...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed, forcing close", "error", err)
		_ = server.Close()
	}

	slog.Info("server stopped gracefully")
	return nil
}
