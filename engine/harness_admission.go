package main

import (
	"time"
)

// Consultation deadlines are runtime state, just like the installer admission
// gate. Restart already interrupts pending consultations; this never replays
// them or rewrites their attributed creation/receipt timestamps.
func (a *app) consultationDeadlineLocked(id, created string) time.Time {
	if deadline, ok := a.harnessConsultationDeadlines[id]; ok {
		return deadline
	}
	at, _ := time.Parse(time.RFC3339Nano, created)
	return at.Add(10 * time.Minute)
}

func (a *app) consultationExpiredLocked(c Consultation, at time.Time) bool {
	return !at.Before(a.consultationDeadlineLocked(c.ID, c.CreatedAt))
}

// Called before releasing admission, with app.mu held. Only requests that were
// still eligible at suspension keep their remaining timeout budget.
func (a *app) endHarnessAdmissionPauseLocked(at time.Time) error {
	start := a.harnessAdmissionStarted
	if start.IsZero() {
		return nil
	}
	deadlines := map[string]time.Time{}
	pause := func(id, created string) {
		deadline := a.consultationDeadlineLocked(id, created)
		if !deadline.After(start) {
			return
		}
		from := start
		if createdAt, err := time.Parse(time.RFC3339Nano, created); err == nil && createdAt.After(from) {
			from = createdAt
		}
		if at.After(from) {
			deadline = deadline.Add(at.Sub(from))
		}
		deadlines[id] = deadline
	}
	if a.store != nil {
		after := ""
		for {
			rows, err := a.store.readers.QueryContext(a.ctx, `SELECT id,created_at FROM data_consultations WHERE status='queued' AND id>? ORDER BY id LIMIT 100`, after)
			if err != nil {
				return err
			}
			count := 0
			for rows.Next() {
				var id, created string
				if err := rows.Scan(&id, &created); err != nil {
					rows.Close()
					return err
				}
				pause(id, created)
				after = id
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if count < 100 {
				break
			}
		}
	} else {
		for _, c := range a.state.Consultations {
			if c.Status == "queued" {
				pause(c.ID, c.CreatedAt)
			}
		}
	}
	a.harnessConsultationDeadlines = deadlines
	a.harnessAdmissionStarted = time.Time{}
	return nil
}

func (a *app) resumeHarnessAdmissions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.harnessInstalling || a.closing || a.storageErr != nil || a.ctx.Err() != nil {
		return
	}
	// These arbiters already own FIFO/archive/recovery eligibility. In particular
	// paused/uncertain queues and interrupted recovery work are never auto-replayed.
	a.scheduleLinkedLocked()
	a.scheduleAutomationsLocked()
	a.scheduleAutomationRecoveryLocked()
}
