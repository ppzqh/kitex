package utils

import (
	"github.com/cloudwego/kitex/internal/test"
	"testing"
)

func TestStack_Len(t *testing.T) {
	capacity := 10
	s := NewStack(capacity)
	test.Assert(t, s.Cap() == capacity)
	test.Assert(t, s.Len() == 0)
}

func TestStack_Push(t *testing.T) {
	capacity := 10
	s := NewStack(capacity)

	for i := 0; i < 2*capacity; i++ {
		err := s.Push(i)
		if i < capacity {
			test.Assert(t, err == nil)
			test.Assert(t, s.Len() == i+1)
		} else {
			test.Assert(t, err == ErrStackFull)
			test.Assert(t, s.Len() == capacity)
		}
	}
}

func TestStack_Top(t *testing.T) {
	capacity := 10
	s := NewStack(capacity)

	for i := 0; i < 2*capacity; i++ {
		_ = s.Push(i)
		if i < capacity {
			test.Assert(t, s.Top() == i)
		} else {
			test.Assert(t, s.Top() == capacity-1)
		}
	}
}

func TestStack_Pop(t *testing.T) {
	capacity := 10
	s := NewStack(capacity)

	for i := 0; i < capacity; i++ {
		_ = s.Push(i)
	}

	for i := 0; i < 2*capacity; i++ {
		p := s.Pop()
		if i < capacity {
			test.Assert(t, s.Len() == capacity-i-1)
			test.Assert(t, p == capacity-i-1)
		} else {
			test.Assert(t, s.Len() == 0)
			test.Assert(t, p == nil)
		}
	}
	test.Assert(t, s.Len() == 0)
}

func TestStack_Bottom(t *testing.T) {
	capacity := 10
	s := NewStack(capacity)
	test.Assert(t, s.Bottom() == nil)

	for i := 0; i < capacity; i++ {
		_ = s.Push(i)
	}

	for i := 0; i < capacity; i++ {
		test.Assert(t, s.Bottom() == 0)
	}
}

func TestStack_PopBottom(t *testing.T) {
	capacity := 10
	s := NewStack(capacity)

	for i := 0; i < capacity; i++ {
		_ = s.Push(i)
	}

	for i := 0; i < 20; i++ {
		p := s.PopBottom()
		if i < capacity {
			test.Assert(t, p == i)
		} else {
			test.Assert(t, s.Bottom() == nil)
		}
	}
}

func Benchmark(b *testing.B) {
	capacity := 1024
	r := NewStack(capacity)
	for i := 0; i < capacity; i++ {
		r.Push(struct{}{})
	}

	// benchmark
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := r.Pop()
		r.Push(p)
	}
}
