package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRedactionConflictAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	_ = json.Unmarshal(original, &seed)
	seed["mysql"].(map[string]any)["password"] = "test-private-password"
	original, _ = json.Marshal(seed)
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	svc := NewConfigStore(path)
	snapshot, err := svc.Read()
	if err != nil {
		t.Fatal(err)
	}
	mysql := snapshot.Config["mysql"].(map[string]any)
	if mysql["password"] != Masked {
		t.Fatal("password must be masked")
	}
	mysql["host"] = "db.internal"
	if err = svc.Save(snapshot.Version, snapshot.Config); err != nil {
		t.Fatal(err)
	}
	if err = svc.Save(snapshot.Version, snapshot.Config); err == nil {
		t.Fatal("stale update must fail")
	}
	next, _ := svc.Read()
	next.Config["embedding"].(map[string]any)["default"] = "missing-model"
	before, _ := os.ReadFile(path)
	if err = svc.Save(next.Version, next.Config); err == nil {
		t.Fatal("invalid default must fail")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid save modified file")
	}
	var parsed map[string]any
	_ = json.Unmarshal(after, &parsed)
	if parsed["mysql"].(map[string]any)["password"] != "test-private-password" {
		t.Fatal("masked password was overwritten")
	}
	if string(after) == string(original) {
		t.Fatal("valid edit was not saved")
	}
}

func TestMaskTraversesSensitiveModelNames(t *testing.T) {
	obj := map[string]any{"models": map[string]any{"token-model": map[string]any{"api_key": "private-key"}}}
	mask(obj)
	if obj["models"].(map[string]any)["token-model"].(map[string]any)["api_key"] != Masked {
		t.Fatal("model name bypassed credential masking")
	}
}
