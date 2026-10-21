package main

import (
	"sort"
	"time"
)

func (a *app) wakeWebhooksLocked() {
	select {
	case a.webhookWake <- struct{}{}:
	default:
	}
}

// Cleanup applies to every project, including Removed. Each bounded batch is one
// durable deletion, so pending/release and dedupe never outlive their event.
func (a *app) cleanExpiredWebhooks() error {
	for {
		if a.ctx.Err() != nil {
			return nil
		}
		a.mu.Lock()
		if a.closing || a.storageErr != nil {
			err := a.storageErr
			a.mu.Unlock()
			return err
		}
		ids := make([]string, 0, webhookBatchSize)
		at := time.Now().UTC()
		for id, e := range a.state.WebhookEvents {
			if webhookExpired(e, at) {
				ids = append(ids, id)
				if len(ids) == webhookBatchSize {
					break
				}
			}
		}
		if len(ids) == 0 {
			a.mu.Unlock()
			return nil
		}
		err := a.commitWebhooksLocked(func(d *diskState) error {
			for _, id := range ids {
				removeWebhookEvent(d, id)
			}
			return nil
		})
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

func (a *app) releasePendingWebhooks() {
	for {
		if a.ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		if a.closing || a.storageErr != nil {
			a.mu.Unlock()
			return
		}
		pending := make([]WebhookPending, 0, len(a.state.WebhookPending))
		at := time.Now().UTC()
		for id, p := range a.state.WebhookPending {
			e, exists := a.state.WebhookEvents[id]
			project := a.state.project(e.ProjectID)
			if exists && project != nil && !project.Removed && !webhookExpired(e, at) {
				pending = append(pending, p)
			}
		}
		if len(pending) == 0 {
			a.mu.Unlock()
			return
		}
		sort.Slice(pending, func(i, j int) bool { return pending[i].ReceivedSequence < pending[j].ReceivedSequence })
		pending = pending[:min(webhookBatchSize, len(pending))]
		if webhookSequenceAvailable(a.state.WebhookReleaseSequence, len(pending)) != nil {
			a.mu.Unlock()
			return
		}
		err := a.commitWebhooksLocked(func(d *diskState) error {
			for _, p := range pending {
				e := d.WebhookEvents[p.EventID]
				// Clamp only for wall-clock rollback; release cannot precede receipt.
				emitted := maxWebhookTimestamp(e.ReceivedAt, at)
				d.WebhookReleaseSequence++
				d.WebhookReleases[p.EventID] = WebhookRelease{EventID: p.EventID, ReleaseSequence: d.WebhookReleaseSequence, EmittedAt: emitted}
				delete(d.WebhookPending, p.EventID)
			}
			return nil
		})
		a.mu.Unlock()
		if err != nil {
			return
		}
		// Unlock between batches; fallback/wake never owns delivery correctness.
	}
}

func (a *app) webhookWorker() {
	poll := time.NewTicker(time.Second)
	cleanup := time.NewTicker(time.Minute)
	defer poll.Stop()
	defer cleanup.Stop()
	a.releasePendingWebhooks()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.webhookWake:
			a.releasePendingWebhooks()
		case <-poll.C:
			a.releasePendingWebhooks()
		case <-cleanup.C:
			if a.cleanExpiredWebhooks() == nil {
				a.releasePendingWebhooks()
			}
		}
	}
}
