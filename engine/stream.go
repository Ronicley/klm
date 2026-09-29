package main

import (
	"sync"
	"time"
)

const streamBatchInterval = 40 * time.Millisecond

type streamUpdate struct {
	event      Event
	appendText bool
}

// Only the worker waits for disk while streaming. Enqueue has its own short
// lock so harness readers keep draining even when a state checkpoint is slow.
// Published snapshots still pass through the normal durable commit path.
type streamBatch struct {
	adapter *adapter
	writeMu sync.Mutex
	mu      sync.Mutex
	pending []streamUpdate
	indices map[string]int
	err     error
	stop    chan struct{}
	done    chan struct{}
}

func newStreamBatch(p *adapter) *streamBatch {
	b := &streamBatch{adapter: p, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(b.done)
		ticker := time.NewTicker(streamBatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if b.flush(nil) != nil {
					return
				}
			case <-b.stop:
				_ = b.flush(nil)
				return
			}
		}
	}()
	return b
}

func (b *streamBatch) enqueue(update streamUpdate) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if b.indices == nil {
		b.indices = make(map[string]int)
	}
	if index, ok := b.indices[update.event.ID]; ok {
		previous := &b.pending[index]
		if update.appendText {
			previous.event.Text += update.event.Text
		} else {
			previous.event.Text = update.event.Text
			previous.appendText = false
		}
		if update.event.Title != "" {
			previous.event.Title = update.event.Title
		}
		previous.event.Status = update.event.Status
		for key, value := range update.event.Data {
			previous.event.Data[key] = value
		}
	} else {
		b.indices[update.event.ID] = len(b.pending)
		b.pending = append(b.pending, update)
	}
	return nil
}

// Serialize periodic writes with synchronous boundaries. Swap the pending batch
// before acquiring app.mu; new text never waits for JSON cloning or fsync.
func (b *streamBatch) flush(last *streamUpdate) error {
	if last == nil {
		return b.flushUpdates(nil)
	}
	return b.flushUpdates([]streamUpdate{*last})
}

func (b *streamBatch) flushUpdates(last []streamUpdate) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.mu.Lock()
	if b.err != nil {
		err := b.err
		b.mu.Unlock()
		return err
	}
	updates := b.pending
	b.pending, b.indices = nil, nil
	b.mu.Unlock()
	updates = append(updates, last...)
	if len(updates) == 0 {
		return nil
	}
	err := b.adapter.commitStreamUpdates(updates)
	if err != nil {
		b.mu.Lock()
		b.err = err
		b.mu.Unlock()
		b.adapter.turn.cancel()
	}
	return err
}

func (b *streamBatch) close() error {
	close(b.stop)
	<-b.done
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func (p *adapter) commitStreamUpdates(updates []streamUpdate) error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	a := p.app
	if a.storageErr != nil {
		return a.storageErr
	}
	s := a.state.session(p.id)
	if s == nil {
		return nil
	}
	revision := a.state.GraphRevision + 1
	if a.streamIndexes == nil {
		a.streamIndexes = map[string]map[string]int{}
	}
	indices := a.streamIndexes[p.id]
	if indices == nil {
		indices = make(map[string]int, len(s.Events))
		for i, entry := range s.Events {
			indices[entry.ID] = i
		}
		a.streamIndexes[p.id] = indices
	}
	changes := streamChangesWithIndex(s.Events, updates, revision, indices)
	if len(changes) == 0 {
		return nil
	}
	updated := now()
	record := streamRecord{Revision: revision, Session: p.id, Updated: updated}
	changedIDs := make(map[string]bool, len(changes))
	for _, change := range changes {
		changedIDs[change.Event.ID] = true
	}
	for _, update := range updates {
		if changedIDs[update.event.ID] {
			record.Updates = append(record.Updates, streamOperation{Event: update.event, AppendText: update.appendText})
		}
	}
	if err := appendStreamRecord(a.dir, record); err != nil {
		return a.failStorageLocked(err)
	}
	for _, change := range changes {
		if a.messageIDs != nil {
			a.messageIDs[change.Event.ID] = true
		}
		if change.Index == len(s.Events) {
			s.Events = append(s.Events, change.Event)
		} else {
			s.Events[change.Index] = change.Event
		}
	}
	s.UpdatedAt = updated
	a.state.GraphRevision = revision
	a.appendHistoryChangesLocked(p.id, revision, changes)
	a.notifySessionLocked(p.id)
	return nil
}
