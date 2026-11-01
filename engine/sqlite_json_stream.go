package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

func jsonSpace(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return b, nil
		}
	}
}
func jsonStreamString(r *bufio.Reader, consume func(rune) error) error {
	for {
		c, size, err := r.ReadRune()
		if err != nil {
			return err
		}
		if c < 0x20 || c == utf8.RuneError && size == 1 {
			return errProtocolJSON
		}
		if c == '"' {
			return nil
		}
		if c == '\\' {
			b, err := r.ReadByte()
			if err != nil {
				return err
			}
			switch b {
			case '"', '\\', '/':
				c = rune(b)
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'u':
				var digits [4]byte
				if _, err := io.ReadFull(r, digits[:]); err != nil {
					return err
				}
				n, err := strconv.ParseUint(string(digits[:]), 16, 16)
				if err != nil {
					return err
				}
				c = rune(n)
				if c >= 0xd800 && c <= 0xdbff {
					next, err := r.Peek(6)
					if err == nil && next[0] == '\\' && next[1] == 'u' {
						low, e := strconv.ParseUint(string(next[2:]), 16, 16)
						if e == nil && low >= 0xdc00 && low <= 0xdfff {
							_, _ = r.Discard(6)
							c = utf16.DecodeRune(c, rune(low))
						} else {
							c = utf8.RuneError
						}
					} else {
						c = utf8.RuneError
					}
				} else if c >= 0xdc00 && c <= 0xdfff {
					c = utf8.RuneError
				}
			default:
				return errors.New("Invalid JSON escape.")
			}
		}
		if consume != nil {
			if err := consume(c); err != nil {
				return err
			}
		}
	}
}
func jsonSkipValue(r *bufio.Reader, first byte) error {
	if first == '"' {
		return jsonStreamString(r, nil)
	}
	if first != '{' && first != '[' {
		for {
			b, err := r.ReadByte()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if b == ',' || b == '}' || b == ']' || b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				return r.UnreadByte()
			}
		}
	}
	depth := 1
	for depth > 0 {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		switch b {
		case '"':
			if err := jsonStreamString(r, nil); err != nil {
				return err
			}
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return nil
}
func jsonEventStrings(r *bufio.Reader, parent string, visit func(string, rune) error) error {
	b, err := jsonSpace(r)
	if err != nil {
		return err
	}
	if b != '{' {
		return jsonSkipValue(r, b)
	}
	for {
		b, err := jsonSpace(r)
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != '"' {
			return errors.New("Invalid JSON object.")
		}
		var key strings.Builder
		if err := jsonStreamString(r, func(c rune) error {
			if key.Len() < 256 {
				key.WriteRune(c)
			}
			return nil
		}); err != nil {
			return err
		}
		if b, err = jsonSpace(r); err != nil || b != ':' {
			return errors.New("Invalid JSON member.")
		}
		b, err = jsonSpace(r)
		if err != nil {
			return err
		}
		path := parent + key.String()
		if path == "data" && b == '{' {
			if err := r.UnreadByte(); err != nil {
				return err
			}
			if err := jsonEventStrings(r, "data.", visit); err != nil {
				return err
			}
		} else if b == '"' && (path == "text" || path == "data.answer") {
			if err := visit(path, -1); err != nil {
				return err
			}
			if err := jsonStreamString(r, func(c rune) error { return visit(path, c) }); err != nil {
				return err
			}
		} else if err := jsonSkipValue(r, b); err != nil {
			return err
		}
		b, err = jsonSpace(r)
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != ',' {
			return errors.New("Invalid JSON delimiter.")
		}
	}
}

var errSQLExcerptComplete = errors.New("excerpt complete")

func searchSQLiteEvent(ctx context.Context, q sqliteQuery, id, query string) (string, bool, error) {
	reader, err := newSQLiteContentReader(ctx, q, "session_events", id)
	if err != nil {
		return "", false, err
	}
	needle := []rune(strings.ToLower(query))
	if len(needle) == 0 {
		return "", false, errors.New("Supply a nonempty literal query.")
	}
	prefix := make([]int, len(needle))
	for i, j := 1, 0; i < len(needle); i++ {
		for j > 0 && needle[i] != needle[j] {
			j = prefix[j-1]
		}
		if needle[i] == needle[j] {
			j++
		}
		prefix[i] = j
	}
	matched := 0
	found := false
	before := []rune{}
	excerpt := []rune{}
	err = jsonEventStrings(bufio.NewReaderSize(reader, 32<<10), "", func(_ string, c rune) error {
		if c < 0 {
			matched = 0
			before = nil
			if found {
				return errSQLExcerptComplete
			}
			return nil
		}
		if found {
			excerpt = append(excerpt, c)
			if len(excerpt) >= 1024 {
				return errSQLExcerptComplete
			}
			return nil
		}
		before = append(before, c)
		if len(before) > 160+len(needle) {
			copy(before, before[1:])
			before = before[:len(before)-1]
		}
		lower := unicode.ToLower(c)
		for matched > 0 && needle[matched] != lower {
			matched = prefix[matched-1]
		}
		if needle[matched] == lower {
			matched++
		}
		if matched == len(needle) {
			found = true
			excerpt = append(excerpt, before...)
		}
		return nil
	})
	if errors.Is(err, errSQLExcerptComplete) {
		err = nil
	}
	return string(excerpt), found, err
}
