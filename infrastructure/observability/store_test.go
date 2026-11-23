package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedLogsFiltersAndRedaction(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "service.jsonl"), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetSecrets([]string{"super-secret-value"})
	s.Add("info", "ready", map[string]any{"api_key": "secret"})
	s.Add("error", "failed super-secret-value", nil)
	s.Add("warn", "slow", nil)
	s.Add("info", "last", nil)
	r := s.Query(Filter{Level: "error", Query: "failed", Limit: 50})
	if r.Total != 1 || r.Items[0].Message != "failed [REDACTED]" {
		t.Fatalf("unexpected result %#v", r)
	}
	if s.Query(Filter{Limit: 50}).Total != 3 {
		t.Fatal("retention unbounded")
	}
	if s.Query(Filter{Query: "super-secret-value", Limit: 50}).Total != 0 {
		t.Fatal("secret exposed")
	}
}

func TestSecretsTraverseSensitiveModelNames(t *testing.T) {
	secrets := SecretsFrom(map[string]any{"token-model": map[string]any{"api_key": "private-key"}})
	if len(secrets) != 1 || secrets[0] != "private-key" {
		t.Fatal("nested credential was not collected")
	}
}

func TestMonthHistorySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.jsonl")
	s, err := NewStore(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	s.Add("error", "first", nil)
	s.Add("info", "second", nil)
	s.Add("info", "third", nil)
	s.Close()
	s, err = NewStore(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Query(Filter{Level: "error"}); got.Total != 1 {
		t.Fatalf("lost rotated error: %#v", got)
	}
}

func TestMonthlyFilesAndExpiredCleanup(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().In(time.FixedZone("CST", 8*60*60))
	expired := filepath.Join(dir, "service-"+now.AddDate(0, -1, 0).Format("2006-01")+".jsonl")
	unrelated := filepath.Join(dir, "other-2020-01.jsonl")
	os.WriteFile(expired, []byte("old"), 0600)
	os.WriteFile(unrelated, []byte("keep"), 0600)
	s, err := NewStore(filepath.Join(dir, "service.jsonl"), 20)
	if err != nil {
		t.Fatal(err)
	}
	s.Add("info", "current month", nil)
	s.Close()
	monthly := filepath.Join(dir, "service-"+now.Format("2006-01")+".jsonl")
	if _, err := os.Stat(monthly); err != nil {
		t.Fatal("monthly log missing:", err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatal("expired month not deleted")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated file was deleted")
	}
}

func TestMonthBoundaryUsesBeijingTimeAndClearsIdleHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.jsonl")
	now := time.Date(2026, 11, 30, 15, 59, 59, 0, time.UTC)
	s, err := newStore(path, 20, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Add("error", "November event", nil)
	old := filepath.Join(dir, "service-2026-11.jsonl")
	if _, err := os.Stat(old); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	// The maintenance path must work even without another log write or request.
	s.mu.Lock()
	err = s.advanceLocked(now)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("previous month still on disk")
	}
	if r := s.Query(Filter{}); r.Total != 0 {
		t.Fatal("previous month still in memory")
	}
	s.Add("info", "December event", nil)
	if data, err := os.ReadFile(filepath.Join(dir, "service-2026-12.jsonl")); err != nil || !strings.Contains(string(data), "December event") {
		t.Fatal("new month not written", err)
	}
	s.Close()
	s, err = newStore(path, 20, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if r := s.Query(Filter{}); r.Total != 1 || r.Items[0].Message != "December event" {
		t.Fatalf("restart restore failed: %#v", r)
	}
}

func TestLegacyLogsRemainReadableUntilMonthEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.jsonl")
	now := time.Now()
	e := Entry{ID: 42, Time: now.UTC().Format(time.RFC3339Nano), Level: "error", Message: "legacy event"}
	data, _ := json.Marshal(e)
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStore(path, 20, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if r := s.Query(Filter{}); r.Total != 1 || r.Items[0].ID != 42 {
		t.Fatalf("legacy event not restored: %#v", r)
	}
	now = now.In(logTimezone).AddDate(0, 1, 0)
	s.Query(Filter{})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expired legacy file remains")
	}
}
