package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestJSONLinesLongStream(t *testing.T) {
	frame := []byte(`{"type":"tool_execution_update","partialResult":{"content":[{"type":"text","text":"` + strings.Repeat("x", 50<<10) + `"}]}}` + "\n")
	consumed := 0
	lines := &jsonLines{
		cancel: func() { t.Fatal("valid long stream was cancelled") },
		consume: func(line []byte) error {
			if !bytes.Equal(line, frame[:len(frame)-1]) {
				t.Fatal("frame content changed")
			}
			consumed++
			return nil
		},
	}
	// Repeated snapshots exceed the former 32 MiB quota without retaining them.
	for i := 0; i < 800; i++ {
		if n, err := lines.Write(frame); err != nil || n != len(frame) {
			t.Fatalf("frame %d: wrote %d bytes: %v", i, n, err)
		}
	}
	if err := lines.flush(); err != nil || consumed != 800 || len(lines.pending) != 0 {
		t.Fatalf("stream did not drain: frames=%d pending=%d err=%v", consumed, len(lines.pending), err)
	}
}

func TestJSONLinesOversizedFragmentedLine(t *testing.T) {
	cancelled := false
	lines := &jsonLines{
		cancel:  func() { cancelled = true },
		consume: func([]byte) error { t.Fatal("oversized line was delivered"); return nil },
	}
	chunk := bytes.Repeat([]byte("x"), 4<<10)
	for i := 0; i < 1024; i++ {
		if _, err := lines.Write(chunk); err != nil {
			t.Fatalf("line rejected before reaching 4 MiB: %v", err)
		}
	}
	_, err := lines.Write([]byte("x\n"))
	if err == nil || err.Error() != "Harness JSON line exceeded the 4 MiB limit." || !cancelled {
		t.Fatalf("missing per-line protection: cancelled=%t err=%v", cancelled, err)
	}
	if _, nextErr := lines.Write([]byte("{}\n")); nextErr != err {
		t.Fatal("reader resumed after a fatal protocol error")
	}
}

func TestJSONLinesConsumerFailure(t *testing.T) {
	cancelled, consumed := false, 0
	failure := errors.New("consumer stopped")
	lines := &jsonLines{
		cancel: func() { cancelled = true },
		consume: func([]byte) error {
			consumed++
			return failure
		},
	}
	if _, err := lines.Write([]byte("{}\n{}\n")); err == nil || err.Error() != failure.Error() || !cancelled || consumed != 1 {
		t.Fatalf("consumer failure was not propagated: cancelled=%t frames=%d err=%v", cancelled, consumed, err)
	}
}
