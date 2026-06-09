/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"time"
)

// boundedTxLifetime caps how long a write transaction opened by the driver
// interface may stay open. NewTransactionStoreTransaction / NewTokenDBTransaction
// receive no request ctx, so a caller that stalls after BEGIN (e.g. blocked on
// P2P/signing once its HTTP request already timed out) would leave the
// connection "idle in transaction" forever and exhaust the pool. Opening with a
// bounded context lets database/sql roll back and release the connection once
// the cap elapses, even if the caller never returns. 60s matches the gateway
// HTTP request timeout — past it the originating request is already gone, so
// force-closing the orphaned transaction loses nothing.
const boundedTxLifetime = 60 * time.Second

// beginBoundedTx opens a write transaction whose lifetime is capped by
// boundedTxLifetime. The returned cancel must be invoked when the transaction
// finishes (commit/rollback) to release the timer promptly; if the caller never
// finishes, the context's own deadline still rolls the transaction back.
func beginBoundedTx(db *sql.DB) (*sql.Tx, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), boundedTxLifetime)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return tx, cancel, nil
}
