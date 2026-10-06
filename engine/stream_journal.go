package main

import (
	"bufio"
	encodingbinary "encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

// Both transaction and legacy stream records share one contiguous revision log.
// Publication follows Sync; only the checkpoint worker rotates/removes segments.
type streamRecord struct {
	Format      int               `json:"format,omitempty"`
	Transaction []statePatch      `json:"transaction,omitempty"`
	Revision    uint64            `json:"revision"`
	Session     string            `json:"session"`
	Updated     string            `json:"updated"`
	Updates     []streamOperation `json:"updates"`
	Usage       *SessionUsage     `json:"usage,omitempty"`
}

type streamOperation struct {
	Event      Event `json:"event"`
	AppendText bool  `json:"appendText"`
}

func streamJournalPath(dir string) string { return filepath.Join(dir, "stream.journal") }
func sealedJournalPath(dir string) string { return filepath.Join(dir, "stream.sealed") }

// Reconciliation can contain multiple native parts (each independently bounded
// by its adapter), so this must be larger than a single protocol frame.
const maxStreamRecordBytes = 256 << 20

func appendStreamRecord(dir string, record streamRecord) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(b) > maxStreamRecordBytes {
		return errors.New("stream record exceeds 256 MiB")
	}
	f, err := os.OpenFile(streamJournalPath(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	var header [8]byte
	encodingbinary.LittleEndian.PutUint32(header[:4], uint32(len(b)))
	encodingbinary.LittleEndian.PutUint32(header[4:], crc32.ChecksumIEEE(b))
	_, err = f.Write(append(header[:], b...))
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func replayStreamJournal(dir string, state *diskState) error {
	if err := replayJournalFile(sealedJournalPath(dir), state); err != nil {
		return err
	}
	return replayJournalFile(streamJournalPath(dir), state)
}

func replayJournalFile(path string, state *diskState, transform ...func(*streamRecord) error) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var offset int64
	for {
		var header [8]byte
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			break
		} // interrupted tail
		if err != nil {
			return err
		}
		n := encodingbinary.LittleEndian.Uint32(header[:4])
		if n == 0 || n > maxStreamRecordBytes {
			return errors.New("invalid stream journal record size")
		}
		b := make([]byte, n)
		_, err = io.ReadFull(r, b)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
		if crc32.ChecksumIEEE(b) != encodingbinary.LittleEndian.Uint32(header[4:]) {
			return errors.New("stream journal checksum mismatch")
		}
		var record streamRecord
		if err := json.Unmarshal(b, &record); err != nil {
			return err
		}
		if record.Format != 0 && record.Format != 1 {
			return errors.New("unsupported journal format")
		}
		if record.Revision > state.GraphRevision {
			if record.Revision != state.GraphRevision+1 {
				return fmt.Errorf("stream journal revision gap at %d", record.Revision)
			}
			if record.Format == 1 {
				if len(record.Transaction) == 0 || len(record.Updates) != 0 || record.Usage != nil {
					return errors.New("invalid transaction frame")
				}
				for _, clean := range transform {
					if err := clean(&record); err != nil {
						return err
					}
				}
				if err := replayTransaction(state, record.Transaction); err != nil {
					return err
				}
				state.GraphRevision, state.GraphViewRevision = record.Revision, record.Revision
			} else {
				s := state.session(record.Session)
				if s == nil {
					return errors.New("stream journal references missing session")
				}
				if len(record.Updates) == 0 && record.Usage == nil {
					return errors.New("empty stream journal record")
				}
				updates := make([]streamUpdate, len(record.Updates))
				for i, op := range record.Updates {
					updates[i] = streamUpdate{event: op.Event, appendText: op.AppendText}
				}
				for _, change := range streamChanges(s.Events, updates, record.Revision) {
					if change.Index < 0 || change.Index > len(s.Events) {
						return errors.New("invalid stream journal event index")
					}
					if change.Index == len(s.Events) {
						s.Events = append(s.Events, change.Event)
					} else {
						s.Events[change.Index] = change.Event
					}
				}
				s.UpdatedAt = record.Updated
				if record.Usage != nil {
					s.Usage = record.Usage
				}
				state.GraphRevision = record.Revision
			}
		}
		offset += int64(8 + n)
	}
	// Remove only an incomplete final frame. Complete malformed frames are fatal.
	if err := f.Truncate(offset); err != nil {
		return err
	}
	return f.Sync()
}

func streamChanges(events []Event, updates []streamUpdate, revision uint64) []EventChange {
	indices := make(map[string]int, len(events)+len(updates))
	for i, e := range events {
		indices[e.ID] = i
	}
	return streamChangesWithIndex(events, updates, revision, indices)
}

func streamChangesWithIndex(events []Event, updates []streamUpdate, revision uint64, indices map[string]int) []EventChange {
	changed := map[int]Event{}
	next := len(events)
	for _, update := range updates {
		index, found := indices[update.event.ID]
		if !found {
			index = next
			next++
			indices[update.event.ID] = index
			changed[index] = update.event
			continue
		}
		e, ok := changed[index]
		if !ok {
			e = events[index]
		}
		before := e
		if update.event.Title != "" {
			e.Title = update.event.Title
		}
		if update.event.Status != "" {
			e.Status = update.event.Status
		}
		if update.appendText {
			e.Text += update.event.Text
		} else {
			e.Text = update.event.Text
		}
		if len(update.event.Data) > 0 {
			data := make(map[string]any, len(e.Data)+len(update.event.Data))
			for k, v := range e.Data {
				data[k] = v
			}
			for k, v := range update.event.Data {
				data[k] = v
			}
			e.Data = data
		}
		if !reflect.DeepEqual(before, e) {
			changed[index] = e
		}
	}
	result := make([]EventChange, 0, len(changed))
	keys := make([]int, 0, len(changed))
	for index := range changed {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	for _, index := range keys {
		if index < len(events) && reflect.DeepEqual(events[index], changed[index]) {
			continue
		}
		result = append(result, EventChange{Index: index, Revision: revision, Event: changed[index]})
	}
	return result
}

// Rotate under the writer lock, then reconstruct the checkpoint from its durable
// base + sealed segment. No live global snapshot is cloned under app.mu. New
// writes append to the active segment throughout replay, validation and fsync.
func (a *app) checkpointJournal() error {
	a.checkpointMu.Lock()
	defer a.checkpointMu.Unlock()
	a.mu.Lock()
	if a.storageErr != nil {
		err := a.storageErr
		a.mu.Unlock()
		return err
	}
	_, sealedErr := os.Stat(sealedJournalPath(a.dir))
	if errors.Is(sealedErr, os.ErrNotExist) {
		if err := replaceFile(streamJournalPath(a.dir), sealedJournalPath(a.dir)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				a.mu.Unlock()
				return nil
			}
			err = a.failStorageLocked(err)
			a.mu.Unlock()
			return err
		}
	} else if sealedErr != nil {
		err := a.failStorageLocked(sealedErr)
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	state, err := loadState(a.dir)
	if err == nil {
		err = replayJournalFile(sealedJournalPath(a.dir), &state)
	}
	if err == nil {
		err = saveState(a.dir, &state)
	}
	// Crash before removal: startup skips the prefix covered by state.json.
	if err == nil {
		err = os.Remove(sealedJournalPath(a.dir))
	}
	if err != nil {
		a.mu.Lock()
		err = a.failStorageLocked(err)
		a.mu.Unlock()
	}
	return err
}

func (a *app) checkpointStreamJournal() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			_ = a.checkpointJournal()
		}
	}
}
