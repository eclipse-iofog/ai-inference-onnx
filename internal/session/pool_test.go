package session

import (
	"errors"
	"sync"
	"testing"
	"unsafe"
)

func TestSizeClasses(t *testing.T) {
	t.Parallel()

	p := mustPool(t, DefaultCeiling)
	if DefaultCeiling != 32<<20 {
		t.Fatalf("DefaultCeiling = %d", DefaultCeiling)
	}

	checks := []struct {
		n     int
		class int
	}{
		{n: 1, class: size4KiB},
		{n: size4KiB, class: size4KiB},
		{n: size4KiB + 1, class: size64KiB},
		{n: size64KiB, class: size64KiB},
		{n: size64KiB + 1, class: size1MiB},
		{n: size1MiB, class: size1MiB},
		{n: size1MiB + 1, class: DefaultCeiling},
		{n: DefaultCeiling, class: DefaultCeiling},
	}
	for _, check := range checks {
		buf := mustGet(t, p, check.n)
		if cap(buf.Data) != check.class || !buf.Pooled {
			t.Fatalf("Get(%d) cap=%d pooled=%v, want cap %d", check.n, cap(buf.Data), buf.Pooled, check.class)
		}
	}

	t.Run("ceiling between fixed classes", func(t *testing.T) {
		t.Parallel()
		mid := mustPool(t, 5000)
		small := mustGet(t, mid, size4KiB)
		if cap(small.Data) != size4KiB {
			t.Fatalf("cap %d", cap(small.Data))
		}
		large := mustGet(t, mid, size4KiB+1)
		if cap(large.Data) != 5000 {
			t.Fatalf("cap %d", cap(large.Data))
		}
		if mid.classBySize(size64KiB) != nil || mid.classBySize(size1MiB) != nil {
			t.Fatal("a fixed class larger than the ceiling is in use")
		}
	})
}

func TestCeiling(t *testing.T) {
	t.Parallel()

	p := mustPool(t, DefaultCeiling)
	buf := mustGet(t, p, DefaultCeiling)
	if len(buf.Data) != DefaultCeiling || cap(buf.Data) != DefaultCeiling {
		t.Fatalf("len=%d cap=%d", len(buf.Data), cap(buf.Data))
	}
	rejected, err := p.Get(DefaultCeiling+1, nil)
	if !errors.Is(err, ErrCeiling) || rejected.Data != nil || rejected.Pooled {
		t.Fatalf("buf=%v err=%v", rejected, err)
	}

	t.Run("100 bytes", func(t *testing.T) {
		t.Parallel()
		small := mustPool(t, 100)
		if small.classBySize(size4KiB) != nil || small.classBySize(size64KiB) != nil || small.classBySize(size1MiB) != nil {
			t.Fatal("fixed class larger than 100 bytes is in use")
		}
		got := mustGet(t, small, 100)
		if cap(got.Data) != 100 || len(got.Data) != 100 {
			t.Fatalf("len=%d cap=%d", len(got.Data), cap(got.Data))
		}
		one := mustGet(t, small, 1)
		if cap(one.Data) != 100 {
			t.Fatalf("cap %d", cap(one.Data))
		}
		over, err := small.Get(101, nil)
		if !errors.Is(err, ErrCeiling) || over.Data != nil {
			t.Fatalf("buf=%v err=%v", over, err)
		}
		if small.idleCount(size4KiB) != 0 || small.idleCount(100) != 0 {
			t.Fatal("rejected get stored a buffer")
		}
	})
}

func TestRejectedGetDoesNotAllocate(t *testing.T) {
	var allocated int
	prev := newBytes
	newBytes = func(n int) []byte {
		allocated += n
		return prev(n)
	}
	t.Cleanup(func() { newBytes = prev })

	p := mustPool(t, DefaultCeiling)
	buf, err := p.Get(DefaultCeiling+1, nil)
	if !errors.Is(err, ErrCeiling) || buf.Data != nil || allocated != 0 {
		t.Fatalf("buf=%v err=%v allocated=%d", buf, err, allocated)
	}
	buf, err = p.Get(0, make([]byte, 8))
	if !errors.Is(err, ErrSize) || buf.Data != nil || allocated != 0 {
		t.Fatalf("buf=%v err=%v allocated=%d", buf, err, allocated)
	}
	buf, err = p.Get(-1, nil)
	if !errors.Is(err, ErrSize) || allocated != 0 {
		t.Fatalf("buf=%v err=%v allocated=%d", buf, err, allocated)
	}

	small := mustPool(t, 100)
	buf, err = small.Get(101, nil)
	if !errors.Is(err, ErrCeiling) || buf.Data != nil || allocated != 0 {
		t.Fatalf("buf=%v err=%v allocated=%d", buf, err, allocated)
	}
	buf, err = small.Get(50, nil)
	if err != nil || cap(buf.Data) != 100 || allocated != 100 {
		t.Fatalf("cap=%d err=%v allocated=%d", cap(buf.Data), err, allocated)
	}
}

