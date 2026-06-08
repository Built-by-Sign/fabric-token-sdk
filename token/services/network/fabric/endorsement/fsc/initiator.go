/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"encoding/json"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/hyperledger-labs/fabric-token-sdk/token"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/network/driver"
)

// TransientApprovalMetadataKey is the transient map key used to carry optional application-level
// approval metadata from the initiator to the responder.
const TransientApprovalMetadataKey = "approval_metadata"

// RequestApprovalView is the initiator of the request approval protocol
type RequestApprovalView struct {
	TMSID      token.TMSID
	TxID       driver.TxID
	RequestRaw []byte
	// Nonce, if not nil it will be appended to the messages to sign.
	// This is to be used only for testing.
	Nonce []byte
	// Endorsers are the identities of the FSC node that play the role of endorser
	Endorsers []view.Identity
	// Metadata carries optional application-level key-value pairs forwarded to the approver via transient data.
	Metadata driver.TransientMap

	// EndorserService is the endorser service
	EndorserService EndorserService
	// TokenManagementSystemProvider
	TokenManagementSystemProvider TokenManagementSystemProvider
}

// NewRequestApprovalView returns a new instance of RequestApprovalView
func NewRequestApprovalView(
	TMSID token.TMSID,
	txID driver.TxID,
	requestRaw []byte,
	nonce []byte,
	endorsers []view.Identity,
	endorserService EndorserService,
	metadata driver.TransientMap,
) *RequestApprovalView {
	return &RequestApprovalView{
		TMSID:           TMSID,
		TxID:            txID,
		RequestRaw:      requestRaw,
		Nonce:           nonce,
		Endorsers:       endorsers,
		EndorserService: endorserService,
		Metadata:        metadata,
	}
}

func (r *RequestApprovalView) Call(ctx view.Context) (any, error) {
	logger.DebugfContext(ctx.Context(), "request approval from tms id [%s]", r.TMSID)

	newTxStart := time.Now()
	tx, err := r.EndorserService.NewTransaction(
		ctx,
		fabric.WithCreator(r.TxID.Creator),
		fabric.WithNonce(r.TxID.Nonce),
	)
	recordPhaseSince(ctx.Context(), "req_approval_new_tx", newTxStart, err)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create endorser transaction")
	}

	tx.SetProposal(r.TMSID.Namespace, ChaincodeVersion, InvokeFunction)
	endorseProposalStart := time.Now()
	if err := tx.EndorseProposal(); err != nil {
		recordPhaseSince(ctx.Context(), "req_approval_endorse_proposal", endorseProposalStart, err)
		return nil, errors.WithMessagef(err, "failed to endorse proposal")
	}
	recordPhaseSince(ctx.Context(), "req_approval_endorse_proposal", endorseProposalStart, nil)

	// transient fields
	transientStart := time.Now()
	if err := tx.SetTransientState(TransientTMSIDKey, r.TMSID); err != nil {
		recordPhaseSince(ctx.Context(), "req_approval_set_transient", transientStart, err)
		return nil, errors.WithMessagef(err, "failed to set TMS ID transient")
	}
	if err := tx.SetTransient(TransientTokenRequestKey, r.RequestRaw); err != nil {
		recordPhaseSince(ctx.Context(), "req_approval_set_transient", transientStart, err)
		return nil, errors.WithMessagef(err, "failed to set token request transient")
	}
	if len(r.Metadata) > 0 {
		metadataRaw, err := json.Marshal(r.Metadata)
		if err != nil {
			recordPhaseSince(ctx.Context(), "req_approval_set_transient", transientStart, err)
			return nil, errors.WithMessagef(err, "failed to marshal approval metadata")
		}
		if err := tx.SetTransient(TransientApprovalMetadataKey, metadataRaw); err != nil {
			recordPhaseSince(ctx.Context(), "req_approval_set_transient", transientStart, err)
			return nil, errors.WithMessagef(err, "failed to set approval metadata transient")
		}
	}
	recordPhaseSince(ctx.Context(), "req_approval_set_transient", transientStart, nil)

	logger.DebugfContext(ctx.Context(), "request endorsement on tx [%s] to [%v]...", tx.ID(), r.Endorsers)
	collectEndorsementsStart := time.Now()
	err = r.EndorserService.CollectEndorsements(ctx, tx, 2*time.Minute, r.Endorsers...)
	recordPhaseSince(ctx.Context(), "req_approval_collect_endorsements", collectEndorsementsStart, err)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to collect endorsements")
	}
	logger.DebugfContext(ctx.Context(), "request endorsement done")

	// Return envelope
	envelopeStart := time.Now()
	env, err := tx.Envelope()
	recordPhaseSince(ctx.Context(), "req_approval_envelope", envelopeStart, err)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to retrieve envelope for endorsement")
	}
	logger.DebugfContext(ctx.Context(), "envelope ready")

	return env, nil
}
