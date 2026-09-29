package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func messageFingerprint(text, mode string, sources []SourceReference, mentions []Mention) string {
	if mode == "" {
		mode = "queue"
	}
	if len(sources) == 0 {
		sources = nil
	}
	if len(mentions) == 0 {
		mentions = nil
	}
	b, _ := json.Marshal(struct {
		Text     string
		Mode     string
		Sources  []SourceReference
		Mentions []Mention
	}{text, mode, sources, mentions})
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

func messageIdentityInUse(d *diskState, id string) bool {
	// QueuePayloads is global, so client IDs must not alias another session's
	// attachments. Keep this check under the same lock as durable acceptance.
	for _, s := range d.Sessions {
		if _, ok := d.AcceptedMessages[s.ID+"/"+id]; ok {
			return true
		}
		for _, q := range s.Queue {
			if q.ID == id {
				return true
			}
		}
		for _, e := range s.Events {
			if e.ID == id {
				return true
			}
		}
	}
	return false
}

func (a *app) indexMessageIDsLocked() {
	if a.messageIDs != nil {
		return
	}
	a.messageIDs = map[string]bool{}
	if a.streamIndexes == nil {
		a.streamIndexes = map[string]map[string]int{}
	}
	for _, s := range a.state.Sessions {
		index := make(map[string]int, len(s.Events))
		for i, e := range s.Events {
			a.messageIDs[e.ID] = true
			index[e.ID] = i
		}
		a.streamIndexes[s.ID] = index
	}
}

func (a *app) messageIdentityInUseLocked(id string) bool {
	a.indexMessageIDsLocked()
	if a.messageIDs[id] {
		return true
	}
	for _, s := range a.state.Sessions {
		if _, ok := a.state.AcceptedMessages[s.ID+"/"+id]; ok {
			return true
		}
		for _, q := range s.Queue {
			if q.ID == id {
				return true
			}
		}
	}
	return false
}
