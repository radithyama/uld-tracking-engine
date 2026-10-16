// Package kafkax builds tuned kafka-go writers and readers.
package kafkax

import (
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/compress"
)

// NewWriter returns a producer that is safe for concurrent use by many HTTP
// handler goroutines. Concurrent WriteMessages calls are coalesced into
// batches per partition, so throughput scales with request concurrency while
// each caller still gets a synchronous ack.
func NewWriter(brokers []string, topic string) *kafka.Writer {
	return &kafka.Writer{
		Addr:  kafka.TCP(brokers...),
		Topic: topic,
		// Keyed by ULD id; murmur2 matches the Java client's partitioner so
		// every producer routes a given ULD to the same partition, which
		// preserves per-ULD ordering end to end.
		Balancer:               &kafka.Murmur2Balancer{},
		RequiredAcks:           kafka.RequireAll,
		BatchSize:              1000,
		BatchBytes:             4 << 20,
		BatchTimeout:           5 * time.Millisecond,
		WriteTimeout:           10 * time.Second,
		MaxAttempts:            3,
		WriteBackoffMin:        100 * time.Millisecond,
		WriteBackoffMax:        1 * time.Second,
		Compression:            compress.Snappy,
		AllowAutoTopicCreation: false,
	}
}

// NewGroupReader returns one member of a consumer group. Run several (one per
// goroutine) to consume partitions in parallel; Kafka assigns each partition
// to exactly one member, so ordering per partition is preserved.
func NewGroupReader(brokers []string, topic, groupID string, errLog kafka.Logger) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:           brokers,
		Topic:             topic,
		GroupID:           groupID,
		MinBytes:          1,
		MaxBytes:          10 << 20,
		MaxWait:           250 * time.Millisecond,
		StartOffset:       kafka.FirstOffset,
		CommitInterval:    time.Second, // offsets are flushed periodically and on Close
		HeartbeatInterval: 3 * time.Second,
		SessionTimeout:    30 * time.Second,
		ErrorLogger:       errLog,
	})
}

func Header(m kafka.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
