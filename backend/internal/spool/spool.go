// Package spool is a durable, append-only, FIFO on-disk buffer used by the
// ingest API to keep accepting scanner telemetry while Kafka is unavailable.
//
// Layout: a directory of segment files (seg-<seq>.jsonl), one JSON record per
// line. New records go to the single "active" segment; a segment is closed
// (made immutable) when it is full or when the replayer wants to drain it.
// Closed segments are replayed oldest-first and deleted only after Kafka has
// acknowledged every record in them.
//
// Durability: Append fsyncs before returning, and the HTTP 202 is sent only
// after that, so an acknowledged scan survives an ingest crash or restart.
// Delivery is at-least-once (a segment whose publish partially succeeded is
// re-published in full); the processor deduplicates on event_id.
package spool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrFull  = errors.New("spool full")
	ErrEmpty = errors.New("spool empty")
)

// Record is one buffered Kafka message.
type Record struct {
	Key     string          `json:"k"`
	Value   json.RawMessage `json:"v"`
	EventID string          `json:"id"`
	Time    time.Time       `json:"t"`
}

type segment struct {
	name    string
	records int
	bytes   int64
}

type Spool struct {
	dir           string
	maxBytes      int64
	maxSegRecords int

	mu      sync.Mutex // guards everything below plus file writes
	active  *os.File
	cur     segment // the active segment's bookkeeping
	closed  []segment
	lastSeq int64

	pending atomic.Int64 // records not yet acknowledged by Kafka
	size    atomic.Int64 // bytes on disk
}

// Open creates dir if needed and recovers any segments left by a previous
// run (they are all treated as closed and will be replayed).
func Open(dir string, maxBytes int64, maxSegRecords int) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Spool{dir: dir, maxBytes: maxBytes, maxSegRecords: maxSegRecords}
	names, err := filepath.Glob(filepath.Join(dir, "seg-*.jsonl"))
	if err != nil {
		return nil, err
	}
	slices.Sort(names) // zero-padded sequence numbers sort chronologically
	for _, path := range names {
		seg, err := scanSegment(path)
		if err != nil {
			return nil, err
		}
		if seg.records == 0 {
			_ = os.Remove(path)
			continue
		}
		s.closed = append(s.closed, seg)
		s.pending.Add(int64(seg.records))
		s.size.Add(seg.bytes)
		if seq := seqOf(seg.name); seq > s.lastSeq {
			s.lastSeq = seq
		}
	}
	return s, nil
}

func scanSegment(path string) (segment, error) {
	f, err := os.Open(path)
	if err != nil {
		return segment{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return segment{}, err
	}
	seg := segment{name: filepath.Base(path), bytes: info.Size()}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) > 0 {
			seg.records++
		}
	}
	return seg, sc.Err()
}

func seqOf(name string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, "seg-"), ".jsonl"), 10, 64)
	return n
}

func (s *Spool) Pending() int64 { return s.pending.Load() }
func (s *Spool) Bytes() int64   { return s.size.Load() }

// Append durably writes records to the active segment. All records are
// written with a single write + fsync; either the call succeeds and every
// record is durable, or it returns an error and the caller must not ack.
func (s *Spool) Append(recs []Record) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // Encode appends '\n'
	for i := range recs {
		if err := enc.Encode(&recs[i]); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.size.Load()+int64(buf.Len()) > s.maxBytes {
		return ErrFull
	}
	if s.active == nil {
		if err := s.openActiveLocked(); err != nil {
			return err
		}
	}
	if _, err := s.active.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("spool write: %w", err)
	}
	if err := s.active.Sync(); err != nil {
		return fmt.Errorf("spool fsync: %w", err)
	}
	s.cur.records += len(recs)
	s.cur.bytes += int64(buf.Len())
	s.pending.Add(int64(len(recs)))
	s.size.Add(int64(buf.Len()))

	if s.cur.records >= s.maxSegRecords {
		return s.rotateLocked()
	}
	return nil
}

func (s *Spool) openActiveLocked() error {
	seq := time.Now().UnixNano()
	if seq <= s.lastSeq {
		seq = s.lastSeq + 1
	}
	s.lastSeq = seq
	name := fmt.Sprintf("seg-%020d.jsonl", seq)
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	// fsync the directory so the new file's entry itself is durable.
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	s.active, s.cur = f, segment{name: name}
	return nil
}

func (s *Spool) rotateLocked() error {
	if s.active == nil {
		return nil
	}
	err := s.active.Close()
	s.closed = append(s.closed, s.cur)
	s.active, s.cur = nil, segment{}
	return err
}

// Oldest returns the oldest segment's records for replay. If there are no
// closed segments it closes the active one first, so a backlog drains even
// while producers keep appending (they get a fresh active segment).
// Unparseable lines (a torn write from a crash, which was never acked to the
// client) are skipped and counted in corrupt.
func (s *Spool) Oldest() (name string, recs []Record, corrupt int, err error) {
	s.mu.Lock()
	if len(s.closed) == 0 {
		if s.cur.records == 0 {
			s.mu.Unlock()
			return "", nil, 0, ErrEmpty
		}
		if err := s.rotateLocked(); err != nil {
			s.mu.Unlock()
			return "", nil, 0, err
		}
	}
	seg := s.closed[0]
	s.mu.Unlock()

	// Closed segments are immutable, so read without holding the lock.
	f, err := os.Open(filepath.Join(s.dir, seg.name))
	if err != nil {
		return "", nil, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			corrupt++
			continue
		}
		recs = append(recs, r)
	}
	return seg.name, recs, corrupt, sc.Err()
}

// Ack deletes a fully published segment.
func (s *Spool) Ack(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.closed) == 0 || s.closed[0].name != name {
		return fmt.Errorf("spool: ack of %q out of order", name)
	}
	seg := s.closed[0]
	if err := os.Remove(filepath.Join(s.dir, seg.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.closed = s.closed[1:]
	s.pending.Add(-int64(seg.records))
	s.size.Add(-seg.bytes)
	return nil
}

func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return s.active.Close()
	}
	return nil
}
