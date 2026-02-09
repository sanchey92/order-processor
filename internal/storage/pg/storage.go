package pg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

type executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

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

func (s *Storage) conn(ctx context.Context) executor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return s.pool
}

func (s *Storage) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("RunInTx: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			s.logger.Error("failed to rollback transaction", slog.Any("error", rbErr))
		}
	}()

	if err = fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("tx commit: %w", err)
	}
	return nil
}

func (s *Storage) Close() {
	s.pool.Close()
}
