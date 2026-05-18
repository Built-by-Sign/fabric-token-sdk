/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import "context"

// PhaseFn wraps a unit of work whose duration should be recorded under
// the given phase name. The wrapper is supplied by the caller and is
// retrieved from the context by SDK internals at instrumented sub-phases.
//
// Mirrors auditdb.PhaseFn but exists as a separate type so that callers
// can selectively wrap auditor.Service operations (Append, AddFinalityListener)
// without also wrapping auditdb's per-row DB writes.
type PhaseFn func(ctx context.Context, phaseName string, fn func(context.Context) error) error

type phaseRecorderKey struct{}

// WithPhaseRecorder attaches a phase wrapper to the context. auditor.Service
// internals look it up via PhaseRecorderFrom and invoke it at instrumented
// sub-phases (e.g. AddFinalityListener). Nil wrapper is treated as a no-op.
func WithPhaseRecorder(ctx context.Context, wrapper PhaseFn) context.Context {
	return context.WithValue(ctx, phaseRecorderKey{}, wrapper)
}

// PhaseRecorderFrom returns the phase wrapper attached by WithPhaseRecorder.
// Returns nil if no wrapper is attached; callers should fall back to running
// fn directly.
func PhaseRecorderFrom(ctx context.Context) PhaseFn {
	if v, ok := ctx.Value(phaseRecorderKey{}).(PhaseFn); ok {
		return v
	}
	return nil
}

// runPhase invokes fn under the phase recorder attached to ctx, or fn
// directly if no recorder is attached.
func runPhase(ctx context.Context, phaseName string, fn func(context.Context) error) error {
	if rec := PhaseRecorderFrom(ctx); rec != nil {
		return rec(ctx, phaseName, fn)
	}
	return fn(ctx)
}
