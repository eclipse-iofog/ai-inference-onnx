// Package session loads one CPU session per selected model version and holds the tensor buffers those sessions run on.
package session

import (
	"errors"
	"sync"
)

const (
	// DefaultCeiling is 32 MiB, the payload size used when the server keeps its built-in limit.
	DefaultCeiling = 32 << 20

	size4KiB  = 4 << 10
	size64KiB = 64 << 10
	size1MiB  = 1 << 20

	// maxIdle is how many unused buffers one size class retains.
	maxIdle = 2
)

// ErrSize is returned when a get asks for fewer than one byte.
var ErrSize = errors.New("buffer size must be greater than zero")

// ErrCeiling is returned when a get asks for more bytes than the pool allows.
var ErrCeiling = errors.New("buffer size exceeds the ceiling")

var errBadCeiling = errors.New("buffer ceiling must be greater than zero")

// newBytes allocates a class buffer.
var newBytes = func(n int) []byte {
	return make([]byte, n)
}

// Pool retains idle tensor buffers.
// Classes are 4 KiB, 64 KiB, and 1 MiB, and the ceiling when that size is not already one of those.
// A fixed class larger than the ceiling is not used. Each class keeps at most two idle buffers.
type Pool struct {
	ceiling int
	classes []class
}

// Buffer is bytes for one tensor.
type Buffer struct {
	// Data has length equal to the requested size.
	// Its capacity is the size class when Pooled is true.
	Data []byte
	// Pooled reports whether the pool owns Data.
	// Put takes pooled bytes back. A caller slice stays with the caller.
	Pooled bool
}

type class struct {
	size int
	pool sync.Pool
	mu   sync.Mutex
	idle int
}

// NewPool returns a pool whose largest buffer is ceiling bytes.
// Pass DefaultCeiling for the 32 MiB payload limit the server uses unless that limit is changed.
func NewPool(ceiling int) (*Pool, error) {
	if ceiling < 1 {
		return nil, errBadCeiling
	}
	return &Pool{
		ceiling: ceiling,
		classes: classesFor(ceiling),
	}, nil
}

func classesFor(ceiling int) []class {
	fixed := []int{size4KiB, size64KiB, size1MiB}
	sizes := make([]int, 0, len(fixed)+1)
	for _, size := range fixed {
		if size > ceiling {
			break
		}
		sizes = append(sizes, size)
	}
	if len(sizes) == 0 || sizes[len(sizes)-1] != ceiling {
		sizes = append(sizes, ceiling)
	}
	classes := make([]class, len(sizes))
	for i, size := range sizes {
		classes[i].size = size
	}
	return classes
}

// Get returns a buffer of length n.
// When existing has room for n bytes, Data is that slice and Pooled is false, so those bytes are not copied.
// A size below 1 returns ErrSize. A size above the ceiling returns ErrCeiling and allocates nothing.
func (p *Pool) Get(n int, existing []byte) (Buffer, error) {
	if n <= 0 {
		return Buffer{}, ErrSize
	}
	if n > p.ceiling {
		return Buffer{}, ErrCeiling
	}
	if cap(existing) >= n {
		return Buffer{Data: existing[:n]}, nil
	}
	return Buffer{Data: p.classFor(n).get(n), Pooled: true}, nil
}

// Put returns a pooled buffer to its size class.
// Each class keeps at most two idle buffers. Another put drops the buffer.
// A buffer that is not pooled is left with the caller.
func (p *Pool) Put(b Buffer) {
	if !b.Pooled {
		return
	}
	c := p.classBySize(cap(b.Data))
	if c == nil {
		return
	}
	c.put(b.Data)
}

func (p *Pool) classFor(n int) *class {
	for i := range p.classes {
		if n <= p.classes[i].size {
			return &p.classes[i]
		}
	}
	return &p.classes[len(p.classes)-1]
}

func (p *Pool) classBySize(size int) *class {
	for i := range p.classes {
		if p.classes[i].size == size {
			return &p.classes[i]
		}
	}
	return nil
}

func (p *Pool) idleCount(size int) int {
	c := p.classBySize(size)
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idle
}

func (c *class) get(n int) []byte {
	c.mu.Lock()
	v := c.pool.Get()
	if v == nil {
		// The pool may drop an idle buffer. Allow the class to fill again.
		c.idle = 0
		c.mu.Unlock()
		return newBytes(c.size)[:n]
	}
	if c.idle > 0 {
		c.idle--
	}
	c.mu.Unlock()
	bp, ok := v.(*[]byte)
	if !ok || bp == nil || cap(*bp) < n {
		return newBytes(c.size)[:n]
	}
	return (*bp)[:n]
}

func (c *class) put(buf []byte) {
	if cap(buf) < c.size {
		return
	}
	full := buf[:c.size]
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idle >= maxIdle {
		return
	}
	c.idle++
	stored := full
	c.pool.Put(&stored)
}
