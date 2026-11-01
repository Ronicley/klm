package main

import (
	"context"
	"time"
)

// No UI subscription controls whether pending work is discovered or released.
// Changes are published strictly after each successful commit, between bounded
// batches. Delivery/expiry counters and trigger admission remain in SQL on retry.
func runSQLiteWebhookWorker(ctx context.Context, store *sqliteStore, wake <-chan struct{}, publish func(sqliteSessionChanges)) (result error) {
	defer func() {
		if ctx.Err() != nil {
			result = nil
		}
	}()
	release := func() error {
		for ctx.Err() == nil {
			n, changes, err := store.releaseWebhookBatch(ctx, time.Now().UTC())
			if err != nil {
				return err
			}
			if n == 0 {
				return nil
			}
			if publish != nil {
				publish(changes)
			}
		}
		return nil
	}
	clean := func() error {
		for ctx.Err() == nil {
			n, changes, err := store.cleanWebhookBatch(ctx, time.Now().UTC())
			if err != nil {
				return err
			}
			if n == 0 {
				return nil
			}
			if publish != nil {
				publish(changes)
			}
		}
		return nil
	}
	if err := clean(); err != nil {
		return err
	}
	if err := release(); err != nil {
		return err
	}
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-wake:
			if !ok {
				wake = nil
				continue
			}
			if err := release(); err != nil {
				return err
			}
		case <-poll.C:
			if err := release(); err != nil {
				return err
			}
		case <-cleanup.C:
			if err := clean(); err != nil {
				return err
			}
			if err := release(); err != nil {
				return err
			}
		}
	}
}

func (a *app) sqliteWebhookWorker() {
	err := runSQLiteWebhookWorker(a.ctx, a.store, a.webhookWake, func(changes sqliteSessionChanges) {
		if a.sqlChanges != nil {
			a.sqlChanges.publish(changes)
		}
	})
	if err != nil && a.ctx.Err() == nil {
		a.mu.Lock()
		a.failStorageLocked(err)
		a.mu.Unlock()
	}
}
