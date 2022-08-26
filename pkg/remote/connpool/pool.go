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

package connpool

import (
	"context"
	"sync"
	"time"
)

type poolObject interface {
	SetDeadline(time.Time) error
	IsActive() bool
}

type PoolConfig struct {
	MaxNum         int
	MinIdle        int // zero if not specified
	MaxIdle        int
	MaxIdleTimeout time.Duration

	Wait bool
}

type pool struct {
	idleList []poolObject
	mu       sync.RWMutex
	closedCh chan struct{}

	// config
	maxIdle        int           // currIdle <= maxIdle. no limit if the value is set to zero.
	minIdle        int           // currIdle >= minIdle.
	maxNum         int           // currIdle + currInuse <= maxNum; maxNum can be the same with maxIdle..
	maxIdleTimeout time.Duration // the idle connection will be cleaned if the idle time exceeds maxIdleTimeout.
	evictFrequency time.Duration // the frequency to evict idle connections.
	wait           bool          // indicate whether to wait for connections be put back into the pool if currNum == maxNum. default = false.
	chs            chan struct{} // chs will be used for notification when there are available objects. cap(chs) should be maxNum.

	// stat
	currNum      int           // alive objects, idleList + objects in use.
	waitCount    int           // record the number of Get that were blocked due to the maxNum limit.
	waitDuration time.Duration // record the waiting time of Get.
}

func NewPool(cfg PoolConfig) *pool {
	p := &pool{
		maxNum:         cfg.MaxNum,
		maxIdle:        cfg.MaxIdle,
		maxIdleTimeout: cfg.MaxIdleTimeout,
	}
	// TODO: where to set `wait`
	cfg.Wait = true
	if cfg.Wait {
		p.wait = true
		chs := make(chan struct{}, cfg.MaxNum)
		for i := 0; i < cfg.MaxNum; i++ {
			chs <- struct{}{}
		}
		p.chs = chs
	}
	return p
}

func (p *pool) Get(newer func() (poolObject, error)) (poolObject, bool, error) {
	if p.wait {
		if !p.waitAvailable(context.Background()) {
			return nil, false, nil
		}
	}

	p.mu.Lock()
	n := len(p.idleList)
	if n > 0 {
		c := p.idleList[n-1]
		p.idleList = p.idleList[:n-1]
		p.mu.Unlock()
		return c, true, nil
	}
	p.mu.Unlock()
	// create a new object
	c, err := newer()
	if err != nil {
		return nil, false, err
	}
	c.SetDeadline(time.Now().Add(p.maxIdleTimeout))
	return c, false, nil
}

func (p *pool) waitAvailable(ctx context.Context) bool {
	select {
	case <-p.chs:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *pool) Put(c poolObject) bool {
	var recycled bool
	p.mu.Lock()
	if len(p.idleList) < p.maxIdle {
		p.idleList = append(p.idleList, c)
		c.SetDeadline(time.Now().Add(p.maxIdleTimeout))
		recycled = true
	}
	if p.wait {
		p.chs <- struct{}{}
	}
	p.mu.Unlock()
	return recycled
}
