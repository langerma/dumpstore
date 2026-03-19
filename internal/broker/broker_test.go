package broker

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	b := New()
	if b == nil {
		t.Fatal("New() returned nil")
	}
	if b.subs == nil {
		t.Fatal("subs map not initialized")
	}
	if b.cache == nil {
		t.Fatal("cache map not initialized")
	}
}

func TestSubscribeReturnsChannel(t *testing.T) {
	b := New()
	ch := b.Subscribe("pool.query")
	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}
}

func TestSubscribeDeliversCachedValue(t *testing.T) {
	b := New()
	b.Publish("dataset.query", map[string]string{"key": "value"})

	ch := b.Subscribe("dataset.query")
	select {
	case msg := <-ch:
		var got map[string]string
		if err := json.Unmarshal(msg, &got); err != nil {
			t.Fatalf("unmarshal cached value: %v", err)
		}
		if got["key"] != "value" {
			t.Fatalf("cached value = %v, want key=value", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cached value")
	}
}

func TestSubscribeNoCacheNothingDelivered(t *testing.T) {
	b := New()
	ch := b.Subscribe("pool.query")
	select {
	case msg := <-ch:
		t.Fatalf("expected no message, got %s", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestPublishDeliversToAllSubscribers(t *testing.T) {
	tests := []struct {
		name       string
		numSubs    int
		topic      string
		payload    any
		wantString string
	}{
		{
			name:       "two subscribers",
			numSubs:    2,
			topic:      "pool.query",
			payload:    "hello",
			wantString: `"hello"`,
		},
		{
			name:       "three subscribers with object payload",
			numSubs:    3,
			topic:      "iostat",
			payload:    map[string]int{"ops": 42},
			wantString: `{"ops":42}`,
		},
		{
			name:       "single subscriber",
			numSubs:    1,
			topic:      "snapshot.query",
			payload:    []int{1, 2, 3},
			wantString: `[1,2,3]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			channels := make([]chan []byte, tt.numSubs)
			for i := range channels {
				channels[i] = b.Subscribe(tt.topic)
			}

			b.Publish(tt.topic, tt.payload)

			for i, ch := range channels {
				select {
				case msg := <-ch:
					if string(msg) != tt.wantString {
						t.Errorf("subscriber %d: got %s, want %s", i, msg, tt.wantString)
					}
				case <-time.After(time.Second):
					t.Errorf("subscriber %d: timed out", i)
				}
			}
		})
	}
}

func TestPublishUpdatesCache(t *testing.T) {
	b := New()
	b.Publish("pool.query", "first")
	b.Publish("pool.query", "second")

	ch := b.Subscribe("pool.query")
	select {
	case msg := <-ch:
		if string(msg) != `"second"` {
			t.Fatalf("cache not updated: got %s, want %q", msg, `"second"`)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cached value")
	}
}

func TestPublishNoCacheDoesNotUpdateCache(t *testing.T) {
	b := New()

	// Publish a cached value first
	b.Publish("ansible.progress", "cached-value")

	// PublishNoCache should NOT overwrite the cache
	b.PublishNoCache("ansible.progress", "nocache-value")

	// New subscriber should get the original cached value, not the nocache one
	ch := b.Subscribe("ansible.progress")
	select {
	case msg := <-ch:
		if string(msg) != `"cached-value"` {
			t.Fatalf("PublishNoCache overwrote cache: got %s, want %q", msg, `"cached-value"`)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cached value")
	}
}

func TestPublishNoCacheDeliversToSubscribers(t *testing.T) {
	b := New()
	ch := b.Subscribe("ansible.progress")

	b.PublishNoCache("ansible.progress", "progress-msg")

	select {
	case msg := <-ch:
		if string(msg) != `"progress-msg"` {
			t.Fatalf("got %s, want %q", msg, `"progress-msg"`)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestPublishNoCacheEmptyCache(t *testing.T) {
	b := New()

	// Only PublishNoCache, never Publish — cache should remain empty
	b.PublishNoCache("iostat", "data")

	ch := b.Subscribe("iostat")
	select {
	case msg := <-ch:
		t.Fatalf("expected no cached message, got %s", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: no cache entry
	}
}

func TestUnsubscribeRemovesAndCloses(t *testing.T) {
	b := New()
	ch := b.Subscribe("pool.query")

	b.Unsubscribe("pool.query", ch)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after Unsubscribe")
	}

	// Publishing after unsubscribe should not panic or deliver
	b.Publish("pool.query", "after-unsub")

	b.mu.Lock()
	subCount := len(b.subs["pool.query"])
	b.mu.Unlock()
	if subCount != 0 {
		t.Fatalf("expected 0 subscribers after Unsubscribe, got %d", subCount)
	}
}

func TestUnsubscribeUnknownChannelIsNoop(t *testing.T) {
	b := New()
	realCh := b.Subscribe("pool.query")
	unknownCh := make(chan []byte, 8)

	// Should not panic or affect existing subscribers
	b.Unsubscribe("pool.query", unknownCh)

	b.mu.Lock()
	subCount := len(b.subs["pool.query"])
	b.mu.Unlock()
	if subCount != 1 {
		t.Fatalf("expected 1 subscriber after no-op Unsubscribe, got %d", subCount)
	}

	// Real channel should still work
	b.Publish("pool.query", "still-works")
	select {
	case msg := <-realCh:
		if string(msg) != `"still-works"` {
			t.Fatalf("got %s, want %q", msg, `"still-works"`)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out: real subscriber should still receive")
	}
}

func TestUnsubscribeUnknownTopicIsNoop(t *testing.T) {
	b := New()
	ch := make(chan []byte, 8)

	// Should not panic
	b.Unsubscribe("nonexistent.topic", ch)
}

func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	b := New()
	slowCh := b.Subscribe("iostat")
	fastCh := b.Subscribe("iostat")

	// Fill the slow subscriber's buffer (capacity 8)
	for i := 0; i < 8; i++ {
		b.Publish("iostat", i)
	}
	// Drain fast channel
	for i := 0; i < 8; i++ {
		<-fastCh
	}

	// This publish should not block even though slowCh is full
	done := make(chan struct{})
	go func() {
		b.Publish("iostat", "overflow")
		close(done)
	}()

	select {
	case <-done:
		// expected: Publish returned without blocking
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}

	// Fast subscriber should still get the message
	select {
	case msg := <-fastCh:
		if string(msg) != `"overflow"` {
			t.Fatalf("fast subscriber got %s, want %q", msg, `"overflow"`)
		}
	case <-time.After(time.Second):
		t.Fatal("fast subscriber did not receive message")
	}

	// Drain slow channel to clean up
	for len(slowCh) > 0 {
		<-slowCh
	}
}

func TestMultipleTopicsIndependent(t *testing.T) {
	b := New()
	chPool := b.Subscribe("pool.query")
	chSnap := b.Subscribe("snapshot.query")

	b.Publish("pool.query", "pool-data")
	b.Publish("snapshot.query", "snap-data")

	select {
	case msg := <-chPool:
		if string(msg) != `"pool-data"` {
			t.Fatalf("pool subscriber got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("pool subscriber timed out")
	}

	select {
	case msg := <-chSnap:
		if string(msg) != `"snap-data"` {
			t.Fatalf("snapshot subscriber got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot subscriber timed out")
	}

	// Pool subscriber should NOT receive snapshot topic data
	select {
	case msg := <-chPool:
		t.Fatalf("pool subscriber received unexpected message: %s", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestPublishMarshalError(t *testing.T) {
	b := New()
	ch := b.Subscribe("pool.query")

	// Channels and functions cannot be marshaled to JSON
	b.Publish("pool.query", make(chan int))

	// No message should be delivered
	select {
	case msg := <-ch:
		t.Fatalf("expected no message on marshal error, got %s", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestPublishNoCacheMarshalError(t *testing.T) {
	b := New()
	ch := b.Subscribe("pool.query")

	b.PublishNoCache("pool.query", make(chan int))

	select {
	case msg := <-ch:
		t.Fatalf("expected no message on marshal error, got %s", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestConcurrentSubscribePublish(t *testing.T) {
	b := New()
	const goroutines = 50
	const messages = 20

	// Phase 1: concurrent subscribe + publish (no unsubscribe during publish).
	var channels []chan []byte
	var chMu sync.Mutex

	var wg sync.WaitGroup

	// Concurrent subscribers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := b.Subscribe("pool.query")
			chMu.Lock()
			channels = append(channels, ch)
			chMu.Unlock()
		}()
	}
	wg.Wait()

	// Concurrent publishers (subscribers stay open)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messages; j++ {
				b.Publish("pool.query", map[string]int{"id": id, "seq": j})
			}
		}(i)
	}

	// Concurrent PublishNoCache
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messages; j++ {
				b.PublishNoCache("pool.query", map[string]int{"id": id, "seq": j})
			}
		}(i)
	}

	wg.Wait()

	// Phase 2: unsubscribe after all publishers are done.
	for _, ch := range channels {
		b.Unsubscribe("pool.query", ch)
	}
}

func TestValidTopics(t *testing.T) {
	expected := []string{
		"pool.query", "poolstatus", "dataset.query",
		"autosnapshot.query", "snapshot.query", "iostat",
		"user.query", "group.query", "ansible.progress",
	}
	for _, topic := range expected {
		t.Run(topic, func(t *testing.T) {
			if !ValidTopics[topic] {
				t.Fatalf("expected %q to be a valid topic", topic)
			}
		})
	}

	if ValidTopics["nonexistent"] {
		t.Fatal("nonexistent topic should not be valid")
	}
}

func TestPayloadJSONIntegrity(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	tests := []struct {
		name string
		data payload
	}{
		{"simple", payload{Name: "test", Count: 1}},
		{"empty name", payload{Name: "", Count: 0}},
		{"large count", payload{Name: "big", Count: 999999}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			ch := b.Subscribe("dataset.query")

			b.Publish("dataset.query", tt.data)

			select {
			case msg := <-ch:
				var got payload
				if err := json.Unmarshal(msg, &got); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if got != tt.data {
					t.Fatalf("got %+v, want %+v", got, tt.data)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out")
			}
		})
	}
}
