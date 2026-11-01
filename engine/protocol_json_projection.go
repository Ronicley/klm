package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

const protocolChunkSize = 64 << 10

var errProtocolJSON = errors.New("Harness returned invalid protocol JSON (including ambiguous duplicate keys).")
var errProtocolMetadata = errors.New("Harness protocol metadata exceeded the supported resident budget.")

type projectionAction uint8

const (
	projectionKeep projectionAction = iota
	projectionDiscard
	projectionDiffPreview
	projectionSmallValue
)

// The policy sees decoded keys and exact paths, never substring matches.
type protocolFieldPolicy func([]string) projectionAction

type projectionBuffer struct {
	bytes.Buffer
	limit int
	err   error
}

func (b *projectionBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, b.err
	}
	return b.Buffer.Write(p)
}

// A small SAX-style projector: strings and discarded values are consumed byte by
// byte, not Decoder.Token/RawMessage/map values. The same validator handles kept
// and discarded input. Live duplicate-key bookkeeping is independently bounded.
type protocolJSONProjection struct {
	ctx         context.Context
	r           *bufio.Reader
	policy      protocolFieldPolicy
	tag         string
	keyBytes    int
	readBytes   uint64
	counter     *protocolCountingReader
	observe     func([]string, int64, int64, byte)
	captureType bool
	keyIndex    *protocolKeyIndex
	objectID    int64
	objects     int
}

type protocolCountingReader struct {
	input io.Reader
	bytes int64
}

func (r *protocolCountingReader) Read(p []byte) (int, error) {
	n, err := r.input.Read(p)
	r.bytes += int64(n)
	return n, err
}

func projectProtocolJSON(ctx context.Context, input io.Reader, policy protocolFieldPolicy, limit int, limitErr error, classify bool, observe ...func([]string, int64, int64, byte)) ([]byte, string, error) {
	var observer func([]string, int64, int64, byte)
	if len(observe) > 0 {
		observer = observe[0]
	}
	return projectProtocolJSONUsing(ctx, input, policy, limit, limitErr, classify, observer, 0, true)
}

