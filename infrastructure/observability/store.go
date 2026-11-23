// Package observability retains bounded, redacted structured application logs.
package observability

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	ID      uint64         `json:"id"`
	Time    string         `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}
type Filter struct {
	Level, Query, Since, Until string
	Page, Limit                int
}
type Result struct {
	Items            []Entry        `json:"items"`
	Total            int            `json:"total"`
	Page             int            `json:"page"`
	Limit            int            `json:"limit"`
	Retained         int            `json:"retained"`
	Counts           map[string]int `json:"counts"`
	PersistenceError bool           `json:"persistence_error"`
}
type Store struct {
	mu           sync.Mutex
	entries      []Entry
	limit        int
	file         *os.File
	path         string
	next         uint64
	secrets      []string
	diskError    bool
	month        string
	cleanupMonth string
	now          func() time.Time
	stop         chan struct{}
	done         chan struct{}
	closed       bool
}

// Calendar months follow Beijing time regardless of the container timezone.
var logTimezone = time.FixedZone("Asia/Shanghai", 8*60*60)

func NewStore(path string, limit int) (*Store, error) {
	s, err := newStore(path, limit, time.Now)
	if err != nil {
		return nil, err
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if !s.closed {
					_ = s.advanceLocked(s.now())
				}
				s.mu.Unlock()
			}
		}
	}()
	return s, nil
}

func newStore(path string, limit int, now func() time.Time) (*Store, error) {
	if limit < 1 {
		limit = 2000
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	s := &Store{path: path, limit: limit, now: now}
	if err := s.advanceLocked(now()); err != nil {
		return nil, err
	}
	// Legacy size-rotated files remain readable for the rest of their month.
	for _, history := range []string{path + ".2", path + ".1", path, s.monthPath(s.month)} {
		f, err := os.Open(history)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			s.diskError = true
			continue
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 256<<10)
		for scan.Scan() {
			var e Entry
			if json.Unmarshal(scan.Bytes(), &e) != nil {
				continue
			}
			when, err := time.Parse(time.RFC3339Nano, e.Time)
			if err != nil || when.In(logTimezone).Format("2006-01") != s.month {
				continue
			}
			if e.ID > s.next {
				s.next = e.ID
			}
			s.entries = append(s.entries, e)
			if len(s.entries) > limit {
				s.entries = s.entries[1:]
			}
		}
		if scan.Err() != nil {
			s.diskError = true
		}
		f.Close()
	}
	return s, nil
}

func (s *Store) monthPath(month string) string {
	ext := filepath.Ext(s.path)
	return strings.TrimSuffix(s.path, ext) + "-" + month + ext
}

// Called on writes, reads and by the idle maintenance timer. Close the previous
// file before deleting it; only this store's dated files and legacy paths match.
func (s *Store) advanceLocked(now time.Time) error {
	month := now.In(logTimezone).Format("2006-01")
	if month != s.month {
		if s.file != nil {
			_ = s.file.Close()
			s.file = nil
		}
		s.month = month
		s.entries = nil
	}
	if s.file == nil {
		file, err := os.OpenFile(s.monthPath(month), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			s.diskError = true
			return err
		}
		s.file = file
	}
	if s.cleanupMonth == month {
		return nil
	}
	cleaned := true
	names, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil {
		s.diskError = true
		return err
	}
	ext := filepath.Ext(s.path)
	prefix := strings.TrimSuffix(filepath.Base(s.path), ext) + "-"
	for _, entry := range names {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ext) {
			continue
		}
		date := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ext)
		if _, err := time.Parse("2006-01", date); err != nil || date >= month {
			continue
		}
		if err := os.Remove(filepath.Join(filepath.Dir(s.path), name)); err != nil {
			s.diskError = true
			cleaned = false
		}
	}
	for _, legacy := range []string{s.path + ".2", s.path + ".1", s.path} {
		if info, err := os.Stat(legacy); err == nil && info.ModTime().In(logTimezone).Format("2006-01") < month {
			if err := os.Remove(legacy); err != nil {
				s.diskError = true
				cleaned = false
			}
		}
	}
	if cleaned {
		s.cleanupMonth = month
	}
	return nil
}

func (s *Store) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.stop != nil {
			close(s.stop)
		}
		if s.file != nil {
			_ = s.file.Close()
			s.file = nil
		}
	}
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}
func (s *Store) SetSecrets(secrets []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets = secrets
	for i := range s.entries {
		s.entries[i].Message = s.redact(s.entries[i].Message)
		for k, v := range s.entries[i].Fields {
			s.entries[i].Fields[k] = s.redact(fmt.Sprint(v))
		}
	}
}
func (s *Store) redact(value string) string {
	for _, secret := range s.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
func sensitive(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "api_key") || strings.Contains(key, "authorization") || strings.Contains(key, "cookie")
}
func (s *Store) Add(level, message string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	now := s.now()
	_ = s.advanceLocked(now)
	s.next++
	safe := map[string]any{}
	for k, v := range fields {
		if sensitive(k) {
			safe[k] = "[REDACTED]"
		} else {
			value := s.redact(fmt.Sprint(v))
			if len(value) > 8192 {
				value = value[:8192] + "…"
			}
			safe[k] = value
		}
	}
	message = s.redact(message)
	if len(message) > 16384 {
		message = message[:16384] + "…"
	}
	e := Entry{s.next, now.UTC().Format(time.RFC3339Nano), level, message, safe}
	s.entries = append(s.entries, e)
	if len(s.entries) > s.limit {
		s.entries = s.entries[len(s.entries)-s.limit:]
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err != nil || s.file == nil {
		s.diskError = true
		return
	}
	if _, err = s.file.Write(data); err != nil {
		s.diskError = true
	}
}
func (s *Store) Query(f Filter) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		_ = s.advanceLocked(s.now())
	}
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Page < 1 {
		f.Page = 1
	}
	r := Result{Items: []Entry{}, Page: f.Page, Limit: f.Limit, Retained: len(s.entries), Counts: map[string]int{"info": 0, "warn": 0, "error": 0}, PersistenceError: s.diskError}
	var matched []Entry
	since, _ := time.Parse(time.RFC3339, f.Since)
	until, _ := time.Parse(time.RFC3339, f.Until)
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := s.entries[i]
		r.Counts[e.Level]++
		if f.Level != "" && e.Level != f.Level {
			continue
		}
		when, _ := time.Parse(time.RFC3339Nano, e.Time)
		if !since.IsZero() && when.Before(since) || !until.IsZero() && when.After(until) {
			continue
		}
		data, _ := json.Marshal(e)
		if f.Query != "" && !strings.Contains(strings.ToLower(string(data)), strings.ToLower(f.Query)) {
			continue
		}
		matched = append(matched, e)
	}
	r.Total = len(matched)
	lastPage := (r.Total + f.Limit - 1) / f.Limit
	if lastPage < 1 {
		lastPage = 1
	}
	if r.Page > lastPage {
		r.Page = lastPage
	}
	start := (r.Page - 1) * f.Limit
	if start < r.Total {
		end := start + f.Limit
		if end > r.Total {
			end = r.Total
		}
		r.Items = matched[start:end]
	}
	return r
}

// SecretsFrom extracts known credential strings for message-level redaction.
func SecretsFrom(value any) []string {
	var out []string
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if sensitive(k) {
				if text, ok := child.(string); ok && text != "" {
					out = append(out, text)
				}
			}
			out = append(out, SecretsFrom(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, SecretsFrom(child)...)
		}
	}
	return out
}

func (s *Store) AddSecrets(secrets []string) {
	s.mu.Lock()
	all := append(append([]string{}, s.secrets...), secrets...)
	s.mu.Unlock()
	s.SetSecrets(all)
}
