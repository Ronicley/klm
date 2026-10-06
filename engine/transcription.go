package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxTranscriptionAudioSize = int64(25 << 20)
	maxVocabularyEntries      = 200
	transcriptionTimeout      = 2 * time.Minute
)

var transcriptionHTTPClient = &http.Client{
	Timeout: transcriptionTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirect refused")
	},
}

type transcriptionSettingsView struct {
	APIKeyConfigured bool                           `json:"apiKeyConfigured"`
	Model            string                         `json:"model"`
	Language         string                         `json:"language"`
	Vocabulary       []TranscriptionVocabularyEntry `json:"vocabulary"`
}

func transcriptionView(settings TranscriptionSettings) transcriptionSettingsView {
	vocabulary := append([]TranscriptionVocabularyEntry(nil), settings.Vocabulary...)
	if vocabulary == nil {
		vocabulary = []TranscriptionVocabularyEntry{}
	}
	return transcriptionSettingsView{
		APIKeyConfigured: settings.APIKey != "",
		Model:            settings.Model,
		Language:         settings.Language,
		Vocabulary:       vocabulary,
	}
}

func (a *app) getTranscriptionSettings(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	settings := transcriptionView(a.state.Transcription)
	a.mu.Unlock()
	respond(w, http.StatusOK, settings)
}

func validTranscriptionValue(value string, max int, optional bool) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", optional
	}
	return value, utf8.ValidString(value) && utf8.RuneCountInString(value) <= max &&
		strings.IndexFunc(value, unicode.IsControl) == -1
}

func normalizeVocabulary(entries []TranscriptionVocabularyEntry) ([]TranscriptionVocabularyEntry, error) {
	if len(entries) > maxVocabularyEntries {
		return nil, errors.New("Vocabulary can contain at most 200 entries.")
	}
	result := make([]TranscriptionVocabularyEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		term := strings.Join(strings.Fields(entry.Term), " ")
		note := strings.TrimSpace(entry.Note)
		if term == "" && note == "" {
			continue
		}
		if _, ok := validTranscriptionValue(term, 120, false); !ok {
			return nil, errors.New("Vocabulary terms must contain 1 to 120 characters.")
		}
		if _, ok := validTranscriptionValue(note, 500, true); !ok {
			return nil, errors.New("Vocabulary notes must contain at most 500 characters.")
		}
		key := strings.ToLower(term)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, TranscriptionVocabularyEntry{Term: term, Note: note})
	}
	return result, nil
}

