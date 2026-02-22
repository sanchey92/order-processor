package app

import (
	"context"
	"log/slog"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/storage/pg"
)

func initStorage(ctx context.Context, logger *slog.Logger, cfg config.Postgres) (*pg.Storage, error) {
	pgConfig := &pg.StorageConfig{
		DSN:             cfg.DSN,
		MaxConns:        cfg.MaxConns,
		MinConns:        cfg.MinConns,
		MaxConnLife:     cfg.MaxConnLifetime,
		MaxConnIdleTime: cfg.MaxConnIdleTime,
	}

	pgStorage, err := pg.NewPGStorage(ctx, logger, pgConfig)
	if err != nil {
		return nil, err
	}

	logger.Info("postgres connected")

	return pgStorage, nil
}
