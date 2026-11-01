package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
)

// Compact post-COMMIT scope notices, never payload/history storage. At most one
// pending notice per consumer and 64 consumers; frames cap at 64 KiB (<=4 MiB
// pending engine-wide). A slow consumer gets a scoped reset, not an unbounded
// backlog and never a blocked durable writer. Snapshot handlers register BEFORE
// capturing a SQL read revision and discard already-covered notices themselves.
type sqliteChangeScope struct{ Kind, ID, ProjectID string }
type sqliteChangeNotice struct {
	Kind       string              `json:"kind"`
	Revision   string              `json:"revision"`
	SessionIDs []string            `json:"sessionIds,omitempty"`
	Ordinals   map[string][]string `json:"ordinals,omitempty"`
	Scopes     []string            `json:"scopes,omitempty"`
}
type sqliteChangeSubscription struct {
	scopes    []sqliteChangeScope
	multiplex bool
	notices   chan []byte
}
type sqliteChangeBus struct {
	mu          sync.Mutex
	subscribers map[*sqliteChangeSubscription]bool
}

func (b *sqliteChangeBus) subscribe(scope sqliteChangeScope) (*sqliteChangeSubscription, error) {
	return b.subscribeScopes([]sqliteChangeScope{scope}, false)
}

func (b *sqliteChangeBus) subscribeScopes(scopes []sqliteChangeScope, multiplex bool) (*sqliteChangeSubscription, error) {
	if len(scopes) == 0 || len(scopes) > 64 {
		return nil, errors.New("update scope count exceeds limit")
	}
	for _, scope := range scopes {
		if scope.Kind != "global" && scope.Kind != "project" && scope.Kind != "conversation" {
			return nil, errors.New("invalid update scope")
		}
		if scope.Kind != "global" && scope.ID == "" {
			return nil, errors.New("update scope requires identity")
		}
		if len(scope.ID) > 4096 || len(scope.ProjectID) > 4096 {
			return nil, errors.New("update scope identity exceeds limit")
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers == nil {
		b.subscribers = map[*sqliteChangeSubscription]bool{}
	}
	if len(b.subscribers) >= 64 {
		return nil, errors.New("update subscription capacity reached")
	}
	s := &sqliteChangeSubscription{scopes: scopes, multiplex: multiplex, notices: make(chan []byte, 1)}
	b.subscribers[s] = true
	return s, nil
}

func (b *sqliteChangeBus) unsubscribe(s *sqliteChangeSubscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[s] {
		delete(b.subscribers, s)
		close(s.notices)
	}
}

func (b *sqliteChangeBus) publish(c sqliteSessionChanges) {
	if c.Revision == "" || len(c.Sessions) == 0 && len(c.Records) == 0 && len(c.Invalidated) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	reset, _ := json.Marshal(sqliteChangeNotice{Kind: "reset", Revision: c.Revision})
	for s := range b.subscribers {
		notice := sqliteChangeNotice{Kind: "invalidate", Revision: c.Revision}
		matched := false
		for _, scope := range s.scopes {
			switch scope.Kind {
			case "global": // project/activity invalidation only; never enumerate session IDs.
			case "project":
				if !c.Projects[scope.ID] {
					continue
				}
			case "conversation":
				positions, exists := c.Sessions[scope.ID]
				if !exists && !c.Projects[scope.ProjectID] {
					continue
				}
				if exists && !s.multiplex {
					notice.SessionIDs = []string{scope.ID}
					if len(positions) > 256 {
						notice.Kind = "reset"
					} else {
						values := make([]string, len(positions))
						for i, p := range positions {
							values[i] = strconv.FormatInt(p, 10)
						}
						notice.Ordinals = map[string][]string{scope.ID: values}
					}
				}
			}
			matched = true
			if s.multiplex {
				notice.Scopes = append(notice.Scopes, scope.Kind+"/"+scope.ID)
			}
		}
		if !matched {
			continue
		}
		body, err := json.Marshal(notice)
		if err != nil || len(body) > 64<<10 {
			body = reset
		}
		select {
		case s.notices <- body:
		default:
			select {
			case <-s.notices:
			default:
			}
			select {
			case s.notices <- reset:
			default:
			}
		}
	}
}