func (a *app) updateTranscriptionSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey     json.RawMessage                 `json:"apiKey"`
		Model      *string                         `json:"model"`
		Language   *string                         `json:"language"`
		Vocabulary *[]TranscriptionVocabularyEntry `json:"vocabulary"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.APIKey == nil && body.Model == nil && body.Language == nil && body.Vocabulary == nil {
		fail(w, http.StatusBadRequest, "Provide at least one transcription setting.")
		return
	}

	var apiKey *string
	if body.APIKey != nil {
		value := ""
		if !bytes.Equal(bytes.TrimSpace(body.APIKey), []byte("null")) {
			if err := json.Unmarshal(body.APIKey, &value); err != nil {
				fail(w, http.StatusBadRequest, "API key must be a string or null.")
				return
			}
			value = strings.TrimSpace(value)
			if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 512 || strings.IndexFunc(value, unicode.IsControl) != -1 {
				fail(w, http.StatusBadRequest, "API key must contain 1 to 512 characters, or use null to remove it.")
				return
			}
		}
		apiKey = &value
	}

	var model, language string
	if body.Model != nil {
		var ok bool
		model, ok = validTranscriptionValue(*body.Model, 200, false)
		if !ok {
			fail(w, http.StatusBadRequest, "Model must contain 1 to 200 characters.")
			return
		}
	}
	if body.Language != nil {
		var ok bool
		language, ok = validTranscriptionValue(*body.Language, 100, true)
		if !ok {
			fail(w, http.StatusBadRequest, "Language must contain at most 100 characters.")
			return
		}
	}
	var vocabulary []TranscriptionVocabularyEntry
	if body.Vocabulary != nil {
		var err error
		vocabulary, err = normalizeVocabulary(*body.Vocabulary)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	a.mu.Lock()
	if err := a.commitLocked(func(d *diskState) {
		if apiKey != nil {
			d.Transcription.APIKey = *apiKey
		}
		if body.Model != nil {
			d.Transcription.Model = model
		}
		if body.Language != nil {
			d.Transcription.Language = language
		}
		if body.Vocabulary != nil {
			d.Transcription.Vocabulary = vocabulary
		}
	}); err != nil {
		a.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	response := transcriptionView(a.state.Transcription)
	a.mu.Unlock()
	respond(w, http.StatusOK, response)
}

func (a *app) transcribe(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(transcriptionTimeout + 15*time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, maxTranscriptionAudioSize+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "Audio file must be no larger than 25 MiB.")
		} else {
			fail(w, http.StatusBadRequest, "Invalid multipart upload.")
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "Audio file is required.")
		return
	}
	defer file.Close()
	if header.Size <= 0 {
		fail(w, http.StatusBadRequest, "Audio file is empty.")
		return
	}
	if header.Size > maxTranscriptionAudioSize {
		fail(w, http.StatusRequestEntityTooLarge, "Audio file must be no larger than 25 MiB.")
		return
	}

	a.mu.Lock()
	settings := a.state.Transcription
	a.mu.Unlock()
	if settings.APIKey == "" {
		fail(w, http.StatusConflict, "Configure an OpenAI API key first.")
		return
	}

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	filename := filepath.Base(header.Filename)
	if filename == "." || filename == "" {
		filename = "recording.webm"
	}
	part, err := writer.CreateFormFile("file", filename)
	if err == nil {
		_, err = io.Copy(part, file)
	}
	if err == nil {
		err = writer.WriteField("model", settings.Model)
	}
	if err == nil {
		err = writer.WriteField("response_format", "json")
	}
	if err == nil && settings.Language != "" {
		err = writer.WriteField("language", settings.Language)
	}
	terms := make([]string, 0, len(settings.Vocabulary))
	for _, entry := range settings.Vocabulary {
		if entry.Term != "" {
			terms = append(terms, entry.Term)
		}
	}
	if err == nil && len(terms) > 0 {
		err = writer.WriteField("prompt", "Terms and proper nouns that may appear: "+strings.Join(terms, ", ")+".")
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not prepare the audio upload.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), transcriptionTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/transcriptions", bytes.NewReader(payload.Bytes()))
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not prepare the transcription request.")
		return
	}
	request.Header.Set("Authorization", "Bearer "+settings.APIKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := transcriptionHTTPClient.Do(request)
	if err != nil {
		fail(w, http.StatusBadGateway, "Transcription service unavailable. Retry.")
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		fail(w, http.StatusUnauthorized, "OpenAI rejected the API key.")
		return
	case http.StatusForbidden:
		fail(w, http.StatusForbidden, "OpenAI denied transcription access.")
		return
	case http.StatusRequestEntityTooLarge:
		fail(w, http.StatusRequestEntityTooLarge, "OpenAI rejected the audio file size.")
		return
	case http.StatusTooManyRequests:
		fail(w, http.StatusTooManyRequests, "OpenAI rate limit reached. Retry shortly.")
		return
	default:
		fail(w, http.StatusBadGateway, "Transcription service failed. Retry.")
		return
	}
	var result struct {
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil || strings.TrimSpace(result.Text) == "" {
		fail(w, http.StatusBadGateway, "Transcription service returned an invalid response.")
		return
	}
	respond(w, http.StatusOK, map[string]string{"text": result.Text})
}
