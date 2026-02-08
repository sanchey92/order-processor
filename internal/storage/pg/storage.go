package pg

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type StorageConfig struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLife     time.Duration
	MaxConnIdleTime time.Duration
}

type Storage struct {
	logger *slog.Logger
	pool   *pgxpool.Pool
}

func NewPGStorage(ctx context.Context, log *slog.Logger, cfg *StorageConfig) (*Storage, error) {
	pgConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	pgConfig.MaxConns = cfg.MaxConns
	pgConfig.MinConns = cfg.MinConns
	pgConfig.MaxConnLifetime = cfg.MaxConnLife
	pgConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, pgConfig)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	if err = pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}

	return &Storage{pool: pool, logger: log}, nil
}

func (s *Storage) Close() {
	s.pool.Close()
}
