/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newNotifierBackedMembership builds a LocalMembership whose store supports
// change notifications, loads it, and returns the captured Subscribe callback.
func newNotifierBackedMembership(t *testing.T) (
	*membership.LocalMembership,
	*mock.IdentityStoreService,
	func(idriver.Operation, idriver.IdentityConfigurationRecord),
) {
	t.Helper()

	ip := &mock.IdentityProvider{}
	ip.BindReturns(nil)

	iss := &mock.IdentityStoreService{}
	iss.ConfigurationExistsReturns(false, nil)
	iss.AddConfigurationReturns(nil)
	iss.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

	notifier := &mock.IdentityConfigurationNotifier{}
	iss.NotifierReturns(notifier, nil)

	var mu sync.Mutex
	var subCallback func(idriver.Operation, idriver.IdentityConfigurationRecord)
	notifier.SubscribeStub = func(callback func(idriver.Operation, idriver.IdentityConfigurationRecord)) error {
		mu.Lock()
		defer mu.Unlock()
		subCallback = callback

		return nil
	}

	km := &mock.KeyManager{}
	km.EnrollmentIDReturns("e1")
	km.IdentityReturns(&idriver.IdentityDescriptor{Identity: []byte("id1")}, nil)
	km.IdentityTypeReturns(identity.Type(99))

	kmp := &mock.KeyManagerProvider{}
	kmp.GetReturns(km, nil)

	lm := membership.NewLocalMembership(
		logging.MustGetLogger("test"),
		&mock.Config{},
		[]byte("netid"),
		&mock.SignerDeserializerManager{},
		iss,
		"testType",
		false,
		ip,
		kmp,
	)

	require.NoError(t, lm.Load(t.Context(), nil, nil))

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, subCallback)

	return lm, iss, subCallback
}

func TestLocalMembership_NotifierGatesReload(t *testing.T) {
	ctx := t.Context()
	lm, iss, notify := newNotifierBackedMembership(t)

	// Load performed the initial store read.
	require.Equal(t, 1, iss.IteratorConfigurationsCallCount())

	// First miss reloads once: the index starts unsynchronized with the store.
	_, err := lm.GetIdentityInfo(ctx, "ghost1", nil)
	require.Error(t, err)
	assert.Equal(t, 2, iss.IteratorConfigurationsCallCount())

	// Further misses do not touch the store: notifications keep the index fresh.
	_, err = lm.GetIdentityInfo(ctx, "ghost2", nil)
	require.Error(t, err)
	_, err = lm.GetIdentityInfo(ctx, "ghost1", nil)
	require.Error(t, err)
	assert.Equal(t, 2, iss.IteratorConfigurationsCallCount())

	// A failed notification marks the store dirty: the next miss reloads.
	iss.GetConfigurationReturns(nil, nil)
	notify(idriver.Insert, idriver.IdentityConfigurationRecord{ID: "x", Type: "testType", URL: "/tmp/x"})

	_, err = lm.GetIdentityInfo(ctx, "ghost3", nil)
	require.Error(t, err)
	assert.Equal(t, 3, iss.IteratorConfigurationsCallCount())
}

func TestLocalMembership_NotificationRegistersWithoutReload(t *testing.T) {
	ctx := t.Context()
	lm, iss, notify := newNotifierBackedMembership(t)

	// Clear the initial dirty state with one miss.
	_, err := lm.GetIdentityInfo(ctx, "ghost", nil)
	require.Error(t, err)
	require.Equal(t, 2, iss.IteratorConfigurationsCallCount())

	// A successful notification registers the configuration incrementally.
	newConfig := idriver.IdentityConfiguration{ID: "bob", URL: "/tmp/bob", Type: "testType"}
	iss.GetConfigurationReturns(&newConfig, nil)
	notify(idriver.Insert, idriver.IdentityConfigurationRecord{ID: "bob", Type: "testType", URL: "/tmp/bob"})

	info, err := lm.GetIdentityInfo(ctx, "bob", nil)
	require.NoError(t, err)
	assert.NotNil(t, info)
	// The lookup was served from the index, not from a store reload.
	assert.Equal(t, 2, iss.IteratorConfigurationsCallCount())
}
