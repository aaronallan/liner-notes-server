package cache

import (
	"strconv"
	"sync"
	"testing"
)

func TestMemory_SetAndGet(t *testing.T) {
	m := NewMemory[string, string]()

	if _, ok := m.Get("missing"); ok {
		t.Error("expected miss for absent key")
	}

	m.Set("USSUB0500001", "track-abc")
	got, ok := m.Get("USSUB0500001")
	if !ok {
		t.Fatal("expected hit after Set")
	}
	if got != "track-abc" {
		t.Errorf("got %q, want track-abc", got)
	}
}

func TestMemory_Overwrite(t *testing.T) {
	m := NewMemory[string, int]()
	m.Set("k", 1)
	m.Set("k", 2)
	if got, _ := m.Get("k"); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestMemory_ConcurrentAccess(t *testing.T) {
	m := NewMemory[int, int]()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.Set(n, n*2)
			_, _ = m.Get(n)
		}(i)
	}
	wg.Wait()

	for i := 0; i < 100; i++ {
		if got, ok := m.Get(i); !ok || got != i*2 {
			t.Fatalf("key %d: got %d ok=%v, want %d", i, got, ok, i*2)
		}
	}
}

func TestMemory_StringKeysFromInts(t *testing.T) {
	// Guards against accidental key collisions across types.
	m := NewMemory[string, string]()
	for i := 0; i < 10; i++ {
		m.Set(strconv.Itoa(i), strconv.Itoa(i))
	}
	if got, _ := m.Get("7"); got != "7" {
		t.Errorf("got %q, want 7", got)
	}
}
