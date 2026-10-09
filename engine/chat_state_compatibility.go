package main

import (
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"time"
)

// Preserve the earlier image-upload schema and accepted attachments. New uploads
// use chat files; existing queue payloads keep their original native image paths.
type ChatImage struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	MIME   string `json:"mime"`
	Size   int    `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type storedImage struct {
	ChatImage
	SessionID string
	SHA256    string
	CreatedAt time.Time
	Accepted  bool
}

func (a *app) getStoredImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("imageID")
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 {
		fail(w, 404, "Image not found.")
		return
	}
	a.mu.Lock()
	image, exists := a.state.Images[id]
	owned := exists && image.ID == id && a.state.session(image.SessionID) != nil
	a.mu.Unlock()
	if !owned {
		fail(w, 404, "Image is unavailable.")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Type", image.MIME)
	http.ServeFile(w, r, filepath.Join(a.dir, "images", id))
}

func (a *app) prepareStoredImagesLocked(sessionID string, ids []string, files []ChatFile) ([]uploadedImage, []ChatImage, error) {
	if len(ids)+len(files) > maxChatFiles {
		return nil, nil, errors.New("Attach at most eight files or images.")
	}
	var total int64
	for _, file := range files {
		total += file.Size
	}
	seen := map[string]bool{}
	var images []uploadedImage
	var metadata []ChatImage
	for _, id := range ids {
		stored, ok := a.state.Images[id]
		decoded, err := hex.DecodeString(id)
		if !ok || err != nil || len(decoded) != 16 || stored.ID != id || stored.SessionID != sessionID || seen[id] {
			return nil, nil, errors.New("Stored image is unavailable for this conversation. Attach it again.")
		}
		seen[id] = true
		total += int64(stored.Size)
		if total > maxChatFileBytes {
			return nil, nil, errors.New("Attachments exceed 25 MiB.")
		}
		path, err := filepath.Abs(filepath.Join(a.dir, "images", id))
		if err != nil {
			return nil, nil, err
		}
		images = append(images, uploadedImage{ChatImage: stored.ChatImage, Path: path, SHA256: stored.SHA256})
		metadata = append(metadata, stored.ChatImage)
	}
	return images, metadata, nil
}
