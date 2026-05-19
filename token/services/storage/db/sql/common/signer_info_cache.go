/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import "sync"

type syncMapSignerInfoCache struct {
	values sync.Map
}

// NewSignerInfoCache returns a process-local cache for signer existence.
func NewSignerInfoCache() SignerInfoCache {
	return &syncMapSignerInfoCache{}
}

func (c *syncMapSignerInfoCache) Get(key string) (bool, bool) {
	value, ok := c.values.Load(key)
	if !ok {
		return false, false
	}

	v, ok := value.(bool)
	return v, ok
}

func (c *syncMapSignerInfoCache) Add(key string, value bool) {
	if !value {
		c.values.LoadOrStore(key, false)
		return
	}

	c.values.Store(key, value)
}

func (c *syncMapSignerInfoCache) Delete(key string) {
	c.values.Delete(key)
}
