package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func rec(i int) Record {
	return Record{Key: "AKE10001GA", Value: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)), EventID: fmt.Sprint(i), Time: time.Now()}
}

func drain(t *testing.T, s *Spool) []string {
	t.Helper()
	var ids []string
	for {
		name, recs, _, err := s.Oldest()
		if errors.Is(err, ErrEmpty) {
			return ids
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			ids = append(ids, r.EventID)
		}
		if err := s.Ack(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFIFOAcrossSegments(t *testing.T) {
	s, err := Open(t.TempDir(), 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if err := s.Append([]Record{rec(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if s.Pending() != 10 {
		t.Fatalf("pending %d", s.Pending())
	}
	ids := drain(t, s)
	for i, id := range ids {
		if id != fmt.Sprint(i) {
			t.Fatalf("order broken at %d: %v", i, ids)
		}
	}
	if len(ids) != 10 || s.Pending() != 0 || s.Bytes() != 0 {
		t.Fatalf("ids=%d pending=%d bytes=%d", len(ids), s.Pending(), s.Bytes())
	}
}

func TestRecoversAfterRestartAndSkipsTornWrite(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 1<<20, 100)
	_ = s.Append([]Record{rec(0), rec(1)})
	_ = s.Close()

	// Simulate a crash mid-write: a truncated trailing line.
	matches, _ := filepath.Glob(filepath.Join(dir, "seg-*.jsonl"))
	f, _ := os.OpenFile(matches[0], os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"k":"AKE1`)
	_ = f.Close()

	s2, err := Open(dir, 1<<20, 100)
	if err != nil {
		t.Fatal(err)
	}
	name, recs, corrupt, err := s2.Oldest()
	if err != nil || len(recs) != 2 || corrupt != 1 {
		t.Fatalf("recs=%d corrupt=%d err=%v", len(recs), corrupt, err)
	}
	_ = s2.Ack(name)
	// New appends after recovery must sort after recovered segments.
	_ = s2.Append([]Record{rec(2)})
	if ids := drain(t, s2); len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("got %v", ids)
	}
}

func TestFull(t *testing.T) {
	s, _ := Open(t.TempDir(), 64, 100)
	if err := s.Append([]Record{rec(0), rec(1), rec(2)}); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}

// Producers append concurrently while a replayer drains; every record must
// come out exactly once.
func TestConcurrentAppendAndDrain(t *testing.T) {
	s, _ := Open(t.TempDir(), 64<<20, 50)
	const producers, each = 8, 200
	var wg sync.WaitGroup
	for p := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if err := s.Append([]Record{rec(p*each + i)}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	seen := map[string]int{}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for {
		name, recs, _, err := s.Oldest()
		if errors.Is(err, ErrEmpty) {
			select {
			case <-done:
				if s.Pending() == 0 {
					goto check
				}
			default:
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			seen[r.EventID]++
		}
		if err := s.Ack(name); err != nil {
			t.Fatal(err)
		}
	}
check:
	if len(seen) != producers*each {
		t.Fatalf("got %d distinct, want %d", len(seen), producers*each)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("record %s seen %d times", id, n)
		}
	}
}
