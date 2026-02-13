package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

const defaultChannelBuf = 256

type ConsumerConfig struct {
	Topics            []string
	Brokers           string
	ConsumerGroup     string
	OffsetReset       string
	SessionTimeoutMs  int
	MaxPollInterval   int
	PartitionStrategy string
	ChannelBufferSize int
}

type Handler func(ctx context.Context, msg *kafka.Message) error

type partitionWorker struct {
	ch   chan *kafka.Message
	done chan struct{}
}

type topicPartition struct {
	topic     string
	partition int32
}

type Consumer struct {
	c       *kafka.Consumer
	h       Handler
	logger  *slog.Logger
	group   string
	bufSize int

	mu      sync.Mutex
	workers map[topicPartition]*partitionWorker
	wg      sync.WaitGroup

	commitCh chan kafka.TopicPartition
}

func NewConsumer(cfg *ConsumerConfig, h Handler, log *slog.Logger) (*Consumer, error) {
	bufSize := cfg.ChannelBufferSize
	if bufSize <= 0 {
		bufSize = defaultChannelBuf
	}

	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":             cfg.Brokers,
		"group.id":                      cfg.ConsumerGroup,
		"enable.auto.commit":            false,
		"auto.offset.reset":             cfg.OffsetReset,
		"session.timeout.ms":            cfg.SessionTimeoutMs,
		"heartbeat.interval.ms":         cfg.SessionTimeoutMs / 3,
		"max.poll.interval.ms":          cfg.MaxPollInterval,
		"partition.assignment.strategy": cfg.PartitionStrategy,
	})
	if err != nil {
		return nil, fmt.Errorf("kafka.newConsumer: %w", err)
	}

	if err = c.SubscribeTopics(cfg.Topics, nil); err != nil {
		if closeErr := c.Close(); closeErr != nil {
			log.Error("failed to close consumer", slog.Any("error", closeErr))
		}
		return nil, fmt.Errorf("subscribe topics: %w", err)
	}

	return &Consumer{
		c:        c,
		h:        h,
		logger:   log,
		group:    cfg.ConsumerGroup,
		bufSize:  bufSize,
		workers:  make(map[topicPartition]*partitionWorker),
		commitCh: make(chan kafka.TopicPartition, bufSize*16),
	}, nil
}

// Run blocks until ctx is canceled, then shuts down gracefully.
func (c *Consumer) Run(ctx context.Context) error {
	c.logger.Info("consumer started", slog.String("group", c.group))

	for {
		select {
		case <-ctx.Done():
			return c.shutdown()
		default:
		}

		ev := c.c.Poll(100)

		// Commit offsets produced by workers since last iteration.
		c.drainCommits()

		if ev == nil {
			continue
		}

		switch e := ev.(type) {
		case *kafka.Message:
			c.dispatch(ctx, e)

		case kafka.Error:
			if e.IsFatal() {
				c.logger.Error("fatal consumer error", slog.Any("error", e))
				if shutdownErr := c.shutdown(); shutdownErr != nil {
					c.logger.Error("shutdown after fatal error", slog.Any("error", shutdownErr))
				}
				return fmt.Errorf("fatal: %w", e)
			}
			c.logger.Warn("consumer error (non-fatal)",
				slog.Any("error", e), slog.Int("code", int(e.Code())))

		case kafka.AssignedPartitions:
			c.logger.Info("partitions assigned",
				slog.Int("count", len(e.Partitions)))

		case kafka.RevokedPartitions:
			c.logger.Info("partitions revoked",
				slog.Int("count", len(e.Partitions)))
			c.stopWorkers(e.Partitions)

		case kafka.OffsetsCommitted:
			if e.Error != nil {
				c.logger.Warn("offset commit error", slog.Any("error", e.Error))
			}
		}
	}
}

func (c *Consumer) dispatch(ctx context.Context, msg *kafka.Message) {
	key := topicPartition{
		topic:     *msg.TopicPartition.Topic,
		partition: msg.TopicPartition.Partition,
	}

	c.mu.Lock()
	pw, ok := c.workers[key]
	if !ok {
		pw = &partitionWorker{
			ch:   make(chan *kafka.Message, c.bufSize),
			done: make(chan struct{}),
		}
		c.workers[key] = pw
		c.wg.Add(1)
		go c.worker(ctx, key, pw)
	}
	c.mu.Unlock()

	// Backpressure: blocks if channel is full, Poll() pauses.
	pw.ch <- msg
}

