package main

import (
	"crypto/sha256"
	"encoding/base64"
	byteorder "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxChatFileBytes = 25 << 20
const maxChatFiles = 8
const imageJSONLineLimit = (maxChatFileBytes * 4 / 3) + (2 << 20)

// Native image bytes belong in harness history, never in engine events/SSE.
func omitNativeImageData(value any) {
	switch v := value.(type) {
	case map[string]any:
		if str(v, "type") == "image" {
			delete(v, "data")
		}
		if strings.HasPrefix(str(v, "url"), "data:image/") {
			v["url"] = "[Image content retained by the harness.]"
		}
		for _, child := range v {
			omitNativeImageData(child)
		}
	case []any:
		for _, child := range v {
			omitNativeImageData(child)
		}
	}
}

type ChatFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Type      string `json:"type"`
	SHA256    string `json:"sha256"`
	Path      string `json:"-"`
	Truncated bool   `json:"truncated,omitempty"`
	URL       string `json:"url,omitempty"`
}

type messageBody struct {
	Images   []string          `json:"images,omitempty"`
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
			if file.Type != "image/png" && file.Type != "image/jpeg" && file.Type != "image/gif" && file.Type != "image/webp" {
				fail(w, 400, "Attach PNG, JPEG, GIF or WebP images with valid image content.")
				return
			}
			if file.Type != "image/webp" {
				in, err := os.Open(file.Path)
				if err != nil {
					fail(w, 503, "Cannot inspect image attachment.")
					return
				}
				config, _, err := image.DecodeConfig(in)
				_ = in.Close()
				if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
					fail(w, 400, "Image is invalid or exceeds 40 megapixels.")
					return
				}
			} else if n < 20 || int64(byteorder.LittleEndian.Uint32(header[4:8]))+8 != file.Size || (string(header[12:16]) != "VP8 " && string(header[12:16]) != "VP8L" && string(header[12:16]) != "VP8X") {
				fail(w, 400, "WebP image has an invalid or incomplete container.")
				return
			}
			file.URL = "/api/sessions/" + url.PathEscape(r.PathValue("id")) + "/files/" + file.ID
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
		if strings.HasPrefix(file.Type, "image/") {
			s.Images = append(s.Images, uploadedImage{ChatImage: ChatImage{Name: file.Name, MIME: file.Type, Size: int(file.Size)}, Path: file.Path, SHA256: file.SHA256})
			continue
		}
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
	if len(blocks) > 0 {
		s.Context += "\n\nUser-uploaded files (JSON data; paths are on the engine computer; use harness tools to read non-text formats):\n" + string(encoded)
	}
	if len(s.Context) > mentionContextBytes {
		return errors.New("Prepared attachments exceed 200 KiB. Select fewer or smaller text files.")
	}
	return nil
}

type uploadedImage struct {
	ChatImage
	Path   string
	SHA256 string
}

// Pi RPC requires inline images. Load only at dispatch; durable queues store paths.
func (s submission) piMessage(id, kind string) (map[string]any, error) {
	message := map[string]any{"id": id, "type": kind, "message": s.piText()}
	if len(s.Images) == 0 {
		return message, nil
	}
	images := make([]any, 0, len(s.Images))
	for _, image := range s.Images {
		f, err := os.Open(image.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: Cannot read image attachment.", image.Name)
		}
		data, err := io.ReadAll(io.LimitReader(f, maxChatFileBytes+1))
		_ = f.Close()
		if err != nil || len(data) > maxChatFileBytes {
			return nil, fmt.Errorf("%s: Cannot read image attachment.", image.Name)
		}
		if hash := sha256.Sum256(data); len(data) != image.Size || hex.EncodeToString(hash[:]) != image.SHA256 {
			return nil, fmt.Errorf("%s: Stored image content changed. Attach it again.", image.Name)
		}
		images = append(images, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(data), "mimeType": image.MIME})
	}
	message["images"] = images
	return message, nil
}

// Resolve only a file actually owned by this conversation's queue/history.
func (a *app) chatFileContent(w http.ResponseWriter, r *http.Request) {
	id, fileID := r.PathValue("id"), r.PathValue("fileID")
	if !validAuthoringID(fileID) {
		fail(w, 404, "Attachment not found.")
		return
	}
	a.mu.Lock()
	s := a.state.session(id)
	owned := false
	if s != nil {
		for _, q := range s.Queue {
			for _, file := range q.Files {
				if file.ID == fileID && file.URL != "" {
					owned = true
				}
			}
		}
		for _, e := range s.Events {
			data, _ := json.Marshal(e.Data["files"])
			var files []ChatFile
			_ = json.Unmarshal(data, &files)
			for _, file := range files {
				if file.ID == fileID && file.URL != "" {
					owned = true
				}
			}
		}
	}
	a.mu.Unlock()
	if !owned {
		fail(w, 404, "Attachment not found.")
		return
	}
	hash := sha256.Sum256([]byte(id))
	paths, err := filepath.Glob(filepath.Join(a.dir, "attachments", hex.EncodeToString(hash[:]), "message-*", fileID+".*"))
	if err != nil || len(paths) != 1 {
		fail(w, 404, "Attachment is unavailable.")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeFile(w, r, paths[0])
}
