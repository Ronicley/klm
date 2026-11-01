package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Speech is separate from transcription; an existing OpenAI key can be reused.
func (a *app) getSpeechSettings(w http.ResponseWriter, r *http.Request) {
	key, err := a.speechKey(r.Context())
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"apiKeyConfigured": key != ""})
}

func (a *app) updateSpeechSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey *string `json:"apiKey"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.APIKey == nil {
		fail(w, 400, "Provide an OpenAI API key.")
		return
	}
	key := strings.TrimSpace(*body.APIKey)
	if key == "" || len(key) > 512 || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) != -1 {
		fail(w, 400, "Provide a valid OpenAI API key.")
		return
	}
	a.mu.Lock()
	var err error
	if a.store != nil {
		err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
			return st.putRoot("speech_settings", "settings", key)
		})
	} else {
		err = a.commitLocked(func(d *diskState) { d.SpeechAPIKey = key })
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 503, "Could not save speech settings.")
		return
	}
	respond(w, 200, map[string]any{"apiKeyConfigured": true})
}

var speechHTTPClient = &http.Client{Timeout: 2 * time.Minute}

func (a *app) speechKey(ctx context.Context) (string, error) {
	if a.store != nil {
		var key string
		err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
			err := decodeSQLiteRecord(ctx, tx, "speech_settings", "settings", 64<<10, &key)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if key == "" {
				st := &sqliteSessionTx{ctx: ctx, tx: tx}
				settings, err := st.transcription()
				if err != nil {
					return err
				}
				key = settings.APIKey
			}
			return nil
		})
		return key, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := a.state.SpeechAPIKey
	if key == "" {
		key = a.state.Transcription.APIKey
	}
	return key, nil
}

func (a *app) synthesizeSpeech(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" || !utf8.ValidString(body.Text) || utf8.RuneCountInString(body.Text) > 2500 {
		fail(w, 400, "Speech requires 1 to 2500 characters.")
		return
	}
	switch body.Voice {
	case "marin", "cedar", "coral", "alloy", "sage":
	default:
		fail(w, 400, "Choose a supported voice.")
		return
	}
	key, err := a.speechKey(r.Context())
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if key == "" {
		fail(w, 409, "Configure an OpenAI API key in the reader.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2*time.Minute + 15*time.Second))
	payload, _ := json.Marshal(map[string]string{
		"model": "gpt-4o-mini-tts", "input": body.Text, "voice": body.Voice, "response_format": "mp3",
		"instructions": "Read in the language of the text. Use a natural, warm, clear voice with an unhurried conversational pace. Pause at headings and between paragraphs. Do not add commentary or omit content.",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/speech", bytes.NewReader(payload))
	if err != nil {
		fail(w, 500, "Could not prepare speech request.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	response, err := speechHTTPClient.Do(req)
	if err != nil {
		fail(w, 502, "Could not reach OpenAI. Retry narration.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := "OpenAI could not generate audio. Check your account and retry."
		if response.StatusCode == 401 {
			message = "OpenAI rejected the API key. Update it in the reader."
		}
		if response.StatusCode == 429 {
			message = "OpenAI quota or rate limit reached. Check billing or retry later."
		}
		fail(w, 502, message)
		return
	}
	audio, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil || len(audio) == 0 || len(audio) > 16<<20 {
		fail(w, 502, "Could not receive OpenAI audio. Retry narration.")
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(audio)
}
