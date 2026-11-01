package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
)

// A turn owns all native source ranges. Adapters keep small envelopes/handles;
// source bytes never become strings/maps. Closing the turn retires these native
// snapshots after canonical publication, independently of a retained process.
type protocolContentScope struct {
	sync.Mutex
	dir      string
	ctx      context.Context
	closed   bool
	frames   []*protocolFrame
	metadata int
	cache    map[string]protocolCacheEntry
}

type protocolCacheEntry struct {
	frames []*protocolFrame
	bytes  int
}

var protocolResidentBudget struct {
	sync.Mutex
	bytes int
}
var errProtocolResident = errors.New("Engine-wide resident ingestion capacity exhausted (64 MiB shared budget).")

func reserveProtocolResident(bytes int) error {
	protocolResidentBudget.Lock()
	defer protocolResidentBudget.Unlock()
	if bytes > 64<<20-protocolResidentBudget.bytes {
		return errProtocolResident
	}
	protocolResidentBudget.bytes += bytes
	return nil
}

type protocolReadContext struct {
	context.Context
	transport context.Context
}

func (c protocolReadContext) Err() error {
	if err := c.transport.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}

func (s *protocolContentScope) keep(f *protocolFrame, metadata int) error {
	s.Lock()
	defer s.Unlock()
	if s.closed {
		return context.Canceled
	}
	delta := max(0, metadata-f.scopeBytes)
	if delta > 8<<20-s.metadata {
		return errProtocolMetadata
	}
	if err := reserveProtocolResident(delta); err != nil {
		return err
	}
	if !f.retained {
		s.frames = append(s.frames, f)
		f.lease = true
	}
	s.metadata += delta
	f.retained = true
	f.scope = s
	f.scopeBytes += delta
	return nil
}

func (s *protocolContentScope) cacheValue(key string, value any) error {
	if s == nil {
		return nil
	}
	frames := map[*protocolFrame]bool{}
	bytes := protocolValueBytes(value, frames) + len(key) + 64
	s.Lock()
	defer s.Unlock()
	if s.closed {
		return context.Canceled
	}
	old := s.cache[key]
	if value == nil {
		bytes = 0
	}
	if bytes-old.bytes > 8<<20-s.metadata {
		return errProtocolMetadata
	}
	if err := reserveProtocolResident(bytes - old.bytes); err != nil {
		return err
	}
	if s.cache == nil {
		s.cache = map[string]protocolCacheEntry{}
	}
	entry := protocolCacheEntry{bytes: bytes}
	for f := range frames {
		f.cacheRefs++
		entry.frames = append(entry.frames, f)
	}
	for _, f := range old.frames {
		f.cacheRefs--
	}
	s.metadata += bytes - old.bytes
	if value == nil {
		delete(s.cache, key)
	} else {
		s.cache[key] = entry
	}
	s.reapLocked()
	return nil
}

func protocolValueBytes(value any, frames map[*protocolFrame]bool) int {
	switch v := value.(type) {
	case *protocolContent:
		frames[v.frame] = true
		return 96
	case *protocolAccumulatedText:
		frames[v.frame] = true
		return 32
	case string:
		return len(v) + 16
	case map[string]any:
		n := 64
		for key, item := range v {
			n += len(key) + 48 + protocolValueBytes(item, frames)
		}
		return n
	case []any:
		n := 24
		for _, item := range v {
			n += 16 + protocolValueBytes(item, frames)
		}
		return n
	default:
		return 16
	}
}

func (s *protocolContentScope) ack(f *protocolFrame) {
	if s == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	f.lease = false
	s.reapLocked()
}

func (s *protocolContentScope) reapLocked() {
	kept := s.frames[:0]
	for _, f := range s.frames {
		if f.cacheRefs == 0 && !f.lease {
			if f.Close() == nil {
				s.metadata -= f.scopeBytes
				_ = reserveProtocolResident(-f.scopeBytes)
				f.scopeBytes = 0
				continue
			}
		}
		kept = append(kept, f)
	}
	clear(s.frames[len(kept):])
	s.frames = kept
}

func (s *protocolContentScope) Close() error {
	s.Lock()
	defer s.Unlock()
	s.closed = true
	var err error
	for _, f := range s.frames {
		err = errors.Join(err, f.Close())
	}
	s.frames = nil
	_ = reserveProtocolResident(-s.metadata)
	s.metadata = 0
	s.cache = nil
	return err
}

func cacheNativeMap(scope *protocolContentScope, name string, cache map[string]map[string]any, key string, value map[string]any) error {
	if err := scope.cacheValue(name+"/"+key, value); err != nil {
		return err
	}
	cache[key] = value
	return nil
}

type protocolContent struct {
	frame        *protocolFrame
	start, bytes int64
	kind         byte
}

func (*protocolContent) MarshalJSON() ([]byte, error) {
	return nil, errors.New("Large native content requires a streaming reader; it cannot be used as inline decision scope.")
}

func (c *protocolContent) raw() (io.Reader, error) {
	if c.frame.file == nil {
		if c.start < 0 || c.bytes < 0 || c.start > int64(len(c.frame.memory))-c.bytes {
			return nil, errProtocolStagingIO
		}
		return bytes.NewReader(c.frame.memory[c.start : c.start+c.bytes]), nil
	}
	return io.NewSectionReader(c.frame.file, c.start, c.bytes), nil
}

func (c *protocolContent) text() (io.Reader, error) {
	r, err := c.raw()
	if err != nil {
		return nil, err
	}
	if c.kind != '"' {
		return r, nil
	}
	return &jsonStringReader{r: bufio.NewReaderSize(r, protocolChunkSize)}, nil
}

func (f *protocolFrame) retainEnvelope(value map[string]any, scope *protocolContentScope, sources, root bool) error {
	bytes := protocolValueBytes(value, map[*protocolFrame]bool{})
	if bytes > 1<<20 {
		return errProtocolMetadata
	}
	if scope == nil || scope.isClosed() {
		if sources {
			return context.Canceled
		}
		if bytes > protocolChunkSize {
			return errProtocolMetadata
		}
		return nil
	}
	if root && !sources && !f.retained {
		if err := f.Close(); err != nil {
			return err
		}
	}
	return scope.keep(f, bytes*2)
}

func protocolHasContent(value any) bool {
	switch v := value.(type) {
	case *protocolAccumulatedText, *protocolText:
		return true
	case *protocolContent:
		return true
	case map[string]any:
		for _, item := range v {
			if protocolHasContent(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if protocolHasContent(item) {
				return true
			}
		}
	}
	return false
}
