package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/storage/pg"
)

type App struct {
	logger    *slog.Logger
	pgStorage *pg.Storage
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

	return &App{
		logger:    logger,
		pgStorage: pgStorage,
	}, nil
}

func (a *App) Run() {
	fmt.Println("hello from application")
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
