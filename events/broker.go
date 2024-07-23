package events

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Subscriber struct {
	Channel     chan []Message
	Unsubscribe chan bool
}

type Broker interface {
	Subscribe(ctx context.Context, channels ...string) *chan []Message
	Unsubscribe(ctx context.Context, sub *chan []Message, channels ...string)
	Publish(ctx context.Context, topic string, messages []Message) error
	Close()
}

type MemoryBroker struct {
	subscribers map[string][]*Subscriber
	mutex       sync.Mutex
}

func NewMemoryBroker() Broker {
	return &MemoryBroker{subscribers: make(map[string][]*Subscriber)}
}

func (b *MemoryBroker) Subscribe(ctx context.Context, channels ...string) *chan []Message {
	sub := &Subscriber{
		Channel:     make(chan []Message, 1),
		Unsubscribe: make(chan bool),
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, t := range channels {
		b.subscribers[t] = append(b.subscribers[t], sub)
	}
	return &sub.Channel
}

func (b *MemoryBroker) Unsubscribe(ctx context.Context, sub *chan []Message, channels ...string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, t := range channels {
		if subscribers, found := b.subscribers[t]; found {
			var newSubscribers []*Subscriber
			for _, subscriber := range subscribers {
				if subscriber.Channel != *sub {
					newSubscribers = append(newSubscribers, subscriber)
				}
			}
			b.subscribers[t] = newSubscribers
		}
	}
}

func (b *MemoryBroker) Publish(ctx context.Context, channel string, messages []Message) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	
	if subscribers, found := b.subscribers[channel]; found {
		for _, sub := range subscribers {
			select {
			case sub.Channel <- messages:
			case <-time.After(time.Second):
				fmt.Printf("Subscriber slow. Unsubscribing from channel: %s\n", channel)
				b.Unsubscribe(ctx, &sub.Channel, channel)
			}
		}
	}
	return nil
}

func (b *MemoryBroker) Close() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, subscribers := range b.subscribers {
		for _, subscriber := range subscribers {
			close(subscriber.Channel)
		}
	}
}
