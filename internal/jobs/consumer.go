package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

type Handler func(msg *kafka.Message) error

type Consumer struct {
	ctx            context.Context
	cancel         context.CancelFunc
	topic          string
	consumer       *kafka.Consumer
	handler        Handler
	onError        func(error)
	pollTimeout    time.Duration
	commitInterval time.Duration
	once           sync.Once
	wg             sync.WaitGroup
	mu             sync.RWMutex
}

type ConsumerOption func(*Consumer)

func WithPollTimeout(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.pollTimeout = d
	}
}

func WithCommitInterval(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.commitInterval = d
	}
}

func WithErrorHandler(fn func(error)) ConsumerOption {
	return func(c *Consumer) {
		c.onError = fn
	}
}

func NewConsumer(
	ctx context.Context,
	brokers, groupID, topic string,
	handler Handler,
	opts ...ConsumerOption,
) (*Consumer, error) {
	config := &kafka.ConfigMap{
		"bootstrap.servers":        brokers,
		"group.id":                 groupID,
		"auto.offset.reset":        "earliest",
		"enable.auto.commit":       false, // committing manually for correctness
		"enable.auto.offset.store": false,
	}

	kc, err := kafka.NewConsumer(config)
	if err != nil {
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}

	// Todo: rebalanceCb
	if err := kc.Subscribe(topic, nil); err != nil {
		_ = kc.Close()
		return nil, fmt.Errorf("subscribing to topic %q: %w", topic, err)
	}

	ctx, cancel := context.WithCancel(ctx)

	c := &Consumer{
		consumer:       kc,
		topic:          topic,
		handler:        handler,
		onError:        func(error) {},
		pollTimeout:    100 * time.Millisecond,
		commitInterval: 5 * time.Second,
		ctx:            ctx,
		cancel:         cancel,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Consumer) Start() {
	c.once.Do(func() {
		c.wg.Add(1)
		go c.loop()
	})
}

func (c *Consumer) Close() error {
	c.cancel()
	c.wg.Wait()
	return c.consumer.Close()
}

func (c *Consumer) loop() {
	defer c.wg.Done()

	var (
		uncommitted int
		lastCommit  = time.Now()
	)

	for {
		if c.ctx.Err() != nil {
			c.commit()
			return
		}

		ev := c.consumer.Poll(int(c.pollTimeout.Milliseconds()))
		if ev == nil {
			continue
		}

		switch e := ev.(type) {
		case *kafka.Message:
			if err := c.handler(e); err != nil {
				c.onError(fmt.Errorf("handler error on %s/%d@%d: %w",
					*e.TopicPartition.Topic,
					e.TopicPartition.Partition,
					e.TopicPartition.Offset,
					err,
				))
				// do not stop
			}

			if _, err := c.consumer.StoreMessage(e); err != nil {
				c.onError(fmt.Errorf("storing offset: %w", err))
				// do not stop
			}
			uncommitted++

			shouldCommit := c.commitInterval == 0 ||
				time.Since(lastCommit) >= c.commitInterval

			if shouldCommit {
				c.commit()
				uncommitted = 0
				lastCommit = time.Now()
			}

		case kafka.Error:
			// kafka.ErrAllBrokersDown is retryable; log and continue.
			c.onError(fmt.Errorf("kafka error (code=%d, fatal=%v): %w",
				e.Code(), e.IsFatal(), e))
			if e.IsFatal() {
				return
			}

		case kafka.AssignedPartitions:
			if err := c.consumer.Assign(e.Partitions); err != nil {
				c.onError(fmt.Errorf("assigning partitions: %w", err))
			}

		case kafka.RevokedPartitions:
			// Commit before giving up partitions to avoid reprocessing.
			if uncommitted > 0 {
				c.commit()
				uncommitted = 0
				lastCommit = time.Now()
			}
			if err := c.consumer.Unassign(); err != nil {
				c.onError(fmt.Errorf("un-assigning partitions: %w", err))
			}
		}
	}
}

func (c *Consumer) commit() {
	if _, err := c.consumer.Commit(); err != nil &&
		!errors.Is(err, kafka.NewError(kafka.ErrNoOffset, "", false)) {
		c.onError(fmt.Errorf("committing offsets: %w", err))
	}
}
