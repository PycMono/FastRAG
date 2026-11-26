package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/PycMono/FastRAG/domain/entity"
)

// EvalReviews 与任务共用受限文件根，但使用独立子目录。
type EvalReviews struct {
	Files *EvalFiles
}

func writeReviewJSON(root *os.Root, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := root.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("评测文件保存失败")
	}
	return root.Rename(path+".tmp", path)
}

func (r *EvalReviews) ListDrafts(ctx context.Context) ([]entity.EvalDraft, error) {
	if err := r.Files.Runs.MkdirAll("drafts", 0700); err != nil {
		return nil, err
	}
	directory, err := r.Files.Runs.Open("drafts")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	drafts := []entity.EvalDraft{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		draft, err := r.GetDraft(ctx, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

func (r *EvalReviews) GetDraft(ctx context.Context, id string) (entity.EvalDraft, error) {
	var draft entity.EvalDraft
	if !evalName.MatchString(id) {
		return draft, fmt.Errorf("草稿 ID 无效")
	}
	err := readEvalJSON(r.Files.Runs, "drafts/"+id+".json", &draft)
	return draft, err
}

func (r *EvalReviews) SaveDraft(ctx context.Context, draft entity.EvalDraft, expected int) error {
	r.Files.mu.Lock()
	defer r.Files.mu.Unlock()
	if !evalName.MatchString(draft.ID) || ctx.Err() != nil {
		return fmt.Errorf("草稿输入无效")
	}
	if err := r.Files.Runs.MkdirAll("drafts", 0700); err != nil {
		return err
	}
	previous, err := r.GetDraft(ctx, draft.ID)
	if err == nil && previous.Version != expected {
		return fmt.Errorf("草稿已修改，请重新加载")
	}
	if err != nil && (!os.IsNotExist(err) || expected != 0) {
		return err
	}
	draft.Version = expected + 1
	return writeReviewJSON(r.Files.Runs, "drafts/"+draft.ID+".json", draft)
}

func (r *EvalReviews) DeleteDraft(ctx context.Context, id string) error {
	r.Files.mu.Lock()
	defer r.Files.mu.Unlock()
	if !evalName.MatchString(id) || ctx.Err() != nil {
		return fmt.Errorf("草稿 ID 无效")
	}
	return r.Files.Runs.Remove("drafts/" + id + ".json")
}

func (r *EvalReviews) Publish(ctx context.Context, dataset entity.EvalDataset) error {
	r.Files.mu.Lock()
	defer r.Files.mu.Unlock()
	if !evalName.MatchString(dataset.Name) {
		return fmt.Errorf("数据集名称无效")
	}
	// 目录名唯一，已发布版本永不覆盖。
	staging := ".publishing-" + dataset.Name
	if err := r.Files.Data.Mkdir(staging, 0700); err != nil {
		return err
	}
	for name, value := range map[string]any{
		"manifest.json": dataset.Manifest,
		"profiles.json": dataset.Profiles,
	} {
		if err := writeReviewJSON(r.Files.Data, staging+"/"+name, value); err != nil {
			return err
		}
	}
	file, err := r.Files.Data.OpenFile(staging+"/cases.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, testCase := range dataset.Cases {
		if err := encoder.Encode(testCase); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return r.Files.Data.Rename(staging, dataset.Name)
}

func (r *EvalReviews) SetBaseline(ctx context.Context, dataset, runID string) error {
	if !evalName.MatchString(dataset) || !evalName.MatchString(runID) {
		return fmt.Errorf("基线标识无效")
	}
	r.Files.mu.Lock()
	defer r.Files.mu.Unlock()
	if err := r.Files.Runs.MkdirAll("baselines", 0700); err != nil {
		return err
	}
	return writeReviewJSON(r.Files.Runs, "baselines/"+dataset+".json", map[string]string{"run_id": runID})
}

func (r *EvalReviews) Baseline(ctx context.Context, dataset string) (string, error) {
	if !evalName.MatchString(dataset) {
		return "", fmt.Errorf("数据集名称无效")
	}
	var value struct {
		RunID string `json:"run_id"`
	}
	err := readEvalJSON(r.Files.Runs, "baselines/"+dataset+".json", &value)
	return value.RunID, err
}

func (r *EvalReviews) SaveComparison(ctx context.Context, report entity.EvalComparison) error {
	if err := r.Files.Runs.MkdirAll("comparisons", 0700); err != nil {
		return err
	}
	path := "comparisons/" + report.RunID + "-" + report.CreatedAt.Format("20060102-150405.000000000") + ".json"
	return writeReviewJSON(r.Files.Runs, path, report)
}

// InvalidateDocument 删除未发布草稿，归档已发布版本，不篡改历史 run。
func (r *EvalReviews) InvalidateDocument(ctx context.Context, tenant, kbNo, docID string) error {
	drafts, err := r.ListDrafts(ctx)
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if draft.Tenant != tenant || draft.KBNo != kbNo {
			continue
		}
		matched := false
		remaining := make([]entity.EvalCandidate, 0, len(draft.Items))
		for _, item := range draft.Items {
			if docID == "" || item.DocID == docID {
				matched = true
			} else {
				remaining = append(remaining, item)
			}
		}
		if matched {
			if len(remaining) == 0 {
				if err := r.Files.Runs.Remove("drafts/" + draft.ID + ".json"); err != nil {
					return err
				}
			} else {
				draft.Items = remaining
				if err := r.SaveDraft(ctx, draft, draft.Version); err != nil {
					return err
				}
			}
		}
	}
	datasets := &EvalDatasets{Files: r.Files}
	list, err := datasets.List(ctx)
	if err != nil {
		return err
	}
	if err := r.Files.Data.MkdirAll(".retired", 0700); err != nil {
		return err
	}
	for _, info := range list {
		dataset, err := datasets.Load(ctx, info.Name)
		if err != nil {
			return err
		}
		matched := false
		for _, testCase := range dataset.Cases {
			if testCase.Tenant != tenant {
				continue
			}
			inScope := false
			for _, name := range testCase.KBNos {
				if name == kbNo {
					inScope = true
				}
			}
			if !inScope {
				continue
			}
			for _, source := range testCase.ReferenceSources {
				if docID == "" || source.DocID == docID {
					matched = true
				}
			}
		}
		if matched {
			if err := r.Files.Data.Rename(info.Name, ".retired/"+info.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
