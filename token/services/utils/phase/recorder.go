/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package phase

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	scopeViews        = "cbdc-biz.views"
	phaseDurationName = "cbdc.view.phase.duration"
	phaseCounterName  = "cbdc.view.phase.count"
	attrPhase         = "phase"
)

// Buckets mirrors cbdc-biz/pkg/observability.PhaseBuckets so SDK-internal
// recorders do not fall back to coarse OTel defaults.
var Buckets = []float64{
	0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

var (
	initOnce    sync.Once
	hist        metric.Float64Histogram
	counterOnce sync.Once
	counter     metric.Int64Counter
)

func ensureInit() {
	initOnce.Do(func() {
		h, err := otel.Meter(scopeViews).Float64Histogram(
			phaseDurationName,
			metric.WithUnit("s"),
			metric.WithDescription("Per-phase latency for view operations"),
			metric.WithExplicitBucketBoundaries(Buckets...),
		)
		if err != nil {
			return
		}
		hist = h
	})
}

func ensureCounterInit() {
	counterOnce.Do(func() {
		c, err := otel.Meter(scopeViews).Int64Counter(
			phaseCounterName,
			metric.WithDescription("Cumulative counter for named phases (rows written, items processed, ...)"),
		)
		if err != nil {
			return
		}
		counter = c
	})
}

// Record records the elapsed duration since start under the supplied phase.
func Record(ctx context.Context, phase string, start time.Time) {
	RecordDuration(ctx, phase, time.Since(start))
}

// RecordDuration records an already-measured duration under the supplied phase.
func RecordDuration(ctx context.Context, phase string, elapsed time.Duration) {
	ensureInit()
	if hist == nil {
		return
	}
	hist.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attribute.String(attrPhase, phase)))
}

// Counter adds value to the cumulative counter under the supplied phase. Use
// it for row counts, byte counts, or any non-latency quantity that should be
// summed across calls. Negative values are clamped to zero.
func Counter(ctx context.Context, phase string, value int64) {
	if value < 0 {
		value = 0
	}
	ensureCounterInit()
	if counter == nil {
		return
	}
	counter.Add(ctx, value, metric.WithAttributes(attribute.String(attrPhase, phase)))
}
