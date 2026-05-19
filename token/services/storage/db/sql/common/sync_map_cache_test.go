/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSyncMapCacheGetOrLoad(t *testing.T) {
	t.Parallel()

	cache := NewSyncMapCache[[]byte]()
	value, ok, err := cache.GetOrLoad("key", func() ([]byte, error) {
		return []byte("loaded"), nil
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, []byte("loaded"), value)

	cache.Add("key", []byte("cached"))
	value, ok, err = cache.GetOrLoad("key", func() ([]byte, error) {
		return []byte("ignored"), nil
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("cached"), value)

	cache.Delete("key")
	_, ok = cache.Get("key")
	require.False(t, ok)
}

func TestSyncMapCacheCoalescesConcurrentMisses(t *testing.T) {
	t.Parallel()

	cache := NewSyncMapCache[int]()
	const workers = 50
	var loaderCalls int32
	loaderStarted := make(chan struct{})
	releaseLoader := make(chan struct{})
	var closeLoaderStarted sync.Once
	start := make(chan struct{})
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		go func() {
			<-start
			value, _, err := cache.GetOrLoad("shared", func() (int, error) {
				atomic.AddInt32(&loaderCalls, 1)
				closeLoaderStarted.Do(func() { close(loaderStarted) })
				<-releaseLoader
				return 7, nil
			})
			if err != nil {
				errs <- err
				return
			}
			if value != 7 {
				errs <- errors.New("unexpected cache value")
				return
			}
			errs <- nil
		}()
	}

	close(start)
	select {
	case <-loaderStarted:
	case <-time.After(time.Second):
		t.Fatal("loader was not called")
	}
	time.Sleep(20 * time.Millisecond)
	close(releaseLoader)

	for i := 0; i < workers; i++ {
		require.NoError(t, <-errs)
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&loaderCalls))
}
