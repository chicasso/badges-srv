package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

type Producer struct {
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	topic          string
	producer       *kafka.Producer
	reportsStarted atomic.Bool
}

func New(topic, servers string) (*Producer, error) {
	return WithContext(context.Background(), topic, servers)
}

func WithContext(ctx context.Context, topic, servers string) (*Producer, error) {
	config := kafka.ConfigMap{
		"bootstrap.servers":  servers,
		"enable.idempotence": true,
		"acks":               "all",
	}

	producer, err := kafka.NewProducer(&config)
	if err != nil {
		return nil, fmt.Errorf("creating kafka producer: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)

	return &Producer{
		topic:    topic,
		producer: producer,
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

func (p *Producer) Close(timeout time.Duration) {
	p.cancel()
	p.Flush(timeout)
	p.producer.Close()
}

func (p *Producer) Send(value []byte, key []byte, timeout time.Duration) error {
	// Blocking
	ch := make(chan kafka.Event, 1)
	err := p.producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &p.topic,
			Partition: kafka.PartitionAny,
		},
		Value: value,
		Key:   key,
	}, ch)
	if err != nil {
		return fmt.Errorf("produce failed: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var e kafka.Event

	select {
	case e = <-ch:
	case <-p.ctx.Done():
		// Eventually the message will be delivered due to Flushing all the pending / queued messages
		return errors.New("producer closed")
	case <-timer.C:
		return fmt.Errorf("send timed out after %s", timeout)
	}
	msg, ok := e.(*kafka.Message)
	if !ok {
		return fmt.Errorf("unexpected kafka event")
	}
	if msg.TopicPartition.Error != nil {
		return fmt.Errorf(
			"delivery failed: %w",
			msg.TopicPartition.Error,
		)
	}

	return nil
}

func (p *Producer) SendAsync(value []byte, key []byte) error {
	if !p.reportsStarted.Load() {
		return errors.New("producer not started")
	}
	err := p.producer.Produce(
		&kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &p.topic,
				Partition: kafka.PartitionAny,
			},
			Value: value,
			Key:   key,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("produce async failed: %w", err)
	}
	return nil
}

func (p *Producer) Flush(timeout time.Duration) int {
	return p.producer.Flush(int(timeout.Milliseconds()))
}

func (p *Producer) StartDeliveryReports() {
	if !p.reportsStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			select {
			case <-p.ctx.Done():
				return
			case e, ok := <-p.producer.Events():
				if !ok {
					return
				}
				switch ev := e.(type) {
				case *kafka.Message:
					if ev.TopicPartition.Error != nil {
						fmt.Println("delivery failed:",
							ev.TopicPartition.Error)
					}
				}
			}
		}
	}()
}
