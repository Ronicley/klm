package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type protocolArrayIterator struct {
	content       *protocolContent
	parser        *protocolJSONProjection
	started, done bool
}

func newProtocolArrayIterator(content *protocolContent) (*protocolArrayIterator, error) {
	if content.kind != '[' {
		return nil, errors.New("Native content is not an array.")
	}
	r, err := content.raw()
	if err != nil {
		return nil, err
	}
	counter := &protocolCountingReader{input: r, bytes: content.start}
	return &protocolArrayIterator{content: content, parser: &protocolJSONProjection{ctx: content.frame.ctx, r: bufio.NewReaderSize(counter, protocolChunkSize), counter: counter}}, nil
}

func (i *protocolArrayIterator) next() (*protocolContent, error) {
	if i.done {
		return nil, io.EOF
	}
	p := i.parser
	if !i.started {
		b, err := p.nonSpace()
		if err != nil || b != '[' {
			return nil, errProtocolJSON
		}
		i.started = true
	}
	b, err := p.nonSpace()
	if err != nil {
		return nil, err
	}
	if b == ']' {
		i.done = true
		return nil, io.EOF
	}
	start := p.counter.bytes - int64(p.r.Buffered()) - 1
	if i.content.frame.validated {
		if err := skipValidatedValue(p.ctx, p.r, b); err != nil {
			return nil, err
		}
	} else {
		_ = p.r.UnreadByte()
		if err := p.value(nil, 0, nil); err != nil {
			return nil, err
		}
	}
	end := p.counter.bytes - int64(p.r.Buffered())
	separator, err := p.nonSpace()
	if err != nil {
		return nil, err
	}
	if separator == ']' {
		i.done = true
	} else if separator != ',' {
		return nil, errProtocolJSON
	}
	return &protocolContent{frame: i.content.frame, start: start, bytes: end - start, kind: b}, nil
}

func protocolArrayEach(value any, visit func(any) error) error {
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}
	content, ok := value.(*protocolContent)
	if !ok || content.kind != '[' {
		return errors.New("Harness returned an invalid native array.")
	}
	i, err := newProtocolArrayIterator(content)
	if err != nil {
		return err
	}
	for {
		span, err := i.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var item any
		if span.kind == '{' {
			item, err = span.frame.projectNative("", 1<<20, span.frame.scope, span)
		} else if span.bytes > protocolChunkSize || span.kind == '[' {
			item = span
		} else {
			r, readErr := span.raw()
			if readErr != nil {
				return readErr
			}
			d := json.NewDecoder(r)
			d.UseNumber()
			err = d.Decode(&item)
		}
		if err != nil {
			return err
		}
		if err := visit(item); err != nil {
			return err
		}
	}
}

func protocolField(content *protocolContent, name string) (*protocolContent, error) {
	if content.kind != '{' {
		return nil, nil
	}
	if content.frame.validated {
		var found *protocolContent
		err := walkValidatedFields(content, func(key string, value *protocolContent) error {
			if key != name {
				return nil
			}
			found = value
			return errProtocolFieldFound
		})
		if errors.Is(err, errProtocolFieldFound) {
			return found, nil
		}
		return found, err
	}
	r, err := content.raw()
	if err != nil {
		return nil, err
	}
	var field *protocolContent
	_, _, err = projectProtocolJSONUsing(content.frame.ctx, r, nil, 0, nil, true, func(path []string, start, end int64, kind byte) {
		if len(path) == 1 && path[0] == name {
			field = &protocolContent{frame: content.frame, start: start, bytes: end - start, kind: kind}
		}
	}, content.start, false)
	return field, err
}

// Streaming counterpart of contentText. It preserves the established text/content
// traversal and newline joining, with no resident final array or result string.
type protocolText struct{ open func() (io.Reader, error) }

func nativeText(value any) *protocolText {
	return &protocolText{open: func() (io.Reader, error) { return nativeTextReader(value) }}
}

func displayNativeText(value any) any {
	if protocolHasContent(value) {
		return nativeText(value)
	}
	return contentText(value)
}

func nativeStringPresent(value any) bool {
	switch v := value.(type) {
	case string:
		return v != ""
	case *protocolContent:
		return v.kind == '"' && v.bytes > 2
	}
	return false
}

func nativeField(value any, key string) (any, error) {
	if m, ok := value.(map[string]any); ok {
		return m[key], nil
	}
	if content, ok := value.(*protocolContent); ok && content.kind == '{' {
		return protocolField(content, key)
	}
	return nil, nil
}

func joinNativeValues(values ...any) any {
	plain := true
	var literal strings.Builder
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			plain = false
			break
		}
		literal.WriteString(text)
	}
	if plain {
		return literal.String()
	}
	return &protocolText{open: func() (io.Reader, error) {
		readers := make([]io.Reader, 0, len(values))
		for _, value := range values {
			r, err := nativeTextReader(value)
			if err != nil {
				return nil, err
			}
			readers = append(readers, r)
		}
		return io.MultiReader(readers...), nil
	}}
}

func trimNativeNewline(value any) any {
	if text, ok := value.(string); ok {
		return strings.TrimSuffix(text, "\n")
	}
	return &protocolText{open: func() (io.Reader, error) {
		r, err := nativeTextReader(value)
		if err != nil {
			return nil, err
		}
		return &protocolTrimNewlineReader{r: bufio.NewReaderSize(r, protocolChunkSize)}, nil
	}}
}

type protocolTrimNewlineReader struct{ r *bufio.Reader }

