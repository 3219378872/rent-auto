# ADR-0009: Durable Reconciliation Observations

Date: 2026-09-08
Status: accepted

## Decision

Inventory ingestion commits a complete successful channel snapshot in one
transaction. An unseen asset becomes `missing` with `tradable=false`, never
`sold`: absence does not prove a financial sale. Failed/partial pagination or
invalid rows must not modify the previous snapshot. A later observed asset
recovers its platform state without overwriting its manual cost basis.

Both `in_stock` and `listed` tradable assets preserve desired routing.
Known leased/locked assets and assets with an active leased listing cannot
produce new publishes, including through a stale mirror on the other channel.
Planned publications consume the remaining copy budget immediately.

Each active listing persists `recon_mismatch_reason` and
`recon_mismatch_since`. Only continuous observations of the same orphan or
surplus condition mature the 24-hour grace. Normal shelf-sync heartbeats do
not reset it; recovery, a changed mismatch reason, or leaving active state
does. The planner resolves one clock value for each execution and persists
observations before returning an executable plan; persistence failure returns
an error and no plan. Unknown observations start a fresh full grace period.

## Consequences

Migration 0009 introduces these fields and the `missing` inventory state.
Rollback maps `missing` to `unknown` while preserving `tradable=false`, and
drops observation timestamps. It does not classify absent assets as sold.
The API/UI must distinguish snapshot absence from sale.

This supersedes ADR-0005's use of `actual_synced_at` as the grace anchor.
`actual_synced_at` remains a last-seen timestamp only. Repairs to the clock,
desired inventory states, and mismatch tracking must ship together.
