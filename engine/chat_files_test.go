package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestChatFileUpload(t *testing.T) {
	a := &app{dir: t.TempDir()}
	decode := func(name string, data []byte) ([]ChatFile, string, bool, int) {
		t.Helper()
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		if err := writer.WriteField("metadata", `{"clientId":"upload-check","text":"","mentions":[]}`); err != nil {
			t.Fatal(err)
		}
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/api/sessions/s/messages", &payload)
		r.SetPathValue("id", "s")
		r.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		var body messageBody
		files, dir, ok := a.decodeMessage(w, r, &body)
		return files, dir, ok, w.Code
	}
	data := []byte("hello UTF-8: café\n")
	files, dir, ok, status := decode("sample.txt", data)
	if !ok || len(files) != 1 {
		t.Fatalf("upload failed: %d", status)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	stored, err := os.ReadFile(files[0].Path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("uploaded bytes changed: %v", err)
	}
	var prepared submission
	if err := prepareChatFiles(&prepared, files); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var restored submission
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restored.piText(), "café") || !strings.Contains(restored.Context, files[0].Name) || len(restored.openCodeParts()) != 2 || len(restored.codexInput()) != 2 {
		t.Fatal("persisted context is missing from a harness")
	}
	base := messageFingerprint("", "queue", nil, nil)
	fingerprint := chatFileFingerprint(base, files)
	retry := append([]ChatFile(nil), files...)
	retry[0].ID, retry[0].Path = "another-upload", "another-path"
	if chatFileFingerprint(base, retry) != fingerprint {
		t.Fatal("retry identity depends on generated storage paths")
	}
	retry[0].SHA256 = "different-content"
	if chatFileFingerprint(base, retry) == fingerprint {
		t.Fatal("different bytes share an identity")
	}
	for _, tc := range []struct {
		name string
		data []byte
		code int
	}{
		{"too-large.bin", bytes.Repeat([]byte{0}, maxChatFileBytes+1), 413},
		{"disguised.bin", []byte("\x89PNG\r\n\x1a\n"), 400},
	} {
		_, rejectedDir, accepted, code := decode(tc.name, tc.data)
		if accepted || code != tc.code {
			t.Fatalf("%s: accepted=%v status=%d", tc.name, accepted, code)
		}
		if _, err := os.Stat(rejectedDir); !os.IsNotExist(err) {
			t.Fatalf("%s: rejected upload was retained", tc.name)
		}
	}
}

func TestChatImagesReachNativeInputs(t *testing.T) {
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	a := &app{dir: t.TempDir()}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	_ = writer.WriteField("metadata", `{"text":""}`)
	part, err := writer.CreateFormFile("files", "screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pixels.Bytes())
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/api/sessions/s/messages", &upload)
	r.SetPathValue("id", "s")
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	var body messageBody
	files, _, ok := a.decodeMessage(w, r, &body)
	if !ok || len(files) != 1 || files[0].URL == "" {
		t.Fatalf("image rejected: %s", w.Body.String())
	}
	var payload submission
	if err := prepareChatFiles(&payload, files); err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(payload)
	var restored submission
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	opencode := restored.openCodeParts()[1].(map[string]any)
	codex := restored.codexInput()[1].(map[string]any)
	if opencode["type"] != "file" || opencode["mime"] != "image/png" || codex["type"] != "localImage" || codex["path"] != files[0].Path {
		t.Fatal("image not forwarded natively")
	}
	for _, kind := range []string{"prompt", "steer"} {
		pi, err := restored.piMessage("input", kind)
		if err != nil {
			t.Fatal(err)
		}
		image := pi["images"].([]any)[0].(map[string]any)
		data, err := base64.StdEncoding.DecodeString(image["data"].(string))
		if err != nil || !bytes.Equal(data, pixels.Bytes()) || image["mimeType"] != "image/png" {
			t.Fatal("Pi image bytes changed")
		}
	}
	if chatFileFingerprint("base", files) == chatFileFingerprint("base", []ChatFile{{Name: files[0].Name, Size: files[0].Size, Type: files[0].Type, SHA256: "changed"}}) {
		t.Fatal("image bytes omitted from retry identity")
	}
	if err := os.Remove(files[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.piMessage("input", "prompt"); err == nil {
		t.Fatal("missing image silently omitted")
	}
	native := map[string]any{"content": []any{map[string]any{"type": "image", "data": "base64"}, map[string]any{"type": "file", "url": "data:image/png;base64,base64"}, map[string]any{"type": "text", "text": "Keep this"}}}
	omitNativeImageData(native)
	clean, _ := json.Marshal(native)
	if bytes.Contains(clean, []byte("base64")) || !bytes.Contains(clean, []byte("Keep this")) {
		t.Fatal("image bytes leaked into engine metadata or text was lost")
	}
	history := `[{"parts":[{"type":"file","url":"data:image/png;base64,` + strings.Repeat("A", 3<<20) + `"}]}]`
	projected, err := readOpenCodeHistoryJSON(strings.NewReader(history))
	if err != nil || len(projected) > 1024 || !bytes.Contains(projected, []byte("retained by the harness")) {
		t.Fatalf("image history is not bounded: %v", err)
	}
}
