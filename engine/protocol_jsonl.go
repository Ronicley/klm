package main

import (
	"bytes"
	"context"
	"errors"
	"time"
)

type protocolLineInput struct {
	ctx     context.Context
	dir     string
	harness string
	scope   *protocolContentScope
}

func (l *jsonLines) writeProjected(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		part := data
		if end >= 0 {
			part = data[:end]
		}
		if l.stage == nil {
			l.stage = &protocolFrame{ctx: l.projection.ctx, dir: l.projection.dir}
		}
		l.ingesting.Store(true)
		if l.inputIdle == nil {
			l.inputIdle = time.AfterFunc(30*time.Second, func() { l.inputTimedOut.Store(true); l.cancel() })
		} else {
			l.inputIdle.Reset(30 * time.Second)
		}
		if _, err := l.stage.Write(part); err != nil {
			return 0, l.reject(protocolFrameError(l.projection.harness, "", l.stage.size, err).Error())
		}
		if end < 0 {
			break
		}
		if err := l.flushProjected(); err != nil {
			return 0, err
		}
		data = data[end+1:]
	}
	return n, nil
}

func (l *jsonLines) flushProjected() error {
	if l.inputIdle != nil {
		l.inputIdle.Stop()
		l.inputIdle = nil
	}
	defer l.ingesting.Store(false)
	f := l.stage
	if f == nil {
		return nil
	}
	l.stage = nil
	var data []byte
	var err error
	if f.size > 0 && (f.file != nil || len(bytes.TrimSpace(f.memory)) > 0) {
		if scope := l.activeScope.Load(); scope != nil && l.native != nil {
			var frame map[string]any
			frame, err = f.projectNative(l.projection.harness, 4<<20, scope)
			if err == nil {
				err = l.native(frame)
				if err == nil {
					if l.lastNative != nil && l.lastNative.scope != nil {
						l.lastNative.scope.ack(l.lastNative)
					}
					l.lastNative = f
				}
			}
		} else {
			data, err = f.project(l.projection.harness, 4<<20)
		}
	}
	if !f.retained {
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		return l.reject(err.Error())
	}
	if len(data) > 0 {
		if err := l.consume(data); err != nil {
			return l.reject(err.Error())
		}
	}
	return nil
}

func (l *jsonLines) closeStaging() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inputIdle != nil {
		l.inputIdle.Stop()
		l.inputIdle = nil
	}
	l.ingesting.Store(false)
	if l.stage != nil {
		if l.inputTimedOut.Load() && l.err == nil {
			l.err = protocolFrameError(l.projection.harness, "", l.stage.size, errors.New("incomplete JSONL frame stalled for 30 seconds"))
		}
		l.err = errors.Join(l.err, l.stage.Close())
		l.stage = nil
	}
}