func (c *Consumer) worker(ctx context.Context, key topicPartition, pw *partitionWorker) {
	defer c.wg.Done()
	defer close(pw.done)

	c.logger.Debug("partition worker started",
		slog.String("topic", key.topic),
		slog.Int("partition", int(key.partition)))

	for msg := range pw.ch {
		if err := c.h(ctx, msg); err != nil {
			c.logger.Error("handler error",
				slog.String("topic", key.topic),
				slog.Int("partition", int(key.partition)),
				slog.Int64("offset", int64(msg.TopicPartition.Offset)),
				slog.Any("error", err))
		}

		// Send processed offset to the poll goroutine for commit.
		c.commitCh <- kafka.TopicPartition{
			Topic:     msg.TopicPartition.Topic,
			Partition: msg.TopicPartition.Partition,
			Offset:    msg.TopicPartition.Offset + 1,
		}
	}

	c.logger.Debug("partition worker stopped",
		slog.String("topic", key.topic),
		slog.Int("partition", int(key.partition)))
}

// drainCommits reads all pending offsets from commitCh and commits them.
// Called from the poll goroutine on every iteration.
func (c *Consumer) drainCommits() {
	pending := make(map[topicPartition]kafka.TopicPartition)

	for {
		select {
		case tp := <-c.commitCh:
			key := topicPartition{topic: *tp.Topic, partition: tp.Partition}
			if prev, ok := pending[key]; !ok || tp.Offset > prev.Offset {
				pending[key] = tp
			}
		default:
			c.commitOffsets(pending)
			return
		}
	}
}

// awaitWorkers drains commitCh concurrently while waiting for workers
// to finish. Prevents deadlock when commitCh buffer fills up.
func (c *Consumer) awaitWorkers(done <-chan struct{}) {
	pending := make(map[topicPartition]kafka.TopicPartition)

	for {
		select {
		case <-done:
			// Workers finished — final non-blocking drain and commit.
			for {
				select {
				case tp := <-c.commitCh:
					key := topicPartition{topic: *tp.Topic, partition: tp.Partition}
					if prev, ok := pending[key]; !ok || tp.Offset > prev.Offset {
						pending[key] = tp
					}
				default:
					c.commitOffsets(pending)
					return
				}
			}
		case tp := <-c.commitCh:
			key := topicPartition{topic: *tp.Topic, partition: tp.Partition}
			if prev, ok := pending[key]; !ok || tp.Offset > prev.Offset {
				pending[key] = tp
			}
		}
	}
}

func (c *Consumer) commitOffsets(pending map[topicPartition]kafka.TopicPartition) {
	if len(pending) == 0 {
		return
	}

	offsets := make([]kafka.TopicPartition, 0, len(pending))
	for _, tp := range pending {
		offsets = append(offsets, tp)
	}

	if _, err := c.c.CommitOffsets(offsets); err != nil {
		c.logger.Error("commit offsets", slog.Any("error", err))
	}
}

func (c *Consumer) stopWorkers(partitions []kafka.TopicPartition) {
	c.mu.Lock()
	var toWait []chan struct{}
	for _, tp := range partitions {
		key := topicPartition{topic: *tp.Topic, partition: tp.Partition}
		if pw, ok := c.workers[key]; ok {
			close(pw.ch)
			toWait = append(toWait, pw.done)
			delete(c.workers, key)
		}
	}
	c.mu.Unlock()

	if len(toWait) == 0 {
		return
	}

	done := make(chan struct{})
	go func() {
		for _, d := range toWait {
			<-d
		}
		close(done)
	}()

	c.awaitWorkers(done)
}

func (c *Consumer) shutdown() error {
	c.logger.Info("consumer shutting down")

	c.mu.Lock()
	for p, pw := range c.workers {
		close(pw.ch)
		delete(c.workers, p)
	}
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	c.awaitWorkers(done)

	if err := c.c.Close(); err != nil {
		return fmt.Errorf("closing: %w", err)
	}

	c.logger.Info("consumer stopped")
	return nil
}
