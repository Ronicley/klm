package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const mentionBytes = 50 << 10
const mentionContextBytes = 200 << 10

// Start and End are UTF-16 code-unit offsets in the original, untrimmed text.
type Mention struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type MentionPreparation struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Mode      string `json:"mode"` // prepared or native; native does not claim materialization
	Bytes     int    `json:"bytes,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type attachedContent struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type submission struct {
	Text        string
	Context     string
	Files       []map[string]any
	Preparation []MentionPreparation
	Images      []uploadedImage
}

func (s submission) piText() string {
	if s.Context == "" {
		return s.Text
	}
	return s.Text + "\n\n" + s.Context
}

func (s submission) openCodeParts() []any {
	parts := []any{map[string]any{"type": "text", "text": s.Text}}
	if s.Context != "" {
		parts = append(parts, map[string]any{"type": "text", "text": s.Context})
	}
	for _, file := range s.Files {
		parts = append(parts, file)
	}
	for _, image := range s.Images {
		path := filepath.ToSlash(image.Path)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		parts = append(parts, map[string]any{"type": "file", "mime": image.MIME, "filename": image.Name, "url": (&url.URL{Scheme: "file", Path: path}).String()})
	}
	return parts
}

func (s submission) codexInput() []any {
	input := []any{map[string]any{"type": "text", "text": s.Text, "text_elements": []any{}}}
	if s.Context != "" {
		input = append(input, map[string]any{"type": "text", "text": s.Context, "text_elements": []any{}})
	}
	for _, image := range s.Images {
		input = append(input, map[string]any{"type": "localImage", "path": image.Path})
	}
	return input
}

func validRelativePath(path string) bool {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsAny(path, "\\:") || strings.IndexFunc(path, unicode.IsControl) >= 0 || !filepath.IsLocal(filepath.FromSlash(path)) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

func containedPath(root, path string) (string, error) {
	if !validRelativePath(path) {
		return "", errors.New("Use a relative project path without traversal, device names, or alternate streams.")
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", errors.New("Path does not exist or cannot be resolved.")
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("Path resolves outside the project.")
	}
	return abs, nil
}

func localFileURL(path string) string {
	path = filepath.ToSlash(path)
	if strings.HasPrefix(path, "//") {
		host, rest, _ := strings.Cut(strings.TrimPrefix(path, "//"), "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func prepareMentions(ctx context.Context, root, harness, text string, mentions []Mention) (submission, error) {
	result := submission{}
	if len(mentions) == 0 {
		return result, nil
	}
	if len(mentions) > 256 {
		return result, errors.New("Attach at most 256 mention occurrences.")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return result, errors.New("Project directory cannot be resolved.")
	}
	fs, err := os.OpenRoot(root)
	if err != nil {
		return result, errors.New("Project directory cannot be opened.")
	}
	defer fs.Close()
	units := utf16.Encode([]rune(text))
	boundary := func(offset int) bool {
		return offset >= 0 && offset <= len(units) && !(offset > 0 && offset < len(units) && units[offset-1] >= 0xd800 && units[offset-1] <= 0xdbff && units[offset] >= 0xdc00 && units[offset] <= 0xdfff)
	}
	ordered := append([]Mention(nil), mentions...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	ids, paths := map[string]bool{}, map[string]bool{}
	blocks := []attachedContent{}
	end := 0
	for _, m := range ordered {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		token := "@" + m.Path
		if m.Kind == "directory" {
			token += "/"
		}
		if m.ID == "" || len(m.ID) > 100 || ids[m.ID] || (m.Kind != "file" && m.Kind != "directory") || m.Start < end || m.End <= m.Start || !boundary(m.Start) || !boundary(m.End) || string(utf16.Decode(units[m.Start:m.End])) != token {
			return result, errors.New("Mention no longer matches its selected text. Select the path again.")
		}
		ids[m.ID], end = true, m.End
		abs, err := containedPath(root, m.Path)
		if err != nil {
			return result, fmt.Errorf("%s: %w", m.Path, err)
		}
		key := abs
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		// Stat/type validation applies to every occurrence, including aliases.
		rel, _ := filepath.Rel(root, abs)
		info, err := fs.Stat(rel)
		if err != nil || (m.Kind == "directory") != info.IsDir() || (!info.IsDir() && !info.Mode().IsRegular()) {
			return result, fmt.Errorf("%s: Attachment type changed or is unsupported.", m.Path)
		}
		f, err := fs.Open(rel)
		if err != nil {
			return result, fmt.Errorf("%s: Cannot open attachment.", m.Path)
		}
		info, err = f.Stat()
		if err != nil || (m.Kind == "directory") != info.IsDir() || (!info.IsDir() && !info.Mode().IsRegular()) {
			f.Close()
			return result, fmt.Errorf("%s: Attachment type changed or is unsupported.", m.Path)
		}
		if paths[key] {
			f.Close()
			continue
		}
		if len(paths) == 8 {
			f.Close()
			return result, errors.New("Attach at most eight unique paths per message.")
		}
		paths[key] = true
		block := attachedContent{Path: m.Path, Kind: m.Kind}
		if info.IsDir() {
			if harness != "opencode" {
				block.Content, block.Truncated, err = readMentionDirectory(f)
			}
		} else {
			// OpenCode still receives only a native file part. The bounded read here
			// validates supported text before accepting the submission.
			block.Content, block.Truncated, err = readMentionText(f)
		}
		f.Close()
		if err != nil {
			return result, fmt.Errorf("%s: %w", m.Path, err)
		}
		metadata := MentionPreparation{Path: m.Path, Kind: m.Kind, Mode: "native"}
		if harness == "opencode" {
			mime := "text/plain"
			if info.IsDir() {
				mime = "application/x-directory"
			}
			result.Files = append(result.Files, map[string]any{"type": "file", "url": localFileURL(abs), "mime": mime, "filename": m.Path})
		} else {
			metadata.Mode, metadata.Bytes, metadata.Truncated = "prepared", len(block.Content), block.Truncated
			blocks = append(blocks, block)
		}
		result.Preparation = append(result.Preparation, metadata)
	}
	if len(blocks) > 0 {
		encoded, err := json.Marshal(blocks)
		if err != nil {
			return result, err
		}
		// JSON escaping keeps filenames/content from breaking out of their values.
		result.Context = "User-selected attachments (JSON data; directory content lists immediate children only):\n" + string(encoded)
		if len(result.Context) > mentionContextBytes {
			return result, errors.New("Prepared attachments exceed 200 KiB. Select fewer or smaller paths.")
		}
	}
	return result, nil
}

func readMentionText(r io.Reader) (string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, mentionBytes+4))
	if err != nil {
		return "", false, errors.New("Cannot read attachment.")
	}
	truncated := len(data) > mentionBytes
	if truncated {
		data = data[:mentionBytes]
		// Only an incomplete final rune may be removed; malformed UTF-8 is an error.
		start := len(data) - 1
		for start > 0 && !utf8.RuneStart(data[start]) {
			start--
		}
		if !utf8.FullRune(data[start:]) {
			data = data[:start]
		}
	}
	if !utf8.Valid(data) || bytes.IndexFunc(data, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' }) >= 0 {
		return "", false, errors.New("Only UTF-8 text files are supported; binary files, images, and PDFs cannot be attached.")
	}
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", false, errors.New("PDF attachments are not supported. Select a UTF-8 text file.")
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 2000 {
		lines, truncated = lines[:2000], true
	}
	for i, line := range lines {
		if utf8.RuneCountInString(line) > 2000 {
			lines[i], truncated = string([]rune(line)[:2000]), true
		}
	}
	return strings.Join(lines, "\n"), truncated, nil
}

func readMentionDirectory(f *os.File) (string, bool, error) {
	entries, err := f.ReadDir(2001)
	if err != nil && err != io.EOF {
		return "", false, errors.New("Cannot list attachment directory.")
	}
	truncated := len(entries) > 2000
	if truncated {
		entries = entries[:2000]
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		} else if entry.Type()&os.ModeSymlink != 0 {
			name += " [link]"
		}
		// Quote each name so newlines and other delimiters stay unambiguous.
		encoded, _ := json.Marshal(name)
		if out.Len()+len(encoded)+1 > mentionBytes {
			truncated = true
			break
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return out.String(), truncated, nil
}

func validateSubmissionSize(s submission, harness string) error {
	var payload any
	switch harness {
	case "opencode":
		payload = s.openCodeParts()
	case "codex":
		payload = s.codexInput()
	default:
		payload = s.piText()
	}
	data, err := json.Marshal(payload)
	// Reserve space for RPC envelopes, identifiers, settings, and event metadata.
	if err != nil || len(data) > (2<<20)-(16<<10) {
		return errors.New("Message and attached context exceed the transport limit. Reduce the message or attachments.")
	}
	return nil
}
