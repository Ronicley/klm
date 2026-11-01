package main

import "context"

// Independent bounded stages: existing owned readers/event loops do the work;
// admission creates no goroutine or writer per frame. Channel waiters are FIFO
// and cancellation remains selectable under backpressure.
var protocolParserSlots = make(chan struct{}, 8)
var protocolNormalizeSlots = make(chan struct{}, 8)
var protocolPersistenceSlots = make(chan struct{}, 8)

func acquireProtocolStage(ctx context.Context, slots chan struct{}) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func acquireProtocolParser(ctx context.Context) (func(), error) {
	return acquireProtocolStage(ctx, protocolParserSlots)
}

func (s *protocolContentScope) isClosed() bool {
	if s == nil {
		return true
	}
	s.Lock()
	defer s.Unlock()
	return s.closed
}
