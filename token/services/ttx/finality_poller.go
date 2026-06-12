/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-token-sdk/token/services/storage/ttxdb"
)

const (
	// statusPollerChunk bounds the number of tx ids per batch status query.
	statusPollerChunk = 1000
	// statusPollerSweepTimeout bounds a single sweep so a stalled database
	// cannot wedge the poller loop.
	statusPollerSweepTimeout = 30 * time.Second
)

// statusPollers holds one statusPoller per finality database instance.
var statusPollers sync.Map // finalityDB -> *statusPoller

// statusPoller is the shared fallback poller behind dbFinality: instead of
// every waiter polling GetStatus on its own timer, a single goroutine per
// database batch-fetches the statuses of all waited-on transactions and
// re-publishes terminal ones through the database's listener notification,
// waking the registered waiters. It runs for the lifetime of the process.
type statusPoller struct {
	db       finalityDB
	interval time.Duration
	once     sync.Once
}

// ensureStatusPoller starts the shared poller for the given database on first
// use. The polling interval is fixed by the first caller.
func ensureStatusPoller(fdb finalityDB, interval time.Duration) {
	v, ok := statusPollers.Load(fdb)
	if !ok {
		v, _ = statusPollers.LoadOrStore(fdb, &statusPoller{db: fdb, interval: interval})
	}
	p := v.(*statusPoller)
	p.once.Do(func() { go p.run() })
}

func (p *statusPoller) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for range ticker.C {
		p.sweep()
	}
}

// sweep batch-fetches the statuses of every transaction someone is waiting on
// and notifies the terminal ones (Confirmed/Deleted), mirroring what the
// per-waiter poll used to detect.
func (p *statusPoller) sweep() {
	txIDs := p.db.ListenerTxIDs()
	if len(txIDs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusPollerSweepTimeout)
	defer cancel()

	for chunk := range slices.Chunk(txIDs, statusPollerChunk) {
		statuses, err := p.db.GetStatuses(ctx, chunk)
		if err != nil {
			logger.Warnf("status poller: batch status query failed (%d tx ids): %v", len(chunk), err)

			return
		}
		for txID, record := range statuses {
			if record.Status != ttxdb.Confirmed && record.Status != ttxdb.Deleted {
				continue
			}
			p.db.NotifyStatus(ctx, txID, record.Status, record.Message)
		}
	}
}
