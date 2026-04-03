package broker

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// ValidTopics is the canonical set of topic names understood by the SSE endpoint.
// Unknown topic names in client query params are silently ignored.
var ValidTopics = map[string]bool{
	"pool.query":         true,
	"poolstatus":         true,
	"dataset.query":      true,
	"autosnapshot.query": true,
	"snapshot.query":     true,
	"iostat":             true,
	"user.query":         true,
	"group.query":        true,
	"ansible.progress":   true,
	"service.query":      true,
}

// Broker is a thread-safe, topic-based pub/sub message broker.
// The zero value is not usable; use New.
type Broker struct {
	mu    sync.Mutex
	subs  map[string][]chan []byte
	cache map[string][]byte // last published payload per topic
}

// New returns an initialised Broker.
func New() *Broker {
	return &Broker{
		subs:  make(map[string][]chan []byte),
		cache: make(map[string][]byte),
	}
}

// Subscribe registers a new subscriber for topic and returns a buffered channel
// (size 64) that receives JSON-encoded payloads. If a cached value exists for the
// topic it is written to the channel immediately so the caller gets current state
// without waiting for the next poll cycle. The caller must call Unsubscribe when
// done to avoid a goroutine/channel leak.
func (b *Broker) Subscribe(topic string) chan []byte {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	if cached, ok := b.cache[topic]; ok {
		ch <- cached // non-blocking: buffer is 64, channel is brand-new
	}
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

// publishLocked delivers payload to every subscriber of topic, closing and
// removing any whose buffer is full. Must be called with b.mu held.
// Sends are non-blocking (select/default) so holding the lock is safe.
func (b *Broker) publishLocked(topic string, payload []byte) {
	subs := b.subs[topic]
	n := 0
	for _, ch := range subs {
		select {
		case ch <- payload:
			subs[n] = ch
			n++
		default:
			slog.Warn("broker: subscriber slow, closing connection", "topic", topic)
			close(ch)
		}
	}
	// Clear dropped slots to avoid memory leaks, then trim the slice.
	for i := n; i < len(subs); i++ {
		subs[i] = nil
	}
	b.subs[topic] = subs[:n]
}

// Publish JSON-encodes data, updates the per-topic cache, and delivers the
// payload to every current subscriber. The send is non-blocking: if a
// subscriber's buffer is full the subscriber is dropped (its channel closed)
// so the caller is never stalled and the client is forced to reconnect.
func (b *Broker) Publish(topic string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		slog.Error("broker: marshal failed", "topic", topic, "err", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cache[topic] = payload
	b.publishLocked(topic, payload)
}

// PublishNoCache delivers data to current subscribers without updating the cache.
// Use this for transient events (e.g. streaming progress) that a new subscriber
// should not receive after the fact.
func (b *Broker) PublishNoCache(topic string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		slog.Error("broker: marshal failed", "topic", topic, "err", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publishLocked(topic, payload)
}