func (r *protocolTrimNewlineReader) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	n, err := r.r.Read(out)
	if n == 0 {
		return n, err
	}
	if _, nextErr := r.r.Peek(1); nextErr == io.EOF {
		if out[n-1] == '\n' {
			n--
		}
		return n, io.EOF
	} else if nextErr != nil {
		return n, nextErr
	}
	return n, err
}

func nativeTextReader(value any) (io.Reader, error) {
	switch v := value.(type) {
	case string:
		return strings.NewReader(v), nil
	case *protocolText:
		return v.open()
	case *protocolAccumulatedText:
		return (&protocolContent{frame: v.frame, bytes: v.frame.size}).raw()
	case *protocolContent:
		switch v.kind {
		case '"':
			return v.text()
		case '[':
			i, err := newProtocolArrayIterator(v)
			if err != nil {
				return nil, err
			}
			return &protocolJoinedText{next: func() (any, error) { return i.next() }}, nil
		case '{':
			text, err := protocolField(v, "text")
			if err != nil {
				return nil, err
			}
			if text != nil && text.kind == '"' && text.bytes > 2 {
				return text.text()
			}
			content, err := protocolField(v, "content")
			if err != nil {
				return nil, err
			}
			if content != nil {
				return nativeTextReader(content)
			}
		}
	case map[string]any:
		if text, ok := v["text"].(string); ok && text != "" {
			return strings.NewReader(text), nil
		}
		if text, ok := v["text"].(*protocolContent); ok && text.kind == '"' && text.bytes > 2 {
			return text.text()
		}
		return nativeTextReader(v["content"])
	case []any:
		index := 0
		return &protocolJoinedText{next: func() (any, error) {
			if index == len(v) {
				return nil, io.EOF
			}
			item := v[index]
			index++
			return item, nil
		}}, nil
	}
	return strings.NewReader(""), nil
}

type protocolJoinedText struct {
	next    func() (any, error)
	current io.Reader
	emitted bool
}

func (r *protocolJoinedText) Read(out []byte) (int, error) {
	for {
		if r.current != nil {
			n, err := r.current.Read(out)
			if err != io.EOF {
				return n, err
			}
			r.current = nil
			if n > 0 {
				return n, nil
			}
		}
		value, err := r.next()
		if err != nil {
			return 0, err
		}
		text, err := nativeTextReader(value)
		if err != nil {
			return 0, err
		}
		peek := bufio.NewReaderSize(text, 4096)
		if _, err := peek.Peek(1); err == io.EOF {
			continue
		} else if err != nil {
			return 0, err
		}
		if r.emitted {
			r.current = io.MultiReader(strings.NewReader("\n"), peek)
		} else {
			r.current = peek
			r.emitted = true
		}
	}
}

func joinNativeText(prefix string, value any) *protocolText {
	return &protocolText{open: func() (io.Reader, error) {
		r, err := nativeTextReader(value)
		if err != nil {
			return nil, err
		}
		return io.MultiReader(strings.NewReader(prefix), r), nil
	}}
}

func nativeJSONText(value any) *protocolText {
	return &protocolText{open: func() (io.Reader, error) {
		// Unknown structured results are reconstructed by the same bounded JSON
		// writer as canonical details, using owned staging instead of a Blob/map.
		if content, ok := value.(*protocolContent); ok {
			return content.raw()
		}
		content := firstProtocolContent(value)
		if content == nil || content.frame.scope == nil {
			return nil, errors.New("Structured native text has no owning source.")
		}
		scope := content.frame.scope
		frame := &protocolFrame{ctx: content.frame.ctx, dir: scope.dir}
		w := bufio.NewWriterSize(frame, protocolChunkSize)
		if err := writeProtocolJSON(w, value); err != nil {
			_ = frame.Close()
			return nil, err
		}
		if err := w.Flush(); err != nil {
			_ = frame.Close()
			return nil, err
		}
		if err := scope.keep(frame, 128); err != nil {
			_ = frame.Close()
			return nil, err
		}
		r, err := (&protocolContent{frame: frame, bytes: frame.size}).raw()
		if err != nil {
			scope.ack(frame)
			return nil, err
		}
		return &protocolOwnedReader{reader: r, release: func() { scope.ack(frame) }}, nil
	}}
}

type protocolOwnedReader struct {
	reader  io.Reader
	release func()
	done    bool
}

func (r *protocolOwnedReader) Read(out []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	n, err := r.reader.Read(out)
	if err != nil {
		r.done = true
		r.release()
	}
	return n, err
}

func firstProtocolContent(value any) *protocolContent {
	switch v := value.(type) {
	case *protocolContent:
		return v
	case map[string]any:
		for _, item := range v {
			if found := firstProtocolContent(item); found != nil {
				return found
			}
		}
	case []any:
		for _, item := range v {
			if found := firstProtocolContent(item); found != nil {
				return found
			}
		}
	}
	return nil
}

func displayNativeJSON(value any) any {
	if protocolHasContent(value) {
		return nativeJSONText(value)
	}
	return codexJSON(value)
}

func nativeTextIsEmpty(value any) (bool, error) {
	if text, ok := value.(string); ok {
		return text == "", nil
	}
	r, err := nativeTextReader(value)
	if err != nil {
		return false, err
	}
	var first [1]byte
	_, err = io.ReadFull(r, first[:])
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

func nativeIDs(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	ids := []string{}
	bytes := 0
	err := protocolArrayEach(value, func(value any) error {
		id, ok := value.(string)
		if !ok || !codexIdentifier(id) {
			return errProtocolJSON
		}
		bytes += len(id) + 32
		if bytes > 1<<20 {
			return errProtocolMetadata
		}
		ids = append(ids, id)
		return nil
	})
	return ids, err
}
