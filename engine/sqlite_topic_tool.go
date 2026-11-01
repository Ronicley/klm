package main

import (
	"errors"
)

func (p *adapter) setSQLTopicLocked(topicID, name, color *string) (any, error) {
	if !p.activeToolLocked() {
		return nil, errors.New("Initial topic assignment requires an active project conversation.")
	}
	var result Topic
	err := p.app.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(p.id)
		if err != nil {
			return err
		}
		if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			return errors.New("Initial topic assignment is available only in normal project conversations.")
		}
		available, err := st.projectAvailable(s.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return errors.New("Project is unavailable.")
		}
		if s.TopicID != "" {
			result, _, err = st.topic(s.ProjectID, s.TopicID)
			return err
		}
		if archived, err := st.effectivelyArchived(s); err != nil {
			return err
		} else if archived {
			return errors.New("Restore this conversation before assigning its topic.")
		}
		if topicID != nil {
			if name != nil || color != nil {
				return errors.New("Supply topicId alone when reusing an active topic.")
			}
			if err := st.moveSessionTopic(&s, *topicID); err != nil {
				return err
			}
			result, _, err = st.topic(s.ProjectID, *topicID)
			if err != nil {
				return err
			}
		} else {
			if name == nil {
				return errors.New("Supply an existing topicId or a new topic name and color.")
			}
			label, ok := cleanLabel(*name, 60)
			if !ok {
				return errors.New("Topic name must contain 1 to 60 characters.")
			}
			chosen := ""
			if color != nil {
				chosen = *color
				if !validTopicColor(chosen) {
					return errors.New("Choose a predefined topic color.")
				}
			} else {
				least := int64(1<<63 - 1)
				for _, candidate := range topicColors {
					var count int64
					if err := st.tx.QueryRowContext(st.ctx, `SELECT count(*) FROM data_topics WHERE owner_id=? AND x_archived<>'true' AND x_color=?`, s.ProjectID, candidate).Scan(&count); err != nil {
						return err
					}
					if count < least {
						chosen, least = candidate, count
					}
				}
			}
			result = Topic{ID: newID(), Name: label, Color: chosen, CreatedAt: now()}
			if err := st.putRecord("topics", sqliteOwnedKey(s.ProjectID, result.ID), s.ProjectID, result.ID, -1, result); err != nil {
				return err
			}
			s.TopicID = result.ID
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
	return result, err
}
