/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Phase instrumentation for the endorsement responder.
//
// The endorser node runs in a separate process from the institution, so it
// cannot import the institution's cbdc-biz/pkg/observability helper. This file
// re-emits the SAME OTel signal (scope "cbdc-biz.views", histogram
// "cbdc.view.phase.duration" keyed by the "phase" attribute) so the existing
// analyze.py phase queries pick up endorser-side spans without extra wiring.
//
// Buckets, names, unit and attribute keys mirror cbdc-biz/pkg/observability so
// the histogram is consistent with the institution/auditor phase metrics.
const (
	phaseScope        = "cbdc-biz.views"
	phaseDurationName = "cbdc.view.phase.duration"
	phaseCounterName  = "cbdc.view.phase"
	attrPhase         = "phase"
	attrError         = "error"
)

var phaseBuckets = []float64{
	0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

var (
	phaseInitOnce sync.Once
	phaseTracer   trace.Tracer
	phaseHist     metric.Float64Histogram
	phaseCounter  metric.Int64Counter
)

// phaseInit lazily binds the shared instruments to the global OTel providers.
// It runs on first record — by then the endorser's signet.Init has installed
// the real meter/tracer providers and exporter.
func phaseInit() {
	phaseInitOnce.Do(func() {
		phaseTracer = otel.Tracer(phaseScope)
		meter := otel.Meter(phaseScope)
		phaseHist, _ = meter.Float64Histogram(
			phaseDurationName,
			metric.WithUnit("s"),
			metric.WithDescription("Per-phase latency for view operations"),
			metric.WithExplicitBucketBoundaries(phaseBuckets...),
		)
		phaseCounter, _ = meter.Int64Counter(
			phaseCounterName,
			metric.WithDescription("Per-phase counter for view operations"),
		)
	})
}

// recordPhaseSince records the elapsed time from start to now under phaseName.
func recordPhaseSince(ctx context.Context, phaseName string, start time.Time, err error) {
	recordPhaseDur(ctx, phaseName, time.Since(start), err)
}

// recordPhaseDur records an explicit duration under phaseName. Used for
// aggregated timings (e.g. cumulative getState) that are not a single span.
func recordPhaseDur(ctx context.Context, phaseName string, elapsed time.Duration, err error) {
	phaseInit()

	attrs := []attribute.KeyValue{
		attribute.String(attrPhase, phaseName),
		attribute.Bool(attrError, err != nil),
	}
	if phaseHist != nil {
		phaseHist.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attrs...))
	}
	if phaseCounter != nil {
		phaseCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	_, span := phaseTracer.Start(ctx, phaseName, trace.WithTimestamp(time.Now().Add(-elapsed)))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
