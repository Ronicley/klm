package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

func delegationOutputDigest(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

func delegationExcerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}

// Budget encoded JSON bytes as well as UTF-8: ordinary multiline/code output
// must not make an eight-member aggregate overflow after escaping.
func delegationSummary(text string, budget int) string {
	low, high := 0, min(len(text), budget)
	for low < high {
		middle := (low + high + 1) / 2
		raw, _ := json.Marshal(delegationExcerpt(text, middle))
		if len(raw) <= budget {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return delegationExcerpt(text, low)
}

type DelegationOutputPage struct {
	InvocationID string `json:"invocationId"`
	Digest       string `json:"digest"`
	Offset       int64  `json:"offset,string"`
	NextOffset   int64  `json:"nextOffset,string"`
	TotalBytes   int64  `json:"totalBytes,string"`
	Content      string `json:"content"`
	HasMore      bool   `json:"hasMore"`
}

func delegationOutputPage(item DelegationInvocationRecord, digest string, offset int64, limit int) (DelegationOutputPage, error) {
	page := DelegationOutputPage{InvocationID: item.ID, Digest: delegationOutputDigest(item.Result), TotalBytes: int64(len(item.Result)), Offset: offset}
	if item.State != DelegationInvocationTerminal || digest != page.Digest || offset < 0 || offset > page.TotalBytes || limit < 1 || limit > 32<<10 {
		return page, errors.New("Invalid, changed or unavailable child output continuation.")
	}
	if offset < page.TotalBytes && !utf8.RuneStart(item.Result[offset]) {
		return page, errors.New("Output offset is not a UTF-8 boundary.")
	}
	end := min(page.TotalBytes, offset+int64(limit))
	for end > offset && !utf8.ValidString(item.Result[offset:end]) {
		end--
	}
	if end == offset && offset < page.TotalBytes {
		return page, errors.New("Output limit cannot contain the next character.")
	}
	page.Content, page.NextOffset, page.HasMore = item.Result[offset:end], end, end < page.TotalBytes
	return page, nil
}
