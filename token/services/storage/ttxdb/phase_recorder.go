/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-token-sdk/token/services/utils/phase"
)

// ttxdb records phase histograms through the SDK phase helper rather than
// going through a caller-supplied callback. The CollectEndorsementsView
// layer (in token/services/ttx) does not know about ttxdb internals, so
// pushing a PhaseRecorder option down to here would require either a
// driver-facing API or a context value. The helper keeps the call sites thin,
// uses the same explicit buckets as cbdc-biz, and merges the histogram into
// cbdc-biz's existing observability series under the same metric name.
//
// Source: obsidian "CBDC 压测优化迭代 2026-05-16" §3.

// recordPhase records the elapsed duration since start under the given
// phase label. Safe to call from any goroutine; no-op until the OTel
// meter has been registered by the host application.
func recordPhase(ctx context.Context, phaseName string, start time.Time) {
	phase.Record(ctx, phaseName, start)
}