func projectProtocolJSONUsing(ctx context.Context, input io.Reader, policy protocolFieldPolicy, limit int, limitErr error, classify bool, observe func([]string, int64, int64, byte), origin int64, captureType bool) ([]byte, string, error) {
	counter := &protocolCountingReader{input: input, bytes: origin}
	p := &protocolJSONProjection{ctx: ctx, r: bufio.NewReaderSize(counter, protocolChunkSize), policy: policy, counter: counter}
	p.observe = observe
	p.captureType = captureType
	// Match the existing JSONL BOM convention; an interior BOM is invalid JSON.
	if prefix, _ := p.r.Peek(3); bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = p.r.Discard(3)
	}
	var output *bufio.Writer
	buffer := &projectionBuffer{limit: limit, err: limitErr}
	if !classify {
		output = bufio.NewWriterSize(buffer, protocolChunkSize)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if err := p.value(nil, 0, output); err != nil {
		if ctx.Err() != nil {
			return nil, p.tag, ctx.Err()
		}
		return nil, p.tag, err
	}
	if _, err := p.nonSpace(); err != io.EOF {
		if err == nil {
			err = errProtocolJSON
		}
		return nil, p.tag, err
	}
	if output != nil {
		if err := output.Flush(); err != nil {
			return nil, p.tag, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, p.tag, err
	}
	return buffer.Bytes(), p.tag, nil
}

func (p *protocolJSONProjection) read() (byte, error) {
	p.readBytes++
	if p.readBytes%protocolChunkSize == 0 {
		if err := p.ctx.Err(); err != nil {
			return 0, err
		}
	}
	return p.r.ReadByte()
}

func (p *protocolJSONProjection) nonSpace() (byte, error) {
	for {
		b, err := p.read()
		if err != nil || (b != ' ' && b != '\n' && b != '\r' && b != '\t') {
			return b, err
		}
	}
}

func projectionByte(w *bufio.Writer, b byte) error {
	if w == nil {
		return nil
	}
	return w.WriteByte(b)
}

// Opening quote has been consumed. No string-sized allocation is made here.
func (p *protocolJSONProjection) quoted(w *bufio.Writer) error {
	if err := projectionByte(w, '"'); err != nil {
		return err
	}
	for {
		b, err := p.read()
		if err != nil {
			return errProtocolJSON
		}
		if b < 0x20 {
			return errProtocolJSON
		}
		if b >= utf8.RuneSelf {
			_ = p.r.UnreadByte()
			r, n, err := p.r.ReadRune()
			if err != nil || r == utf8.RuneError && n == 1 {
				return errProtocolJSON
			}
			if w != nil {
				if _, err := w.WriteRune(r); err != nil {
					return err
				}
			}
			continue
		}
		if err := projectionByte(w, b); err != nil {
			return err
		}
		if b == '"' {
			return nil
		}
		if b != '\\' {
			continue
		}
		b, err = p.read()
		if err != nil {
			return errProtocolJSON
		}
		if err := projectionByte(w, b); err != nil {
			return err
		}
		switch b {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			for i := 0; i < 4; i++ {
				b, err = p.read()
				if err != nil || !strings.ContainsRune("0123456789abcdefABCDEF", rune(b)) {
					return errProtocolJSON
				}
				if err := projectionByte(w, b); err != nil {
					return err
				}
			}
		default:
			return errProtocolJSON
		}
	}
}

func (p *protocolJSONProjection) smallString(limit int) ([]byte, error) {
	b := &projectionBuffer{limit: limit, err: errProtocolMetadata}
	w := bufio.NewWriterSize(b, 1024)
	if err := p.quoted(w); err != nil {
		return nil, err
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (p *protocolJSONProjection) value(path []string, depth int, w *bufio.Writer) (result error) {
	if depth > 256 {
		return errProtocolMetadata
	}
	action := projectionKeep
	if p.policy != nil {
		action = p.policy(path)
	}
	if action == projectionSmallValue && w != nil {
		preview := &previewSink{limit: protocolChunkSize}
		out := bufio.NewWriterSize(preview, 4096)
		policy := p.policy
		p.policy = nil
		err := p.value(path, depth, out)
		p.policy = policy
		if err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
		if preview.omitted {
			_, err = w.WriteString("null")
		} else {
			_, err = w.Write(preview.data)
		}
		return err
	}
	if action == projectionDiscard && w != nil {
		if _, err := w.WriteString("null"); err != nil {
			return err
		}
		w = nil
	}
	b, err := p.nonSpace()
	if err != nil {
		return errProtocolJSON
	}
	start := p.counter.bytes - int64(p.r.Buffered()) - 1
	if p.observe != nil {
		defer func() {
			if result == nil {
				p.observe(path, start, p.counter.bytes-int64(p.r.Buffered()), b)
			}
		}()
	}
	if p.captureType && len(path) == 1 && path[0] == "type" {
		if b != '"' {
			return errProtocolJSON
		}
		raw, err := p.smallString(1024)
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &p.tag) != nil {
			return errProtocolJSON
		}
		if w != nil {
			_, err = w.Write(raw)
		}
		return err
	}
	switch b {
	case '"':
		if action == projectionDiffPreview && w != nil {
			// Reuse the existing small-preview behavior without retaining the suffix.
			preview := &previewSink{limit: openCodeDiffPreviewLimit}
			out := bufio.NewWriterSize(preview, 4096)
			if err := p.quoted(out); err != nil {
				return err
			}
			if err := out.Flush(); err != nil {
				return err
			}
			if preview.omitted {
				_, err = w.WriteString(`"` + openCodeOmittedDiff + `"`)
			} else {
				_, err = w.Write(preview.data)
			}
			return err
		}
		return p.quoted(w)
	case '{', '[':
		return p.collection(path, depth, w, b)
	case 't', 'f', 'n':
		word := map[byte]string{'t': "true", 'f': "false", 'n': "null"}[b]
		if err := projectionByte(w, b); err != nil {
			return err
		}
		for i := 1; i < len(word); i++ {
			b, err := p.read()
			if err != nil || b != word[i] {
				return errProtocolJSON
			}
			if err := projectionByte(w, b); err != nil {
				return err
			}
		}
		return nil
	default:
		_ = p.r.UnreadByte()
		return p.number(w)
	}
}

func (p *protocolJSONProjection) collection(path []string, depth int, w *bufio.Writer, open byte) (result error) {
	if err := projectionByte(w, open); err != nil {
		return err
	}
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	b, err := p.nonSpace()
	if err != nil {
		return errProtocolJSON
	}
	if b == close {
		return projectionByte(w, close)
	}
	_ = p.r.UnreadByte()
	keys := map[string]bool{}
	keyBytes := 0
	defer func() { p.keyBytes -= keyBytes }()
	objectID := int64(0)
	spilled := false
	if open == '{' {
		p.objectID++
		objectID = p.objectID
		p.objects++
		defer func() {
			if spilled && p.keyIndex != nil {
				if err := p.keyIndex.drop(objectID); result == nil && err != nil {
					result = err
				}
			}
			p.objects--
			if p.objects == 0 && p.keyIndex != nil {
				if err := p.keyIndex.Close(); result == nil && err != nil {
					result = err
				}
				p.keyIndex = nil
			}
		}()
	}
	for {
		key := "[]"
		if open == '{' {
			b, err := p.nonSpace()
			if err != nil || b != '"' {
				return errProtocolJSON
			}
			raw, err := p.smallString(4096)
			if err != nil {
				return err
			}
			if json.Unmarshal(raw, &key) != nil || keys[key] {
				return errProtocolJSON
			}
			cost := len(key) + 64
			if !spilled && p.keyBytes+cost > 1<<20 {
				if p.keyIndex == nil {
					dir, _ := p.ctx.Value(protocolDirectoryKey{}).(string)
					p.keyIndex, err = newProtocolKeyIndex(p.ctx, dir)
					if err != nil {
						return err
					}
				}
				for existing := range keys {
					if err := p.keyIndex.add(objectID, existing); err != nil {
						return err
					}
				}
				p.keyBytes -= keyBytes
				keyBytes = 0
				keys = nil
				spilled = true
			}
			if spilled {
				if err := p.keyIndex.add(objectID, key); err != nil {
					return err
				}
			} else {
				p.keyBytes += cost
				keyBytes += cost
				keys[key] = true
			}
			if w != nil {
				if _, err := w.Write(raw); err != nil {
					return err
				}
			}
			b, err = p.nonSpace()
			if err != nil || b != ':' {
				return errProtocolJSON
			}
			if err := projectionByte(w, ':'); err != nil {
				return err
			}
		}
		if err := p.value(append(path, key), depth+1, w); err != nil {
			return err
		}
		b, err := p.nonSpace()
		if err != nil {
			return errProtocolJSON
		}
		if b == close {
			return projectionByte(w, close)
		}
		if b != ',' {
			return errProtocolJSON
		}
		if err := projectionByte(w, ','); err != nil {
			return err
		}
	}
}

// JSON number grammar, including arbitrarily long discarded numbers. No token
// buffer and no conversion to float64 (which could change correlation IDs).
func (p *protocolJSONProjection) number(w *bufio.Writer) error {
	peek := func() byte {
		b, _ := p.r.Peek(1)
		if len(b) == 0 {
			return 0
		}
		return b[0]
	}
	take := func() error {
		b, err := p.read()
		if err != nil {
			return errProtocolJSON
		}
		return projectionByte(w, b)
	}
	digit := func(b byte) bool { return b >= '0' && b <= '9' }
	if peek() == '-' {
		if err := take(); err != nil {
			return err
		}
	}
	if peek() == '0' {
		if err := take(); err != nil {
			return err
		}
	} else {
		if peek() < '1' || peek() > '9' {
			return errProtocolJSON
		}
		for digit(peek()) {
			if err := take(); err != nil {
				return err
			}
		}
	}
	if peek() == '.' {
		if err := take(); err != nil {
			return err
		}
		if !digit(peek()) {
			return errProtocolJSON
		}
		for digit(peek()) {
			if err := take(); err != nil {
				return err
			}
		}
	}
	if peek() == 'e' || peek() == 'E' {
		if err := take(); err != nil {
			return err
		}
		if peek() == '+' || peek() == '-' {
			if err := take(); err != nil {
				return err
			}
		}
		if !digit(peek()) {
			return errProtocolJSON
		}
		for digit(peek()) {
			if err := take(); err != nil {
				return err
			}
		}
	}
	return nil
}

type previewSink struct {
	data    []byte
	limit   int
	omitted bool
}

func (s *previewSink) Write(p []byte) (int, error) {
	if !s.omitted {
		if len(p) > s.limit-len(s.data) {
			s.data, s.omitted = nil, true
		} else {
			s.data = append(s.data, p...)
		}
	}
	return len(p), nil
}

func liveProtocolPolicy(harness, typ string) protocolFieldPolicy {
	return func(path []string) projectionAction {
		if harness == "opencode-history" {
			if slices.Equal(path, []string{"info", "summary", "diffs"}) {
				return projectionDiscard
			}
			if openCodeDiffPreviewPath(append([]string{"[]"}, path...)) {
				return projectionDiffPreview
			}
		}
		if harness == "opencode" && typ == "message.updated" && slices.Equal(path, []string{"properties", "info", "summary", "diffs"}) {
			return projectionDiscard
		}
		if harness == "pi" && (typ == "agent_end" && slices.Equal(path, []string{"messages"}) || typ == "turn_end" && (slices.Equal(path, []string{"message"}) || slices.Equal(path, []string{"toolResults"}))) {
			return projectionDiscard
		}
		return projectionKeep
	}
}
