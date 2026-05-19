/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSignerInfoCache(t *testing.T) {
	t.Parallel()

	cache := NewSignerInfoCache()

	_, ok := cache.Get("missing")
	require.False(t, ok)

	cache.Add("known", true)
	value, ok := cache.Get("known")
	require.True(t, ok)
	require.True(t, value)

	cache.Add("not-signer", false)
	value, ok = cache.Get("not-signer")
	require.True(t, ok)
	require.False(t, value)

	cache.Add("not-signer", true)
	value, ok = cache.Get("not-signer")
	require.True(t, ok)
	require.True(t, value)

	cache.Add("not-signer", false)
	value, ok = cache.Get("not-signer")
	require.True(t, ok)
	require.True(t, value)

	cache.Delete("known")
	_, ok = cache.Get("known")
	require.False(t, ok)
}
