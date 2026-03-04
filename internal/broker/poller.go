package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// PollInterval is the server-side data refresh cadence.
// 10s is 3x faster than the previous client-side 30s poll.
const PollInterval = 10 * time.Second

// StartPoller launches the background polling goroutine and returns immediately.
// It polls ZFS CLI commands every PollInterval and publishes changed data to b.
// The goroutine exits when ctx is cancelled.
func StartPoller(ctx context.Context, b *Broker) {
	go runPoller(ctx, b)
}

func runPoller(ctx context.Context, b *Broker) {
	// last holds the most recently published JSON for each topic.
	// Used for change detection: we only broadcast when data actually changes.
	last := make(map[string][]byte)

	// publish marshals data, compares to the last published value for the topic,
	// and publishes to the broker only if the data has changed.
	publish := func(topic string, data any) {
		raw, err := json.Marshal(data)
		if err != nil {
			slog.Error("poller: marshal failed", "topic", topic, "err", err)
			return
		}
		if bytes.Equal(last[topic], raw) {
			return // no change
		}
		last[topic] = raw
		b.Publish(topic, data)
		slog.Debug("poller: published change", "topic", topic)
	}

	tick := time.NewTicker(PollInterval)
	defer tick.Stop()

	// Poll immediately on start so the first SSE client gets data right away
	// instead of waiting up to PollInterval.
	pollOnce(publish)

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller: shutting down")
			return
		case <-tick.C:
			pollOnce(publish)
		}
	}
}

// pollOnce runs one full data collection cycle. Each topic is independent:
// a failure on one does not prevent the others from being published.
func pollOnce(publish func(string, any)) {
	if pools, err := zfs.ListPools(); err == nil {
		publish("pool.query", pools)
	} else {
		slog.Warn("poller: ListPools failed", "err", err)
	}

	if datasets, err := zfs.ListDatasets(); err == nil {
		publish("dataset.query", datasets)
	} else {
		slog.Warn("poller: ListDatasets failed", "err", err)
	}

	if snaps, err := zfs.ListSnapshots(); err == nil {
		publish("snapshot.query", snaps)
	} else {
		slog.Warn("poller: ListSnapshots failed", "err", err)
	}

	// IOStats takes ~1s (one sampling interval) — always last to minimise latency
	// on the other three topics.
	if stats, err := zfs.IOStats(); err == nil {
		publish("iostat", stats)
	} else {
		slog.Warn("poller: IOStats failed", "err", err)
	}

	if users, err := system.ListUsers(); err == nil {
		publish("user.query", users)
	} else {
		slog.Warn("poller: ListUsers failed", "err", err)
	}

	if groups, err := system.ListGroups(); err == nil {
		publish("group.query", groups)
	} else {
		slog.Warn("poller: ListGroups failed", "err", err)
	}
}
