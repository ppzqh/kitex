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
	"container/list"
	"sync"
)

type Deque struct {
	l *list.List
	mu sync.RWMutex
}

func NewDeque() *Deque {
	return &Deque{
		l: new(list.List),
	}
}

func (q *Deque) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.l.Len()
}

func (q *Deque) Back() interface{} {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.l.Back()
}

func (q *Deque) Front() interface{} {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.l.Front()
}

func (q *Deque) PushBack(e interface{}) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.l.PushBack(e)
}

func (q *Deque) PushFront(e interface{}) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.l.PushFront(e)
}

func (q *Deque) PopBack() interface{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	e := q.l.Back()
	if e != nil {
		q.l.Remove(e)
		return e
	}
	return nil
}

func (q *Deque) PopFront() interface{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	e := q.l.Front()
	if e != nil {
		q.l.Remove(e)
		return e
	}
	return nil
}