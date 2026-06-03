/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-token-sdk/token"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/ttx/dep"
)

// TokenRequestHasher processes token requests from raw bytes
type TokenRequestHasher struct {
	tmsProvider dep.TokenManagementServiceProvider
	tmsID       token.TMSID
}

// NewTokenRequestHasher creates a new token request hasher
func NewTokenRequestHasher(tmsProvider dep.TokenManagementServiceProvider, tmsID token.TMSID) *TokenRequestHasher {
	return &TokenRequestHasher{
		tmsProvider: tmsProvider,
		tmsID:       tmsID,
	}
}

// ProcessTokenRequest processes a raw token request and returns the request and message to sign
func (h *TokenRequestHasher) ProcessTokenRequest(ctx context.Context, txID string, tokenRequestRaw []byte) (tr *token.Request, msgToSign []byte, err error) {
	tms, err := h.tmsProvider.TokenManagementService(token.WithTMSID(h.tmsID))
	if err != nil {
		return nil, nil, errors.Errorf("failed to get token management service: [%w]", err)
	}

	// Owner nodes store the full TokenRequestWithMetadata; non-owner nodes (e.g.
	// endorsers) only persist the bare TokenRequest (actions, no metadata). Fall
	// back to the actions form when the full-envelope parse fails.
	tr, err = tms.NewFullRequestFromBytes(tokenRequestRaw)
	if err != nil {
		tr, err = tms.NewRequestFromBytes(token.RequestAnchor(txID), tokenRequestRaw, nil)
		if err != nil {
			return nil, nil, errors.Errorf("failed to create token request from bytes: [%w]", err)
		}
		// Bare request carries no metadata; null the empty one NewRequestFromBytes
		// attaches so Service.AppendValid's no-metadata guard skips movement extraction.
		tr.Metadata = nil
	}

	msgToSign, err = tr.MarshalToSign()
	if err != nil {
		return nil, nil, errors.Errorf("failed to marshal token request to sign: [%w]", err)
	}

	return tr, msgToSign, nil
}
