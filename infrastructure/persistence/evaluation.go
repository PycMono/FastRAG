package persistence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
)

var evalName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// EvalFiles 由单个服务实例独占。os.Root 防止数据集路径越界。
type EvalFiles struct {
	Data *os.Root
	Runs *os.Root
	mu   sync.Mutex
}

func (f *EvalFiles) Close() error {
	return errors.Join(f.Data.Close(), f.Runs.Close())
}

func readEvalJSON(root *os.Root, path string, out any) error {
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	const max = 32 << 20
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return err
	}
	if len(data) > max {
		return fmt.Errorf("评测文件超过 32 MiB")
	}

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("评测文件包含多余 JSON")
	}

	return nil
}

// EvalDatasets 从只读数据目录加载固定测试集。
type EvalDatasets struct {
	Files *EvalFiles
}

func (d *EvalDatasets) List(ctx context.Context) ([]entity.EvalDatasetInfo, error) {
	dir, err := d.Files.Data.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	datasets := []entity.EvalDatasetInfo{}
	for _, entry := range entries {
		if !entry.IsDir() || !evalName.MatchString(entry.Name()) {
			continue
		}
		dataset, err := d.Load(ctx, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("数据集 %s 无效: %w", entry.Name(), err)
		}
		info := entity.EvalDatasetInfo{
			Name:           dataset.Name,
			Version:        dataset.Manifest.DatasetVersion,
			Cases:          len(dataset.Cases),
			Profiles:       len(dataset.Profiles),
			SearchRequests: len(dataset.Cases) * len(dataset.Profiles),
		}
		if len(dataset.Profiles) > 0 {
			p := dataset.Profiles[0]
			rerank := "精排关"
			if p.RerankSwitch {
				rerank = "精排开"
			}
			info.ProfileSummary = fmt.Sprintf(
				"%s · 返回 %d 条 · 召回 %d 条 · %s",
				p.Name,
				p.Limit,
				p.RetrieveCount,
				rerank,
			)
			if len(dataset.Profiles) > 1 {
				info.ProfileSummary += fmt.Sprintf(" · 共 %d 组配置", len(dataset.Profiles))
			}
		}
		if len(dataset.Cases) > 0 {
			info.Tenant = dataset.Cases[0].Tenant
			info.KBNos = dataset.Cases[0].KBNos
		}
		datasets = append(datasets, info)
	}

	slices.SortFunc(datasets, func(a, b entity.EvalDatasetInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	return datasets, nil
}

func (d *EvalDatasets) Load(ctx context.Context, name string) (entity.EvalDataset, error) {
	var dataset entity.EvalDataset
	if !evalName.MatchString(name) {
		return dataset, fmt.Errorf("数据集名称无效")
	}
	if err := ctx.Err(); err != nil {
		return dataset, err
	}

	dataset.Name = name
	if err := readEvalJSON(d.Files.Data, name+"/manifest.json", &dataset.Manifest); err != nil {
		return dataset, err
	}
	if err := readEvalJSON(d.Files.Data, name+"/profiles.json", &dataset.Profiles); err != nil {
		return dataset, err
	}

	file, err := d.Files.Data.Open(name + "/cases.jsonl")
	if err != nil {
		return dataset, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(io.LimitReader(file, (8<<20)+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	total := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return dataset, err
		}
		total += len(scanner.Bytes()) + 1
		if total > 8<<20 {
			return dataset, fmt.Errorf("用例文件过大")
		}
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}

		var testCase entity.EvalCase
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&testCase); err != nil {
			return dataset, err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return dataset, fmt.Errorf("用例存在多余 JSON")
		}
		dataset.Cases = append(dataset.Cases, testCase)
		if len(dataset.Cases) > 1000 {
			return dataset, fmt.Errorf("用例超过 1000 条")
		}
	}

	return dataset, scanner.Err()
}

// EvalRuns 以原子文件快照保存任务，终态结果不可覆盖。
type EvalRuns struct {
	Files *EvalFiles
}

func (r *EvalRuns) write(run entity.EvalRun, create bool) error {
	if !evalName.MatchString(run.ID) {
		return fmt.Errorf("run ID 无效")
	}

	r.Files.mu.Lock()
	defer r.Files.mu.Unlock()

	path := run.ID + ".json"
	old, err := r.Files.Runs.Open(path)
	if err == nil {
		if create {
			old.Close()
			return fmt.Errorf("run 已存在")
		}
		var state struct {
			Status string `json:"status"`
		}
		err = json.NewDecoder(old).Decode(&state)
		old.Close()
		if err != nil {
			return err
		}
		if state.Status != "queued" && state.Status != "running" {
			return fmt.Errorf("终态任务不可覆盖")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if !create {
		return err
	}

	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return fmt.Errorf("任务快照超过 32 MiB，请缩小数据集")
	}

	tempPath := run.ID + ".tmp"
	file, err := r.Files.Runs.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}

	return r.Files.Runs.Rename(tempPath, path)
}

func (r *EvalRuns) Create(ctx context.Context, run entity.EvalRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return r.write(run, true)
}

func (r *EvalRuns) Save(ctx context.Context, run entity.EvalRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return r.write(run, false)
}

func (r *EvalRuns) Get(ctx context.Context, id string) (entity.EvalRun, error) {
	var run entity.EvalRun
	if err := ctx.Err(); err != nil {
		return run, err
	}
	if !evalName.MatchString(id) {
		return run, fmt.Errorf("run ID 无效")
	}

	err := readEvalJSON(r.Files.Runs, id+".json", &run)
	return run, err
}

func (r *EvalRuns) List(ctx context.Context) ([]entity.EvalRun, error) {
	dir, err := r.Files.Runs.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	runs := []entity.EvalRun{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		run, err := r.Get(ctx, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		run.Dataset.Cases = nil
		runs = append(runs, run)
	}

	slices.SortFunc(runs, func(a, b entity.EvalRun) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return runs, nil
}

func (r *EvalRuns) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !evalName.MatchString(id) {
		return fmt.Errorf("run ID 无效")
	}

	r.Files.mu.Lock()
	defer r.Files.mu.Unlock()

	if err := r.Files.Runs.Remove(id + ".json"); err != nil {
		return err
	}
	// 删除以该 run 为基线的记录。
	if dir, err := r.Files.Runs.Open("baselines"); err == nil {
		entries, _ := dir.ReadDir(-1)
		dir.Close()
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := "baselines/" + entry.Name()
			var baseline struct {
				RunID string `json:"run_id"`
			}
			if readEvalJSON(r.Files.Runs, path, &baseline) == nil && baseline.RunID == id {
				_ = r.Files.Runs.Remove(path)
			}
		}
	}
	// 删除引用该 run 的对比报告。
	if dir, err := r.Files.Runs.Open("comparisons"); err == nil {
		entries, _ := dir.ReadDir(-1)
		dir.Close()
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := "comparisons/" + entry.Name()
			var report entity.EvalComparison
			if readEvalJSON(r.Files.Runs, path, &report) == nil &&
				(report.RunID == id || report.BaselineID == id) {
				_ = r.Files.Runs.Remove(path)
			}
		}
	}
	return nil
}

func (r *EvalRuns) Recover(ctx context.Context) error {
	runs, err := r.List(ctx)
	if err != nil {
		return err
	}

	for _, run := range runs {
		if run.Status != "running" && run.Status != "queued" {
			continue
		}
		full, err := r.Get(ctx, run.ID)
		if err != nil {
			return err
		}
		full.Status = "interrupted"
		full.Error = "服务已重启，不自动续跑"
		now := time.Now().UTC()
		full.FinishedAt = &now
		if err := r.Save(ctx, full); err != nil {
			return err
		}
	}
	return nil
}
