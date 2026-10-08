package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxChatFileBytes = 25 << 20
const maxChatFiles = 8

type ChatFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Type      string `json:"type"`
	SHA256    string `json:"sha256"`
	Path      string `json:"-"`
	Truncated bool   `json:"truncated,omitempty"`
}

type messageBody struct {
	Text     string            `json:"text"`
	ClientID string            `json:"clientId"`
	Mode     string            `json:"mode"`
	Sources  []SourceReference `json:"sources"`
	Mentions []Mention         `json:"mentions"`
}

// Files remain temporary until durable message acceptance. Retries never share
// a temporary directory, so a failed request cannot remove another's files.
func (a *app) decodeMessage(w http.ResponseWriter, r *http.Request, body *messageBody) (files []ChatFile, dir string, ok bool) {
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "multipart/form-data" {
		return nil, "", decode(w, r, body)
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(2 * time.Minute))
	_ = controller.SetWriteDeadline(time.Now().Add(2*time.Minute + 10*time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, maxChatFileBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "Invalid file upload.")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "metadata" || part.FileName() != "" {
		fail(w, 400, "File uploads require message metadata first.")
		return
	}
	metadataRequest := r.Clone(r.Context())
	metadataRequest.Body = io.NopCloser(part)
	if !decode(w, metadataRequest, body) {
		return
	}
	if err := part.Close(); err != nil {
		fail(w, 400, "Could not read message metadata.")
		return
	}
	defer func() {
		if !ok && dir != "" {
			_ = os.RemoveAll(dir)
		}
	}()
	var total int64
	for {
		part, err = reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(w, 400, "Invalid or incomplete file upload (maximum 25 MiB).")
			return
		}
		name, valid := cleanLabel(part.FileName(), 255)
		if !valid || part.FormName() != "files" || len(files) >= maxChatFiles {
			fail(w, 400, "Upload at most eight files with valid filenames.")
			return
		}
		if dir == "" {
			sessionHash := sha256.Sum256([]byte(r.PathValue("id")))
			root, rootErr := filepath.Abs(filepath.Join(a.dir, "attachments", hex.EncodeToString(sessionHash[:])))
			if rootErr == nil {
				rootErr = os.MkdirAll(root, 0700)
			}
			if rootErr == nil {
				dir, rootErr = os.MkdirTemp(root, "message-")
			}
			if rootErr != nil {
				fail(w, 503, "Cannot store chat attachments.")
				return
			}
		}
		ext := strings.ToLower(filepath.Ext(name))
		if len(ext) > 16 || strings.IndexFunc(ext, func(r rune) bool {
			return r != '.' && !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) >= 0 {
			ext = ".bin"
		}
		file := ChatFile{ID: newID(), Name: name}
		file.Path = filepath.Join(dir, file.ID+ext)
		out, openErr := os.OpenFile(file.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr != nil {
			fail(w, 503, "Cannot store chat attachment.")
			return
		}
		hash := sha256.New()
		file.Size, err = io.Copy(io.MultiWriter(out, hash), io.LimitReader(part, maxChatFileBytes-total+1))
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		total += file.Size
		if total > maxChatFileBytes {
			fail(w, 413, "Files exceed the 25 MiB per-message limit.")
			return
		}
		if err != nil || closeErr != nil || r.Context().Err() != nil {
			fail(w, 400, "File upload interrupted. Retry with the selected files.")
			return
		}
		if err = part.Close(); err != nil {
			fail(w, 400, "File upload interrupted.")
			return
		}
		in, openErr := os.Open(file.Path)
		if openErr != nil {
			fail(w, 503, "Cannot inspect chat attachment.")
			return
		}
		header := make([]byte, 512)
		n, readErr := in.Read(header)
		_ = in.Close()
		if readErr != nil && readErr != io.EOF {
			fail(w, 503, "Cannot inspect chat attachment.")
			return
		}
		file.Type = http.DetectContentType(header[:n])
		declared, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if strings.HasPrefix(file.Type, "image/") || strings.HasPrefix(declared, "image/") || isChatImageExtension(ext) {
			fail(w, 400, "Images are not supported by this file attachment flow.")
			return
		}
		file.SHA256 = hex.EncodeToString(hash.Sum(nil))
		files = append(files, file)
	}
	if len(files) == 0 {
		fail(w, 400, "Select at least one file.")
		return
	}
	ok = true
	return
}

func isChatImageExtension(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico", ".tif", ".tiff", ".avif", ".heic", ".heif", ".jxl":
		return true
	}
	return false
}

func chatFileFingerprint(base string, files []ChatFile) string {
	if len(files) == 0 {
		return base
	}
	intent := make([]ChatFile, len(files))
	for i, file := range files {
		intent[i] = ChatFile{Name: file.Name, Size: file.Size, Type: file.Type, SHA256: file.SHA256}
	}
	encoded, _ := json.Marshal(intent)
	hash := sha256.Sum256(append([]byte(base), encoded...))
	return hex.EncodeToString(hash[:])
}

func prepareChatFiles(s *submission, files []ChatFile) error {
	if len(files) == 0 {
		return nil
	}
	type fileContext struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Type      string `json:"type"`
		Size      int64  `json:"size"`
		Content   string `json:"content,omitempty"`
		Truncated bool   `json:"truncated,omitempty"`
	}
	blocks := make([]fileContext, 0, len(files))
	for i, file := range files {
		block := fileContext{Name: file.Name, Path: file.Path, Type: file.Type, Size: file.Size}
		f, err := os.Open(file.Path)
		if err != nil {
			return fmt.Errorf("%s: Cannot prepare uploaded file.", file.Name)
		}
		// Binary/PDF/other encodings remain available by path, not rejected.
		content, truncated, textErr := readMentionText(f)
		_ = f.Close()
		if textErr == nil {
			block.Content, block.Truncated = content, truncated
			files[i].Truncated = truncated
		}
		blocks = append(blocks, block)
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	s.Context += "\n\nUser-uploaded files (JSON data; paths are on the engine computer; use harness tools to read non-text formats):\n" + string(encoded)
	if len(s.Context) > mentionContextBytes {
		return errors.New("Prepared attachments exceed 200 KiB. Select fewer or smaller text files.")
	}
	return nil
}
