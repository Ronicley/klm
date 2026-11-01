package main

import (
	"io"
	"sync/atomic"
	"time"
)

// Time only a blocked transport read, never parsing, disk work or backpressure.
type protocolIdleReader struct {
	reader   io.Reader
	timer    *time.Timer
	timedOut atomic.Bool
}

func newProtocolIdleReader(reader io.Reader, cancel func()) *protocolIdleReader {
	r := &protocolIdleReader{reader: reader}
	r.timer = time.AfterFunc(30*time.Second, func() { r.timedOut.Store(true); cancel() })
	r.timer.Stop()
	return r
}

func (r *protocolIdleReader) Read(out []byte) (int, error) {
	r.timer.Reset(30 * time.Second)
	n, err := r.reader.Read(out)
	r.timer.Stop()
	return n, err
}
func (r *protocolIdleReader) Close() { r.timer.Stop() }
