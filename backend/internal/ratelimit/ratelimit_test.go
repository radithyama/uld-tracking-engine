package ratelimit

import "testing"

func TestKeyedBurstThenRefuse(t *testing.T) {
	k := NewKeyed(0.001, 3, 10)
	for i := 0; i < 3; i++ {
		if !k.Allow("a") {
			t.Fatalf("request %d within burst refused", i)
		}
	}
	if k.Allow("a") {
		t.Fatal("request beyond burst allowed")
	}
	if !k.Allow("b") {
		t.Fatal("other key affected by a's limit")
	}
}

func TestKeyedBoundsMemory(t *testing.T) {
	k := NewKeyed(0.001, 1, 2)
	k.Allow("a")
	k.Allow("b")
	if k.Allow("c") {
		t.Fatal("new key tracked beyond maxKeys")
	}
	if len(k.m) > 2 {
		t.Fatalf("tracked %d keys, want <= 2", len(k.m))
	}
}

func TestConnsPerKeyAndTotal(t *testing.T) {
	c := NewConns(2, 3)
	r1, _ := c.Acquire("ip1")
	r2, _ := c.Acquire("ip1")
	if _, reason := c.Acquire("ip1"); reason != "per_key" {
		t.Fatalf("third conn from ip1: reason %q, want per_key", reason)
	}
	r3, _ := c.Acquire("ip2")
	if _, reason := c.Acquire("ip3"); reason != "total" {
		t.Fatalf("conn beyond total: reason %q, want total", reason)
	}
	r1()
	r1() // release is idempotent
	if c.Active() != 2 {
		t.Fatalf("active = %d after one release, want 2", c.Active())
	}
	if rel, reason := c.Acquire("ip1"); rel == nil {
		t.Fatalf("slot not freed by release: %q", reason)
	}
	r2()
	r3()
}
