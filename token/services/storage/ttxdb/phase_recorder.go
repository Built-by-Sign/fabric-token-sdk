/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ttxdb records phase histograms directly through the OTel global meter
// rather than going through a caller-supplied callback. The CollectEndorsementsView
// layer (in token/services/ttx) does not know about ttxdb internals, so
// pushing a PhaseRecorder option down to here would require either a
// driver-facing API or a context value. Direct OTel keeps the call sites
// thin and merges the histogram into cbdc-biz's existing observability
// series under the same metric name.
//
// Source: obsidian "CBDC 压测优化迭代 2026-05-16" §3.

const phaseDurationName = "cbdc.view.phase.duration"

var (
	phaseInitOnce sync.Once
	phaseHist     metric.Float64Histogram
)

func ensurePhaseInit() {
	phaseInitOnce.Do(func() {
		meter := otel.Meter("cbdc-biz.views")
		h, err := meter.Float64Histogram(
			phaseDurationName,
			metric.WithDescription("Duration of named sub-phases inside ttxdb operations."),
			metric.WithUnit("s"),
		)
		if err != nil {
			// Fall back to a no-op recorder if histogram registration fails.
			phaseHist = nil
			return
		}
		phaseHist = h
	})
}

// recordPhase records the elapsed duration since start under the given
// phase label. Safe to call from any goroutine; no-op until the OTel
// meter has been registered by the host application.
func recordPhase(ctx context.Context, phase string, start time.Time) {
	ensurePhaseInit()
	if phaseHist == nil {
		return
	}
	phaseHist.Record(ctx, time.Since(start).Seconds(),
		metric.WithAttributes(attribute.String("phase", phase)))
}
