package main

import (
	"bytes"
	"encoding/json"
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
