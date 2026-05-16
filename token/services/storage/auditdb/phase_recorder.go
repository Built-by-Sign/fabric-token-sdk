/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

import "context"

// PhaseFn wraps a unit of work whose duration should be recorded under
// the given phase name. The wrapper is supplied by the caller and is
// retrieved from the context by SDK internals at instrumented sub-phases.
type PhaseFn func(ctx context.Context, phaseName string, fn func(context.Context) error) error

type phaseRecorderKey struct{}

// WithPhaseRecorder attaches a phase wrapper to the context. SDK internals
// (auditdb store, audit DB inserts) look it up via PhaseRecorderFrom and
// invoke it at instrumented sub-phases. Nil wrapper is treated as a no-op.
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
// directly if no recorder is attached. Used by auditdb.StoreService.Append
// to record per-insert sub-phases without making every call site nil-check.
func runPhase(ctx context.Context, phaseName string, fn func(context.Context) error) error {
	if rec := PhaseRecorderFrom(ctx); rec != nil {
		return rec(ctx, phaseName, fn)
	}
	return fn(ctx)
}
