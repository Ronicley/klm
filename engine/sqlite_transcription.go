package main

import (
	"context"
	"database/sql"
	"errors"
)

func (st *sqliteSessionTx) transcription() (TranscriptionSettings, error) {
	var s TranscriptionSettings
	err := decodeSQLiteRecord(st.ctx, st.tx, "transcription_settings", "settings", 64<<10, &s)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	if s.Model == "" {
		s.Model = defaultTranscriptionModel
	}
	entries, more, err := readSQLChildren[TranscriptionVocabularyEntry](st.ctx, st.tx, "transcription_vocabulary", "settings", maxVocabularyEntries+1, 1<<20)
	if err != nil {
		return s, err
	}
	if more || len(entries) > maxVocabularyEntries {
		return s, errors.New("Transcription vocabulary exceeds supported capacity.")
	}
	s.Vocabulary = entries
	return s, nil
}
func (a *app) readSQLTranscription(ctx context.Context) (TranscriptionSettings, error) {
	var s TranscriptionSettings
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		s, err = st.transcription()
		return err
	})
	return s, err
}
func (a *app) updateSQLTranscription(ctx context.Context, key, model, language *string, vocabulary *[]TranscriptionVocabularyEntry) (TranscriptionSettings, error) {
	var result TranscriptionSettings
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		var err error
		result, err = st.transcription()
		if err != nil {
			return err
		}
		if key != nil {
			result.APIKey = *key
		}
		if model != nil {
			result.Model = *model
		}
		if language != nil {
			result.Language = *language
		}
		if vocabulary != nil {
			if err := st.clearChildren("transcription_vocabulary", "settings"); err != nil {
				return err
			}
			result.Vocabulary = *vocabulary
			for _, entry := range *vocabulary {
				if err := st.appendScalarChild("transcription_vocabulary", "settings", entry); err != nil {
					return err
				}
			}
		}
		meta := result
		meta.Vocabulary = nil
		return st.putRoot("transcription_settings", "settings", meta)
	})
	return result, err
}
