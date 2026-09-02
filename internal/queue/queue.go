package queue

import (
	"context"
)

type Queue interface {
	Enqueue(ctx context.Context, topic string, payload []byte) error
}

type MemoryQueue struct {
	ch chan job
}

type job struct {
	Topic   string
	Payload []byte
}

func NewMemory(buf int) *MemoryQueue {
	if buf <= 0 {
		buf = 100
	}
	return &MemoryQueue{ch: make(chan job, buf)}
}

func (q *MemoryQueue) Enqueue(_ context.Context, topic string, payload []byte) error {
	select {
	case q.ch <- job{Topic: topic, Payload: payload}:
		return nil
	default:
		return nil
	}
}

func (q *MemoryQueue) Chan() <-chan job { return q.ch }
