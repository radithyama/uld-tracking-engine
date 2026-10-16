package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestECSShape(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "svc", "info")
	l.Warn("hello \"world\"\n", "err", errors.New("boom"))
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("not JSON: %s", buf.String())
	}
	for _, k := range []string{"@timestamp", "log.level", "message", "error.message", "service", "ecs"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %v", k, m)
		}
	}
	if m["log.level"] != "warn" || m["service"].(map[string]any)["name"] != "svc" {
		t.Fatalf("unexpected: %v", m)
	}
	if bytes.Count(bytes.TrimSpace(buf.Bytes()), []byte("\n")) != 0 {
		t.Fatal("one event must be exactly one line")
	}
}
