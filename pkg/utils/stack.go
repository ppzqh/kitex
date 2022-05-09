package utils

import (
	"errors"
	"sync"
)

var ErrStackFull = errors.New("stack is full")

type Stack struct {
	mu  sync.RWMutex
	arr []interface{}
	cap int
}

func NewStack(cap int) *Stack {
	if cap < 0 {
		cap = 0
	}

	return &Stack{
		mu:  sync.RWMutex{},
		arr: make([]interface{}, 0),
		cap: cap,
	}
}

func (s *Stack) Cap() int {
	return s.cap
}

func (s *Stack) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.arr)
}

func (s *Stack) Pop() interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.arr) - 1
	if n < 0 {
		return nil
	}

	p := s.arr[n]
	s.arr = s.arr[:n]
	return p
}

func (s *Stack) Push(p interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.arr)
	if n >= s.cap {
		return ErrStackFull
	}

	s.arr = append(s.arr, p)
	return nil
}

func (s *Stack) Top() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.arr) - 1
	if n < 0 {
		return nil
	}
	return s.arr[n]
}

func (s *Stack) Bottom() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.arr)
	if n == 0 {
		return nil
	}
	return s.arr[0]
}

func (s *Stack) PopBottom() interface{} {
	s.mu.Lock()
	s.mu.Unlock()

	n := len(s.arr)
	if n == 0 {
		return nil
	}

	p := s.arr[0]
	s.arr = s.arr[1:]
	return p
}

type stackDump struct {
	Array []interface{} `json:"array"`
	Len   int           `json:"len"`
	Cap   int           `json:"cap"`
}

func (s *Stack) Dump() interface{} {
	return nil
}
