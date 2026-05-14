/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

import "context"

type phaseRecorderContextKey struct{}

// PhaseRecorder records a timed phase without coupling the token SDK storage
// package to a concrete observability implementation.
type PhaseRecorder func(ctx context.Context, phaseName string, fn func(context.Context) error) error

// WithPhaseRecorder attaches a phase recorder to ctx.
func WithPhaseRecorder(ctx context.Context, recorder PhaseRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, phaseRecorderContextKey{}, recorder)
}

// RunPhase records phaseName when ctx carries a PhaseRecorder. Otherwise it
// executes fn directly.
func RunPhase(ctx context.Context, phaseName string, fn func(context.Context) error) error {
	recorder, _ := ctx.Value(phaseRecorderContextKey{}).(PhaseRecorder)
	if recorder == nil {
		return fn(ctx)
	}
	return recorder(ctx, phaseName, fn)
}
