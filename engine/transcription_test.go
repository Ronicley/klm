package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type transcriptionTransport func(*http.Request) (*http.Response, error)

func (f transcriptionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTranscriptionProviders(t *testing.T) {
	settings := TranscriptionSettings{APIKey: "fixture-openai", Model: "legacy-model", Language: "pt", Vocabulary: []TranscriptionVocabularyEntry{{Term: "KLM"}}}
	normalizeTranscriptionSettings(&settings)
	if settings.Provider != "openai" || settings.Model != "legacy-model" || settings.APIKey != "fixture-openai" {
		t.Fatal("legacy OpenAI settings changed")
	}
	settings.OpenRouter.APIKey = "fixture-openrouter"
	view, _ := json.Marshal(transcriptionView(settings))
	if bytes.Contains(view, []byte("fixture-")) {
		t.Fatal("settings view leaked credentials")
	}
	audio := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x80\x3e\x00\x00\x00\x7d\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00")
	previous := transcriptionHTTPClient
	t.Cleanup(func() { transcriptionHTTPClient = previous })
	for _, provider := range []string{"openai", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			settings.Provider = provider
			key, model, _ := transcriptionCredentials(settings)
			called := false
			transcriptionHTTPClient = &http.Client{Transport: transcriptionTransport(func(r *http.Request) (*http.Response, error) {
				called = true
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Fatal("wrong provider key")
				}
				response := `{"text":"Olá KLM"}`
				if provider == "openrouter" {
					if r.URL.String() != "https://openrouter.ai/api/v1/chat/completions" || r.Header.Get("Content-Type") != "application/json" {
						t.Fatal("wrong OpenRouter endpoint or content type")
					}
					body, _ := io.ReadAll(r.Body)
					for _, want := range []string{model, base64.StdEncoding.EncodeToString(audio), `"format":"wav"`, "pt", "KLM"} {
						if !bytes.Contains(body, []byte(want)) {
							t.Fatalf("OpenRouter payload missing %q", want)
						}
					}
					response = `{"choices":[{"message":{"content":"Olá KLM"}}]}`
				} else {
					if r.URL.String() != "https://api.openai.com/v1/audio/transcriptions" {
						t.Fatal("wrong OpenAI endpoint")
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Fatal(err)
					}
					defer r.MultipartForm.RemoveAll()
					if r.FormValue("model") != model || r.FormValue("language") != "pt" {
						t.Fatal("OpenAI fields changed")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
			})}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "recording.wav")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(audio); err != nil {
				t.Fatal(err)
			}
			if err = writer.WriteField("provider", provider); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/transcriptions", &body)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			a := &app{state: diskState{Transcription: settings}}
			a.transcribe(w, r)
			if !called || w.Code != 200 || !strings.Contains(w.Body.String(), "Olá KLM") {
				t.Fatalf("transcription failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
