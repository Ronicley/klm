package main

import (
	"context"
	"time"
)

// Runs before ordinary/graph recovery and readiness. Never dispatches or replays.
// A restart is not evidence that a dispatched native/remote call settled.
func recoverDelegationRuntime(ctx context.Context, store *sqliteStore) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	after := ""
	for {
		page, err := store.delegationRecoveryPage(ctx, after, 50)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, pending := range page {
			_, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				batch, err := st.delegationBatch(pending.ID)
				if err != nil {
					return err
				}
				members, err := st.delegationMembers(batch.ID)
				if err != nil {
					return err
				}
				for _, item := range members {
					if item.State == DelegationInvocationTerminal {
						continue
					}
					finality := DelegationFinality{}
					if !item.DispatchAttempted {
						finality = DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}
					}
					if err := st.finishDelegationItem(item.ID, DelegationInterrupted, finality, "", "Execution interrupted by engine restart; not replayed.", 0); err != nil {
						return err
					}
				}
				if batch.State != DelegationSettled {
					if _, err := st.settleDelegation(batch.ID); err != nil {
						return err
					}
					batch, err = st.delegationBatch(batch.ID)
					if err != nil {
						return err
					}
				}
				if err := st.eachMatching("delegation_calls", `owner_id=? AND x_delivery IN ('pending','sending')`, []any{batch.ID}, func(key string) error {
					var call DelegationNativeCall
					if err := decodeSQLiteRecord(ctx, st.tx, "delegation_calls", key, 64<<10, &call); err != nil {
						return err
					}
					if call.Delivery == DelegationDeliverySending {
						call.Delivery = DelegationDeliveryUncertain
					} else {
						call.Delivery = DelegationDeliveryCancelled
					}
					return st.putExistingRecord("delegation_calls", key, call)
				}); err != nil {
					return err
				}
				if batch.Delivery == DelegationDeliveryPending || batch.Delivery == DelegationDeliverySending {
					if batch.Delivery == DelegationDeliverySending {
						batch.Delivery = DelegationDeliveryUncertain
					} else {
						batch.Delivery = DelegationDeliveryCancelled
					}
					batch.CancellationRequested, batch.UpdatedAt = true, now()
					if err := st.putDelegationBatch(&batch); err != nil {
						return err
					}
				}
				if _, err := st.recoverQueue(batch.ParentSessionID, "Child execution was interrupted; check its result before sending again."); err != nil {
					return err
				}
				members, err = st.delegationMembers(batch.ID)
				if err != nil {
					return err
				}
				return st.syncDelegationCards(batch, members)
			})
			if err != nil {
				return err
			}
			after = pending.ID
		}
	}
}
