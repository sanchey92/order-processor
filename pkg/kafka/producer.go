package kafka

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

type ProducerConfig struct {
	Brokers     string
	Acks        string
	LingerMs    int
	Compression string
}

type Producer struct {
	p      *kafka.Producer
	logger *slog.Logger
}

func NewProducer(cfg *ProducerConfig, log *slog.Logger) (*Producer, error) {
	config := &kafka.ConfigMap{
		"bootstrap.servers":            cfg.Brokers,
		"acks":                         cfg.Acks,
		"enable.idempotence":           true,
		"linger.ms":                    cfg.LingerMs,
		"compression.type":             cfg.Compression,
		"message.send.max.retries":     3,
		"delivery.timeout.ms":          30000,
		"queue.buffering.max.messages": 100000,
	}
	p, err := kafka.NewProducer(config)
	if err != nil {
		return nil, fmt.Errorf("kafka.NewProducer: %w", err)
	}
	prod := &Producer{
		p:      p,
		logger: log,
	}

	return prod, nil
}

func (p *Producer) Publish(ctx context.Context, topic string, key, val []byte, headers []kafka.Header) error {
	msg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:     key,
		Value:   val,
		Headers: headers,
	}

	ch := make(chan kafka.Event, 1)

	if err := p.p.Produce(msg, ch); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	select {
	case ev := <-ch:
		m, ok := ev.(*kafka.Message)
		if !ok {
			return fmt.Errorf("unexpected event type: %T", ev)
		}
		if m.TopicPartition.Error != nil {
			return fmt.Errorf("delivery: %w", m.TopicPartition.Error)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Producer) Underlying() *kafka.Producer { return p.p }

func (p *Producer) Close() {
	remaining := p.p.Flush(10_000)
	if remaining > 0 {
		p.logger.Warn("unflushed message on close",
			slog.Int("remaining", remaining))
	}
	p.p.Close()
}
