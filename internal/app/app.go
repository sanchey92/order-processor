package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sync/errgroup"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/storage/pg"
)

type App struct {
	logger     *slog.Logger
	pgStorage  *pg.Storage
	httpServer *http.Server
}

func New(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required")
	}

	ctx := context.Background()

	// Logger initialisation
	logger := newLogger(cfg.App.LogLevel, cfg.App.Name)
	slog.SetDefault(logger)
	logger.Info("initialising", slog.String("service", cfg.App.Name))

	// PgStorage initialisation
	pgConfig := &pg.StorageConfig{
		DSN:             cfg.Postgres.DSN,
		MaxConns:        cfg.Postgres.MaxConns,
		MinConns:        cfg.Postgres.MinConns,
		MaxConnLife:     cfg.Postgres.MaxConnLifetime,
		MaxConnIdleTime: cfg.Postgres.MaxConnIdleTime,
	}

	pgStorage, err := pg.NewPGStorage(ctx, pgConfig)
	if err != nil {
		return nil, fmt.Errorf("app creation: %w", err)
	}

	logger.Info("postgres connected")

	// HTTP Server initialization
	r := chi.NewRouter()
	r.Use(middleware.RequestID)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:      r,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}

	logger.Info("application initialized")

	return &App{
		logger:     logger,
		pgStorage:  pgStorage,
		httpServer: srv,
	}, nil
}

func (a *App) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.logger.Info("http server starting")
		if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		a.logger.Info("shutdown signal received")
		return a.shutdown()
	})

	return g.Wait()
}

func (a *App) shutdown() error {
	a.logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("app shutdown: %w", err)
	}

	a.pgStorage.Close()

	a.logger.Info("shutdown complete")
	return nil
}

func newLogger(level, service string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "info":
		lvl = slog.LevelInfo
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).
		With(slog.String("service", service))
}
