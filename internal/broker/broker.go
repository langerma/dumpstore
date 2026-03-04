package broker

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// ValidTopics is the canonical set of topic names understood by the SSE endpoint.
// Unknown topic names in client query params are silently ignored.
var ValidTopics = map[string]bool{
	"pool.query":     true,
	"dataset.query":  true,
	"snapshot.query": true,
	"iostat":         true,
	"user.query":     true,
	"group.query":    true,
}

// Broker is a thread-safe, topic-based pub/sub message broker.
// The zero value is not usable; use New.
type Broker struct {
	mu   sync.Mutex
	subs map[string][]chan []byte
}

// New returns an initialised Broker.
func New() *Broker {
	return &Broker{subs: make(map[string][]chan []byte)}
}

// Subscribe registers a new subscriber for topic and returns a buffered channel
// (size 8) that receives JSON-encoded payloads. The caller must call Unsubscribe
// when done to avoid a goroutine/channel leak.
func (b *Broker) Subscribe(topic string) chan []byte {
	ch := make(chan []byte, 8)
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from topic's subscriber list and closes it. Calling
// Unsubscribe with a channel not registered for the topic is a no-op.
func (b *Broker) Unsubscribe(topic string, ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[topic]
	for i, c := range list {
		if c == ch {
			// Swap-remove: O(1), order-independent.
			b.subs[topic] = append(list[:i], list[i+1:]...)
			close(ch)
			return
		}
	}
}

// Publish JSON-encodes data and delivers it to every subscriber of topic.
// The send is non-blocking: if a subscriber's buffer is full the message is
// dropped for that subscriber so the caller is never stalled.
func (b *Broker) Publish(topic string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		slog.Error("broker: marshal failed", "topic", topic, "err", err)
		return
	}
	// Copy the slice under lock then release before sending, so the mutex is
	// held only for the slice copy and not for potentially-blocking channel ops.
	b.mu.Lock()
	snapshot := make([]chan []byte, len(b.subs[topic]))
	copy(snapshot, b.subs[topic])
	b.mu.Unlock()

	for _, ch := range snapshot {
		select {
		case ch <- payload:
		default:
			slog.Warn("broker: subscriber slow, dropping message", "topic", topic)
		}
	}
}
