package main

import (
	"context"
	"errors"
	"io"
)

type protocolAccumulatedText struct{ frame *protocolFrame }

func (*protocolAccumulatedText) MarshalJSON() ([]byte, error) {
	return nil, errors.New("Accumulated native text requires streaming content.")
}

func appendNativeText(ctx context.Context, scope *protocolContentScope, current, delta any) (any, error) {
	if before, ok := current.(string); ok {
		if after, ok := delta.(string); ok && len(before)+len(after) <= protocolChunkSize {
			return before + after, nil
		}
	}
	if accumulated, ok := current.(*protocolAccumulatedText); ok {
		r, err := nativeTextReader(delta)
		if err != nil {
			return nil, err
		}
		if _, err := io.CopyBuffer(accumulated.frame, r, make([]byte, protocolChunkSize)); err != nil {
			return nil, err
		}
		return accumulated, nil
	}
	if scope == nil {
		return nil, errors.New("Native text accumulation has no owning turn.")
	}
	f := &protocolFrame{ctx: ctx, dir: scope.dir}
	for _, value := range []any{current, delta} {
		r, err := nativeTextReader(value)
		if err == nil {
			_, err = io.CopyBuffer(f, r, make([]byte, protocolChunkSize))
		}
		if err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if err := scope.keep(f, 128); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &protocolAccumulatedText{frame: f}, nil
}
