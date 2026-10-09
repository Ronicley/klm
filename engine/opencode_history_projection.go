package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// These are presentation-only tool diff previews, not tool input/output.
// Keep small previews intact; consume oversized strings without retaining them.
const openCodeDiffPreviewLimit = 64 << 10
const openCodeHistoryWireLimit = 256 << 20
const openCodeOmittedDiff = "[Large diff omitted from the KLM view; preserved in OpenCode.]"

var errOpenCodeHistoryWireLimit = errors.New("history page exceeded KLM's 256 MiB input limit")
var errOpenCodeHistoryJSON = errors.New("invalid JSON history response")

// Read one page while bounding retained JSON independently of discarded previews.
// The HTTP request's deadline also bounds reading. No native data is modified.
func readOpenCodeHistoryJSON(input io.Reader) ([]byte, error) {
	wire := &io.LimitedReader{R: input, N: openCodeHistoryWireLimit + 1}
	p := openCodeHistoryProjection{reader: bufio.NewReader(wire)}
	err := p.value(nil, 0)
	if err == nil {
		_, end := p.nonSpace()
		if end != io.EOF {
			err = end
			if err == nil {
				err = errOpenCodeHistoryJSON
			}
		}
	}
	if wire.N == 0 {
		return nil, errOpenCodeHistoryWireLimit
	}
	if err != nil {
		return nil, err
	}
	return p.output, nil
}

type openCodeHistoryProjection struct {
	reader *bufio.Reader
	output []byte
}

func (p *openCodeHistoryProjection) emit(data ...byte) error {
	if len(data) > openCodeResponseLimit-len(p.output) {
		return errOpenCodeResponseTooLarge
	}
	p.output = append(p.output, data...)
	return nil
}

func (p *openCodeHistoryProjection) nonSpace() (byte, error) {
	for {
		b, err := p.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return b, nil
		}
	}
}

func openCodeDiffPreviewPath(path []string) bool {
	if len(path) != 6 && len(path) != 8 {
		return false
	}
	if path[0] != "[]" || path[1] != "parts" || path[2] != "[]" || path[3] != "state" || path[4] != "metadata" {
		return false
	}
	return len(path) == 6 && path[5] == "diff" ||
		len(path) == 8 && path[5] == "files" && path[6] == "[]" && path[7] == "patch"
}

// The opening quote has already been consumed. Validate the entire string,
// including discarded suffixes, without allocating for their content.
func (p *openCodeHistoryProjection) quoted(preview bool) ([]byte, error) {
	limit := openCodeResponseLimit - len(p.output)
	if preview {
		limit = openCodeDiffPreviewLimit
	}
	raw := []byte{'"'}
	omitted, escaped, hexLeft := false, false, 0
	for {
		b, err := p.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if !omitted {
			if len(raw) >= limit {
				if !preview {
					return nil, errOpenCodeResponseTooLarge
				}
				raw, omitted = nil, true
			} else {
				raw = append(raw, b)
			}
		}
		if hexLeft > 0 {
			if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
				return nil, errOpenCodeHistoryJSON
			}
			hexLeft--
			continue
		}
		if escaped {
			escaped = false
			switch b {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				hexLeft = 4
			default:
				return nil, errOpenCodeHistoryJSON
			}
			continue
		}
		switch {
		case b == '"':
			if omitted {
				return json.Marshal(openCodeOmittedDiff)
			}
			return raw, nil
		case b == '\\':
			escaped = true
		case b < 0x20:
			return nil, errOpenCodeHistoryJSON
		}
	}
}

func (p *openCodeHistoryProjection) value(path []string, depth int) error {
	if depth > 256 {
		return errOpenCodeHistoryJSON
	}
	b, err := p.nonSpace()
	if err != nil {
		return err
	}
	switch b {
	case '"':
		imageURL := len(path) == 4 && path[0] == "[]" && path[1] == "parts" && path[2] == "[]" && path[3] == "url"
		raw, err := p.quoted(openCodeDiffPreviewPath(path) || imageURL)
		if err != nil {
			return err
		}
		if imageURL {
			var url string
			_ = json.Unmarshal(raw, &url)
			if strings.HasPrefix(url, "data:image/") || url == openCodeOmittedDiff {
				raw, _ = json.Marshal("[Image content retained by the harness.]")
			}
		}
		return p.emit(raw...)
	case '{', '[':
		if err := p.emit(b); err != nil {
			return err
		}
		close := byte('}')
		if b == '[' {
			close = ']'
		}
		next, err := p.nonSpace()
		if err != nil {
			return err
		}
		if next == close {
			return p.emit(close)
		}
		if err := p.reader.UnreadByte(); err != nil {
			return err
		}
		for {
			key := "[]"
			if b == '{' {
				start, err := p.nonSpace()
				if err != nil {
					return err
				}
				if start != '"' {
					return errOpenCodeHistoryJSON
				}
				raw, err := p.quoted(false)
				if err != nil {
					return err
				}
				if json.Unmarshal(raw, &key) != nil {
					return errOpenCodeHistoryJSON
				}
				if err := p.emit(raw...); err != nil {
					return err
				}
				colon, err := p.nonSpace()
				if err != nil {
					return err
				}
				if colon != ':' {
					return errOpenCodeHistoryJSON
				}
				if err := p.emit(':'); err != nil {
					return err
				}
			}
			if err := p.value(append(path, key), depth+1); err != nil {
				return err
			}
			delim, err := p.nonSpace()
			if err != nil {
				return err
			}
			if delim == close {
				return p.emit(close)
			}
			if delim != ',' {
				return errOpenCodeHistoryJSON
			}
			if err := p.emit(','); err != nil {
				return err
			}
		}
	default:
		if b != 't' && b != 'f' && b != 'n' && b != '-' && (b < '0' || b > '9') {
			return errOpenCodeHistoryJSON
		}
		raw := []byte{b}
		for {
			next, err := p.reader.Peek(1)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			b := next[0]
			if b == ',' || b == ']' || b == '}' || b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				break
			}
			if len(raw) >= openCodeResponseLimit-len(p.output) {
				return errOpenCodeResponseTooLarge
			}
			_, _ = p.reader.ReadByte()
			raw = append(raw, b)
		}
		if !json.Valid(raw) {
			return errOpenCodeHistoryJSON
		}
		return p.emit(raw...)
	}
}
