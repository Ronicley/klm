package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf16"
)

func editedMessageMetadata(e Event, text string) (QueuedMessage, error) {
	var q QueuedMessage
	data, err := json.Marshal(e.Data)
	if err != nil {
		return q, err
	}
	if err = json.Unmarshal(data, &q); err != nil {
		return q, err
	}
	q.Text, q.Mode, q.Status, q.Error = text, "queue", "queued", ""
	// Keep only explicitly attached tokens still present in the edited message.
	sort.Slice(q.Mentions, func(i, j int) bool { return q.Mentions[i].Start < q.Mentions[j].Start })
	mentions, used := []Mention{}, map[int]bool{}
	for _, mention := range q.Mentions {
		token := "@" + mention.Path
		if mention.Kind == "directory" {
			token += "/"
		}
		index := -1
		for offset := 0; offset < len(text); {
			found := strings.Index(text[offset:], token)
			if found < 0 {
				break
			}
			found += offset
			end := found + len(token)
			if !used[found] && (end == len(text) || strings.ContainsRune(" \t\r\n,;:!?)]}", rune(text[end]))) {
				index = found
				break
			}
			offset = end
		}
		if index < 0 {
			continue
		}
		used[index] = true
		mention.Start = len(utf16.Encode([]rune(text[:index])))
		mention.End = mention.Start + len(utf16.Encode([]rune(token)))
		mentions = append(mentions, mention)
	}
	sort.Slice(mentions, func(i, j int) bool { return mentions[i].Start < mentions[j].Start })
	q.Mentions = mentions
	if e.Type == "agent_prompt" && q.Origin == nil {
		return q, errors.New("The original agent attribution is unavailable.")
	}
	return q, nil
}

func (a *app) prepareEditedMessage(ctx context.Context, sessionID, folder, harness string, q QueuedMessage) (submission, error) {
	payload, err := prepareMentions(ctx, folder, harness, q.Text, q.Mentions)
	if err != nil {
		return payload, err
	}
	files := append([]ChatFile(nil), q.Files...)
	for i := range files {
		files[i].Path, err = a.chatFilePath(sessionID, files[i].ID)
		if err != nil {
			return payload, errors.New("An original attachment is unavailable. Restore it before editing this message.")
		}
	}
	err = prepareChatFiles(&payload, files)
	return payload, err
}
