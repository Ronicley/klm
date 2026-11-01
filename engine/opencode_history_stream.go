package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func (h *openCodeHTTP) nativeMessagePage(ctx context.Context, path string, limit int) (frames []*protocolFrame, headers http.Header, result error) {
	ownerContext := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	response, err := h.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	idle := newProtocolIdleReader(response.Body, cancel)
	defer idle.Close()
	defer func() {
		if idle.timedOut.Load() {
			result = errors.New("OpenCode history input stalled for 30 seconds.")
		}
		if result != nil {
			for _, f := range frames {
				_ = f.Close()
			}
			frames = nil
		}
	}()
	counter := &protocolCountingReader{input: idle}
	p := &protocolJSONProjection{ctx: ctx, r: bufio.NewReaderSize(counter, protocolChunkSize), counter: counter}
	b, err := p.nonSpace()
	if err != nil || b != '[' {
		return frames, nil, errProtocolJSON
	}
	for {
		b, err := p.nonSpace()
		if err != nil {
			return frames, nil, err
		}
		if b == ']' {
			break
		}
		if len(frames) == limit || b != '{' {
			return frames, nil, errProtocolJSON
		}
		_ = p.r.UnreadByte()
		f := &protocolFrame{ctx: ctx, dir: h.scope.dir}
		frames = append(frames, f)
		w := bufio.NewWriterSize(f, protocolChunkSize)
		if err := p.value(nil, 0, w); err != nil {
			return frames, nil, err
		}
		if err := w.Flush(); err != nil {
			return frames, nil, err
		}
		b, err = p.nonSpace()
		if err != nil {
			return frames, nil, err
		}
		if b == ']' {
			break
		}
		if b != ',' {
			return frames, nil, errProtocolJSON
		}
		// A comma must be followed by another message, never a closing bracket.
		b, err = p.nonSpace()
		if err != nil || b != '{' {
			return frames, nil, errProtocolJSON
		}
		_ = p.r.UnreadByte()
	}
	if _, err := p.nonSpace(); err != io.EOF {
		return frames, nil, errProtocolJSON
	}
	for _, frame := range frames {
		frame.ctx = ownerContext
	}
	return frames, response.Header, nil
}

func (h *openCodeHTTP) walkNativeMessages(ctx context.Context, sessionID string, baseline any, visit func(map[string]any) error) error {
	limit, before := 64, ""
	cursors := newProtocolIDSet(ctx, h.scope.dir)
	defer cursors.Close()
	seen := newProtocolIDSet(ctx, h.scope.dir)
	defer seen.Close()
	for {
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if before != "" {
			query.Set("before", before)
		}
		frames, headers, err := h.nativeMessagePage(ctx, "/session/"+url.PathEscape(sessionID)+"/message?"+query.Encode(), limit)
		if errors.Is(err, errProtocolStaging) && limit > 1 {
			limit /= 2
			continue
		}
		if err != nil {
			return err
		}
		stop, pageErr := func() (bool, error) {
			defer func() {
				for _, f := range frames {
					if !f.retained {
						_ = f.Close()
					}
				}
			}()
			next := headers.Get("X-Next-Cursor")
			duplicate, err := cursors.Has(next)
			if err != nil {
				return false, err
			}
			if next != "" && (len(frames) == 0 || duplicate) {
				return false, errors.New("OpenCode returned a non-advancing history page.")
			}
			for index := len(frames) - 1; index >= 0; index-- {
				message, err := frames[index].projectNative("opencode-history", 1<<20, h.scope)
				if err != nil {
					return false, err
				}
				info := object(message["info"])
				id := str(info, "id")
				if id == "" || str(info, "sessionID") != sessionID {
					return false, errors.New("OpenCode history contained an invalid or repeated message.")
				}
				if err := seen.Add(id); err != nil {
					return false, err
				}
				old, err := protocolIDSeen(baseline, id)
				if err != nil {
					return false, err
				}
				if old {
					return true, nil
				}
				if err := protocolArrayEach(message["parts"], func(value any) error {
					part := object(value)
					if str(part, "sessionID") != sessionID || str(part, "messageID") != id {
						return errors.New("OpenCode history contained an invalid message part.")
					}
					return nil
				}); err != nil {
					return false, err
				}
				if err := visit(message); err != nil {
					return false, err
				}
				if frames[index].scope != nil {
					frames[index].scope.ack(frames[index])
				}
			}
			if next == "" {
				return true, nil
			}
			if err := cursors.Add(next); err != nil {
				return false, err
			}
			before = next
			return false, nil
		}()
		if pageErr != nil {
			return pageErr
		}
		if stop {
			return nil
		}
	}
}

func (h *openCodeHTTP) nativeMessage(ctx context.Context, sessionID, messageID string) (message map[string]any, release func(), result error) {
	ownerContext := ctx
	ctx, cancel := context.WithCancel(ctx)
	response, err := h.request(ctx, http.MethodGet, "/session/"+url.PathEscape(sessionID)+"/message/"+url.PathEscape(messageID), nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	defer response.Body.Close()
	defer cancel()
	idle := newProtocolIdleReader(response.Body, cancel)
	defer idle.Close()
	frame := &protocolFrame{ctx: ctx, dir: h.scope.dir}
	defer func() {
		if result != nil {
			if frame.retained {
				h.scope.ack(frame)
			} else {
				_ = frame.Close()
			}
		}
	}()
	if _, err := io.CopyBuffer(frame, idle, make([]byte, protocolChunkSize)); err != nil {
		return nil, nil, err
	}
	frame.ctx = ownerContext
	message, err = frame.projectNative("opencode-history", 1<<20, h.scope)
	if err != nil {
		return nil, nil, err
	}
	info := object(message["info"])
	if str(info, "id") != messageID || str(info, "sessionID") != sessionID {
		return nil, nil, errors.New("OpenCode returned a different history message.")
	}
	return message, func() {
		if frame.retained {
			h.scope.ack(frame)
		} else {
			_ = frame.Close()
		}
	}, nil
}
