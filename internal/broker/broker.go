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

// trySend attempts a non-blocking send on ch. Returns true if sent, false if
// the channel was full or already closed. The recover guard handles the race
// where two concurrent Publish calls both detect the same full channel and one
// of them closes it (via Unsubscribe) before the other can send.
func trySend(ch chan []byte, data []byte) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false // channel was closed concurrently; treat as drop
		}
	}()
	select {
	case ch <- data:
		return true
	default:
		return false
	}
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
	// Update cache and snapshot subscriber list under the same lock so a
	// Subscribe call racing with Publish cannot miss the update.
	b.mu.Lock()
	b.cache[topic] = payload
	snapshot := make([]chan []byte, len(b.subs[topic]))
	copy(snapshot, b.subs[topic])
	b.mu.Unlock()

	for _, ch := range snapshot {
		if !trySend(ch, payload) {
			slog.Warn("broker: subscriber slow, closing connection", "topic", topic)
			b.Unsubscribe(topic, ch)
		}
	}
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
	snapshot := make([]chan []byte, len(b.subs[topic]))
	copy(snapshot, b.subs[topic])
	b.mu.Unlock()

	for _, ch := range snapshot {
		if !trySend(ch, payload) {
			slog.Warn("broker: subscriber slow, closing connection", "topic", topic)
			b.Unsubscribe(topic, ch)
		}
	}
}
