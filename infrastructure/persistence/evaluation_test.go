package persistence

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func evalFiles(t *testing.T) *EvalFiles {
	t.Helper()
	data, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	runs, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	files := &EvalFiles{Data: data, Runs: runs}
	t.Cleanup(func() {
		_ = files.Close()
	})
	return files
}

func TestEvalRunsRecoverAndFreeze(t *testing.T) {
	repository := &EvalRuns{Files: evalFiles(t)}
	ctx := context.Background()

	if err := repository.Create(ctx, entity.EvalRun{ID: "run1", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	run, err := repository.Get(ctx, "run1")
	if err != nil || run.Status != "interrupted" {
		t.Fatalf("%+v %v", run, err)
	}

	run.Status = "succeeded"
	if err = repository.Save(ctx, run); err == nil {
		t.Fatal("终态被覆盖")
	}
	if _, err = repository.Get(ctx, "../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestEvalDatasetRejectsOutsideSymlink(t *testing.T) {
	files := evalFiles(t)
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(outside, "manifest.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := files.Data.Symlink(outside, "escape"); err != nil {
		t.Fatal(err)
	}

	datasets := &EvalDatasets{Files: files}
	if _, err := datasets.Load(context.Background(), "escape"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}
