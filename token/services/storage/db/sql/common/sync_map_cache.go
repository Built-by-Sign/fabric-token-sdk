/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"sync"

	"golang.org/x/sync/singleflight"
)

type SyncMapCache[T any] struct {
	values sync.Map
	sf     singleflight.Group
}

// NewSyncMapCache returns an unbounded process-local cache with singleflight
// miss coalescing.
func NewSyncMapCache[T any]() *SyncMapCache[T] {
	return &SyncMapCache[T]{}
}

func (c *SyncMapCache[T]) Get(key string) (T, bool) {
	value, ok := c.values.Load(key)
	if !ok {
		return zeroValue[T](), false
	}

	v, ok := value.(T)
	if !ok {
		return zeroValue[T](), false
	}
	return v, true
}

func (c *SyncMapCache[T]) GetOrLoad(key string, loader func() (T, error)) (T, bool, error) {
	if value, ok := c.Get(key); ok {
		return value, true, nil
	}

	value, err, _ := c.sf.Do(key, func() (any, error) {
		if cached, ok := c.Get(key); ok {
			return syncMapResult[T]{value: cached, fromCache: true}, nil
		}

		loaded, err := loader()
		if err != nil {
			return syncMapResult[T]{}, err
		}
		c.values.Store(key, loaded)
		return syncMapResult[T]{value: loaded}, nil
	})
	if err != nil {
		return zeroValue[T](), false, err
	}

	result, _ := value.(syncMapResult[T])
	return result.value, result.fromCache, nil
}

func (c *SyncMapCache[T]) Add(key string, value T) {
	c.values.Store(key, value)
}

func (c *SyncMapCache[T]) Delete(key string) {
	c.values.Delete(key)
}

type syncMapResult[T any] struct {
	value     T
	fromCache bool
}

func zeroValue[T any]() T {
	var zero T
	return zero
}
