package main

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// Reconstruct canonical normalized details without serializing content handles
// as user-visible values or allocating their entire JSON/string content.
func writeProtocolJSON(w io.Writer, value any) error {
	switch v := value.(type) {
	case *protocolAccumulatedText:
		r, err := nativeTextReader(v)
		if err != nil {
			return err
		}
		return writeProtocolString(w, r)
	case *protocolContent:
		r, err := v.raw()
		if err != nil {
			return err
		}
		_, err = io.CopyBuffer(w, r, make([]byte, protocolChunkSize))
		return err
	case map[string]any:
		if _, err := io.WriteString(w, "{"); err != nil {
			return err
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			if index > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			name, err := json.Marshal(key)
			if err != nil {
				return err
			}
			if _, err := w.Write(name); err != nil {
				return err
			}
			if _, err := io.WriteString(w, ":"); err != nil {
				return err
			}
			if err := writeProtocolJSON(w, v[key]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "}")
		return err
	case []any:
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
		for index, item := range v {
			if index > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			if err := writeProtocolJSON(w, item); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	case string:
		return writeProtocolString(w, strings.NewReader(v))
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(encoded)
		return err
	}
}

func writeProtocolString(w io.Writer, input io.Reader) error {
	if _, err := io.WriteString(w, `"`); err != nil {
		return err
	}
	if err := writeProtocolStringBody(w, input); err != nil {
		return err
	}
	_, err := io.WriteString(w, `"`)
	return err
}

func writeProtocolStringBody(w io.Writer, input io.Reader, observe ...func(rune)) error {
	r := bufio.NewReaderSize(input, protocolChunkSize)
	var chunk strings.Builder
	flush := func() error {
		encoded, err := json.Marshal(chunk.String())
		if err != nil {
			return err
		}
		_, err = w.Write(encoded[1 : len(encoded)-1])
		chunk.Reset()
		return err
	}
	for {
		value, _, err := r.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if len(observe) > 0 {
			observe[0](value)
		}
		chunk.WriteRune(value)
		if chunk.Len() >= protocolChunkSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if chunk.Len() > 0 {
		if err := flush(); err != nil {
			return err
		}
	}
	return nil
}