func TestIdleLimit(t *testing.T) {
	t.Parallel()

	p := mustPool(t, DefaultCeiling)
	const n = 32
	bufs := make([]Buffer, 3)
	for i := range bufs {
		bufs[i] = mustGet(t, p, n)
		if !bufs[i].Pooled || cap(bufs[i].Data) != size4KiB {
			t.Fatalf("cap %d pooled %v", cap(bufs[i].Data), bufs[i].Pooled)
		}
	}
	for i, buf := range bufs {
		p.Put(buf)
		want := i + 1
		if want > maxIdle {
			want = maxIdle
		}
		if got := p.idleCount(size4KiB); got != want {
			t.Fatalf("after put %d, idle %d", i+1, got)
		}
	}
	if p.idleCount(size64KiB) != 0 || p.idleCount(size1MiB) != 0 || p.idleCount(DefaultCeiling) != 0 {
		t.Fatal("another class gained an idle buffer")
	}

	// A caller slice is not an idle buffer, including one whose capacity is a class size.
	caller := make([]byte, n, size4KiB)
	got, err := p.Get(n, caller)
	if err != nil {
		t.Fatal(err)
	}
	p.Put(got)
	if p.idleCount(size4KiB) != maxIdle {
		t.Fatalf("idle %d", p.idleCount(size4KiB))
	}
}

func TestCallerSlice(t *testing.T) {
	t.Parallel()
	p := mustPool(t, DefaultCeiling)

	existing := make([]byte, 100)
	existing[0] = 9
	got, err := p.Get(len(existing), existing)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pooled || slicePtr(got.Data) != slicePtr(existing) || len(got.Data) != 100 || cap(got.Data) != 100 {
		t.Fatalf("got len=%d cap=%d pooled=%v", len(got.Data), cap(got.Data), got.Pooled)
	}
	if got.Data[0] != 9 {
		t.Fatal("caller bytes were copied")
	}
	p.Put(got)
	if p.idleCount(size4KiB) != 0 {
		t.Fatal("caller slice was stored")
	}

	reserved := make([]byte, 0, 64)
	view, err := p.Get(32, reserved)
	if err != nil {
		t.Fatal(err)
	}
	if view.Pooled || cap(view.Data) != 64 || unsafe.SliceData(view.Data) != unsafe.SliceData(reserved) {
		t.Fatalf("view len=%d cap=%d pooled=%v", len(view.Data), cap(view.Data), view.Pooled)
	}

	short := make([]byte, 0, 10)
	pooled, err := p.Get(11, short)
	if err != nil {
		t.Fatal(err)
	}
	if !pooled.Pooled || cap(pooled.Data) != size4KiB || unsafe.SliceData(pooled.Data) == unsafe.SliceData(short) {
		t.Fatalf("cap=%d pooled=%v", cap(pooled.Data), pooled.Pooled)
	}
}

func TestZeroSize(t *testing.T) {
	t.Parallel()
	p := mustPool(t, DefaultCeiling)
	for _, n := range []int{0, -5} {
		buf, err := p.Get(n, make([]byte, 4))
		if !errors.Is(err, ErrSize) || buf.Data != nil || buf.Pooled {
			t.Fatalf("Get(%d) buf=%v err=%v", n, buf, err)
		}
	}
	if p.idleCount(size4KiB) != 0 {
		t.Fatal("zero-size get stored a buffer")
	}
}

func TestNewPoolRejectsEmptyCeiling(t *testing.T) {
	t.Parallel()
	for _, ceiling := range []int{0, -1} {
		p, err := NewPool(ceiling)
		if err == nil || p != nil {
			t.Fatalf("ceiling %d pool=%v err=%v", ceiling, p, err)
		}
	}
}

func TestConcurrentGetPut(t *testing.T) {
	t.Parallel()
	p := mustPool(t, DefaultCeiling)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				buf, err := p.Get(128, nil)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				p.Put(buf)
				caller := make([]byte, 16)
				again, err := p.Get(len(caller), caller)
				if err != nil {
					t.Errorf("Get caller: %v", err)
					return
				}
				p.Put(again)
			}
		}()
	}
	wg.Wait()
	if p.idleCount(size4KiB) > maxIdle {
		t.Fatalf("idle %d", p.idleCount(size4KiB))
	}
}

func mustPool(t *testing.T, ceiling int) *Pool {
	t.Helper()
	p, err := NewPool(ceiling)
	if err != nil {
		t.Fatalf("NewPool(%d): %v", ceiling, err)
	}
	return p
}

func mustGet(t *testing.T, p *Pool, n int) Buffer {
	t.Helper()
	buf, err := p.Get(n, nil)
	if err != nil {
		t.Fatalf("Get(%d): %v", n, err)
	}
	if len(buf.Data) != n {
		t.Fatalf("Get(%d) len=%d", n, len(buf.Data))
	}
	return buf
}

func slicePtr(b []byte) uintptr {
	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))
}
