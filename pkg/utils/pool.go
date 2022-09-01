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
	"context"
	"sync"
	"time"
)

type PoolObject interface {
	SetDeadline(time.Time) error
	IsActive() bool
	Close() error
}

type PoolStat struct {
	TotalNum int
	IdleNum  int
}

type PoolConfig struct {
	MaxNum         int
	MinIdle        int // zero if not specified
	MaxIdle        int
	MaxIdleTimeout time.Duration

	Wait bool
}

type Pool struct {
	idleList []PoolObject
	mu       sync.RWMutex
	closedCh chan struct{} // unused for now

	// config
	maxIdle        int           // currIdle <= maxIdle. no limit if the value is set to zero.
	minIdle        int           // currIdle >= minIdle.
	maxNum         int           // currIdle + currInuse <= maxNum; maxNum can be the same with maxIdle.
	maxIdleTimeout time.Duration // the idle connection will be cleaned if the idle time exceeds maxIdleTimeout.
	evictFrequency time.Duration // the frequency to evict idle connections.
	wait           bool          // indicate whether to wait for connections be put back into the pool if currNum == maxNum. default = false.
	chs            chan struct{} // chs will be used for notification when there are available objects. cap(chs) should be maxNum.

	// stat
	// TODO: record all stats.
	total        int           // alive objects, idleList + objects in use.
	waitCount    int           // record the number of Get that were blocked due to the maxNum limit.
	waitDuration time.Duration // record the waiting time of Get.
}

func NewPool(cfg PoolConfig) *Pool {
	p := &Pool{
		idleList:       make([]PoolObject, 0, cfg.MaxIdle),
		maxNum:         cfg.MaxNum,
		maxIdle:        cfg.MaxIdle,
		maxIdleTimeout: cfg.MaxIdleTimeout,
	}
	// TODO: where to set `wait`
	//cfg.Wait = true
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

func (p *Pool) Get(newer func() (PoolObject, error)) (obj PoolObject, reused bool, err error) {
	if p.wait {
		if !p.waitAvailable(context.Background()) {
			return nil, false, nil
		}
	}

	p.mu.Lock()
	// Get the first active one
	i := len(p.idleList) - 1
	for ; i >= 0; i-- {
		c := p.idleList[i]
		if c.IsActive() {
			p.idleList = p.idleList[:i]
			p.mu.Unlock()
			return c, true, nil
		}
	}
	// in case all objects are inactive
	if i < 0 {
		i = 0
	}
	p.idleList = p.idleList[:i]
	p.mu.Unlock()

	// construct a new object
	c, err := newer()
	if err != nil {
		return nil, false, err
	}
	c.SetDeadline(time.Now().Add(p.maxIdleTimeout))
	return c, false, nil
}

func (p *Pool) waitAvailable(ctx context.Context) bool {
	select {
	case <-p.chs:
		return true
	// TODO: add timeout
	case <-ctx.Done():
		return false
	}
}

func (p *Pool) Put(c PoolObject) bool {
	var recycled bool
	p.mu.Lock()
	if len(p.idleList) < p.maxIdle {
		p.idleList = append(p.idleList, c)
		c.SetDeadline(time.Now().Add(p.maxIdleTimeout))
		recycled = true
	}
	p.mu.Unlock()
	if p.wait {
		p.chs <- struct{}{}
	}
	return recycled
}

func (p *Pool) Evict() {
	p.mu.Lock()
	i := 0
	for ; i < len(p.idleList); i++ {
		if p.idleList[i].IsActive() {
			break
		}
	}
	p.idleList = p.idleList[i:]
	p.mu.Unlock()
}

func (p *Pool) Len() int {
	p.mu.Lock()
	l := len(p.idleList)
	p.mu.Unlock()
	return l
}

func (p *Pool) Close() {
	p.mu.Lock()
	for i := 0; i < len(p.idleList); i++ {
		p.idleList[i].Close()
	}
	p.idleList = nil
	p.mu.Unlock()
}

func (p *Pool) Stat() PoolStat {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return PoolStat{
		TotalNum: p.total,
		IdleNum:  len(p.idleList),
	}
}

func (p *Pool) Dump() interface{} {
	return nil
}
