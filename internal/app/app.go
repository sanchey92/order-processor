package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/service/order"
	"github.com/sanchey92/order-processor/internal/storage/pg"
	customKafka "github.com/sanchey92/order-processor/pkg/kafka"
	"github.com/sanchey92/order-processor/pkg/outbox"
)

type App struct {
	logger     *slog.Logger
	pgStorage  *pg.Storage
	producer   *customKafka.Producer
	consumer   *customKafka.Consumer
	httpServer *http.Server
	relay      *outbox.Relay
}

func New(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required")
	}

	logger := newLogger(cfg.App.LogLevel, cfg.App.Name)
	slog.SetDefault(logger)
	logger.Info("initialising", slog.String("service", cfg.App.Name))

	pgStorage, err := initStorage(context.Background(), logger, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("app creation: %w", err)
	}

	paymentClient, warehouseClient := initClients(cfg, logger)

	orderService := order.NewOrderService(
		logger, pgStorage, pgStorage, pgStorage,
		warehouseClient, paymentClient, cfg.Kafka.EventTopic,
	)

	srv := initHTTPServer(cfg.HTTP, orderService, logger)

	producer, err := initProducer(cfg.Kafka, logger)
	if err != nil {
		pgStorage.Close()
		return nil, fmt.Errorf("app creation: %w", err)
	}

	relay := initRelay(cfg.Outbox, pgStorage, producer, logger)

	consumer, err := initConsumer(cfg.Kafka, pgStorage, producer, orderService, logger)
	if err != nil {
		producer.Close()
		pgStorage.Close()
		return nil, fmt.Errorf("app creation: %w", err)
	}

	logger.Info("application initialized")

	return &App{
		logger:     logger,
		pgStorage:  pgStorage,
		httpServer: srv,
		producer:   producer,
		consumer:   consumer,
		relay:      relay,
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
		a.logger.Info("kafka consumer started")
		if err := a.consumer.Run(gCtx); err != nil {
			return fmt.Errorf("consumer run error: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		return a.relay.Run(gCtx)
	})

	g.Go(func() error {
		<-gCtx.Done()
		a.logger.Info("shutdown signal received")
		return a.shutdown()
	})

	err := g.Wait()

	a.producer.Close()
	a.pgStorage.Close()
	a.logger.Info("resources released")

	return err
}

func (a *App) shutdown() error {
	a.logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	a.logger.Info("http server stopped")
	return nil
}
