/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"time"
)

// TransferPhaseRecorder is a callback invoked at instrumented sub-phases of
// a TransferService.Transfer implementation. The recorder is supplied by the
// caller and propagated to the driver via ctx; nil is a safe no-op so call
// sites never need a nil check.
type TransferPhaseRecorder func(ctx context.Context, phase string, dur time.Duration)

type transferPhaseRecorderKey struct{}

// WithTransferPhaseRecorder attaches a recorder to ctx so driver
// implementations of TransferService.Transfer can emit timings for their
// internal stages (token load, prepare inputs, ZK proof, audit info, ...)
// without each driver having to wire the callback through its own option
// struct.
func WithTransferPhaseRecorder(ctx context.Context, rec TransferPhaseRecorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, transferPhaseRecorderKey{}, rec)
}

// TransferPhaseRecorderFrom returns the recorder previously attached via
// WithTransferPhaseRecorder, or nil if none. Drivers should guard nil before
// invoking it.
func TransferPhaseRecorderFrom(ctx context.Context) TransferPhaseRecorder {
	if v, ok := ctx.Value(transferPhaseRecorderKey{}).(TransferPhaseRecorder); ok {
		return v
	}
	return nil
}
