package main

import (
	"context"
	"errors"
)

// Metadata-only recovery seams. Startup supplies recoverDelegationRuntime before
// ordinary/graph recovery and readiness. A nil hook never invents interruption,
// drainage or replay of an invocation.
type DelegationRecoveryHook func(context.Context, *sqliteStore) error

func (s *sqliteStore) pendingDelegationRecovery(ctx context.Context) (bool, error) {
	var pending bool
	err := s.readers.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_delegation_batches WHERE x_state<>'settled' OR x_delivery IN ('pending','sending')) OR EXISTS(SELECT 1 FROM data_delegation_invocations WHERE x_state<>'terminal') OR EXISTS(SELECT 1 FROM data_delegation_calls WHERE x_delivery IN ('pending','sending'))`).Scan(&pending)
	return pending, err
}

func (s *sqliteStore) delegationRecoveryPage(ctx context.Context, after string, limit int) ([]DelegationBatchRecord, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("Delegation recovery page limit must be 1 to 100.")
	}
	ids, err := sqliteQueryIDs(ctx, s.readers, `SELECT b.id FROM data_delegation_batches b WHERE (x_state<>'settled' OR x_delivery IN ('pending','sending') OR EXISTS(SELECT 1 FROM data_delegation_calls c WHERE c.owner_id=b.id AND c.x_delivery IN ('pending','sending'))) AND b.id>? ORDER BY b.id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	items := []DelegationBatchRecord{}
	for _, id := range ids {
		var item DelegationBatchRecord
		if err := decodeSQLiteRecord(ctx, s.readers, "delegation_batches", id, 1<<20, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *sqliteStore) runDelegationRecoveryBarrier(ctx context.Context, hook DelegationRecoveryHook) error {
	if hook != nil {
		if err := hook(ctx, s); err != nil {
			return err
		}
	}
	pending, err := s.pendingDelegationRecovery(ctx)
	if err != nil {
		return err
	}
	if pending {
		return errors.New("Delegation recovery is not complete/wired; interrupted work must be invalidated before readiness, without replay.")
	}
	return nil
}
