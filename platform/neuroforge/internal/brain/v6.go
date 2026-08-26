package brain

import (
	"context"
	"time"
)

// RunV6Maintenance extends v0.5 maintenance with automatic disk-PQ rebuilds.
// The builder itself is single-flight and uses an atomic directory swap, so
// queries continue against the previous index while a new one is produced.
func (e *Engine) RunV6Maintenance(ctx context.Context) {
	go e.RunV5Maintenance(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if e.store.DiskANNNeedsBuild(now) {
				go func() { _, _ = e.store.RebuildDiskANN() }()
			}
		}
	}
}
