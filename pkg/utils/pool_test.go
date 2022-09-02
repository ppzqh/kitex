/*
 * Copyright 2022 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package utils

import (
	"testing"
	"time"

	"github.com/cloudwego/kitex/internal/test"
)

var _ PoolObject = &mockPoolObject{}

type mockPoolObject struct {
	active bool
	deadline time.Time
}

func (o *mockPoolObject) SetDeadline(t time.Time) error {
	o.deadline = t
	return nil
}

func (o *mockPoolObject) IsActive() bool {
	return o.active
}

func (o *mockPoolObject) Close() error {
	return nil
}

func (o *mockPoolObject) Expired() bool {
	return time.Now().After(o.deadline)
}

func mockObjectNewer() (PoolObject, error) {
	return &mockPoolObject{active: true}, nil
}

// fakeNewLongPool creates a LongPool object and modifies it to fit tests.
func newPoolForTest(maxTotal, minIdle, maxIdle int, maxIdleTimeout time.Duration, wait bool) *Pool {
	cfg := PoolConfig{
		MaxNum:         maxTotal,
		MinIdle:        minIdle,
		MaxIdle:        maxIdle,
		MaxIdleTimeout: maxIdleTimeout,
		Wait:           wait,
	}
	return NewPool(cfg)
}

func TestPool_GetPut(t *testing.T) {
	var (
		maxTotal       = 1
		minIdle        = 0
		maxIdle        = 1
		maxIdleTimeout = time.Millisecond
		wait           = false
	)

	pool := NewPool(PoolConfig{
		MaxNum:         maxTotal,
		MinIdle:        minIdle,
		MaxIdle:        maxIdle,
		MaxIdleTimeout: maxIdleTimeout,
		Wait:           wait,
	})

	obj, reused, err := pool.Get(mockObjectNewer)
	test.Assert(t, obj != nil)
	test.Assert(t, reused == false)
	test.Assert(t, err == nil)

	recycled := pool.Put(obj)
	test.Assert(t, recycled == true)
	test.Assert(t, pool.Len() == 1)
}

func TestPool_Reuse(t *testing.T) {
	var (
		maxTotal       = 1
		minIdle        = 0
		maxIdle        = 1
		maxIdleTimeout = time.Millisecond
		wait           = false
	)

	pool := NewPool(PoolConfig{
		MaxNum:         maxTotal,
		MinIdle:        minIdle,
		MaxIdle:        maxIdle,
		MaxIdleTimeout: maxIdleTimeout,
		Wait:           wait,
	})

	count := make(map[PoolObject]bool)
	obj, reused, err := pool.Get(mockObjectNewer)
	test.Assert(t, obj != nil)
	test.Assert(t, reused == false)
	test.Assert(t, err == nil)
	count[obj] = true

	recycled := pool.Put(obj)
	test.Assert(t, recycled == true)
	test.Assert(t, pool.Len() == 1)

	obj, reused, err = pool.Get(mockObjectNewer)
	test.Assert(t, obj != nil)
	test.Assert(t, reused == true)
	test.Assert(t, err == nil)
	count[obj] = true

	test.Assert(t, len(count) == 1)
}

func TestPool_MaxIdle(t *testing.T) {
	var (
		maxTotal       = 10
		minIdle        = 0
		maxIdle        = 2
		maxIdleTimeout = time.Millisecond
		wait           = false
	)

	pool := NewPool(PoolConfig{
		MaxNum:         maxTotal,
		MinIdle:        minIdle,
		MaxIdle:        maxIdle,
		MaxIdleTimeout: maxIdleTimeout,
		Wait:           wait,
	})

	var objs []PoolObject
	for i := 0; i < maxIdle+1; i++ {
		obj, reused, err := pool.Get(mockObjectNewer)
		test.Assert(t, obj != nil)
		test.Assert(t, reused == false)
		test.Assert(t, err == nil)
		objs = append(objs, obj)
	}

	for i := 0; i < maxIdle+1; i++ {
		recycled := pool.Put(objs[i])
		if i < maxIdle {
			test.Assert(t, recycled == true)
		} else {
			test.Assert(t, recycled == false)
		}
	}

	test.Assert(t, pool.Len() == maxIdle)
}

func TestPool_MinIdle(t *testing.T) {
	var (
		maxTotal       = 10
		minIdle        = 2
		maxIdle        = 10
		maxIdleTimeout = time.Millisecond
		wait           = false
	)

	pool := NewPool(PoolConfig{
		MaxNum:         maxTotal,
		MinIdle:        minIdle,
		MaxIdle:        maxIdle,
		MaxIdleTimeout: maxIdleTimeout,
		Wait:           wait,
	})

	var objs []PoolObject
	for i := 0; i < minIdle+1; i++ {
		obj, reused, err := pool.Get(mockObjectNewer)
		test.Assert(t, obj != nil)
		test.Assert(t, reused == false)
		test.Assert(t, err == nil)
		objs = append(objs, obj)
	}
	for i := 0; i < minIdle+1; i++ {
		recycled := pool.Put(objs[i])
		test.Assert(t, recycled == true)
	}
	
	time.Sleep(maxIdleTimeout)
	pool.Evict()
	test.Assert(t, pool.Len() == minIdle)

	obj, reused, err := pool.Get(mockObjectNewer)
	// Although this object has expired, it can still be got from the pool.
	test.Assert(t, obj.Expired() == true)
	test.Assert(t, obj != nil)
	test.Assert(t, reused == true)
	test.Assert(t, err == nil)
}