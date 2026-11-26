// 本文件是题库草稿的后台出题：文档导入/删除联动与每 5 秒扫描一次的 worker。
// 草稿接口见 drafts.go，发布接口见 publish.go。

package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
	logsdk "github.com/PycMono/go-logger-sdk"
)

// Imported 只持久化候选来源。模型由单个后台 worker 执行，不阻塞导入。
func (s *DatasetService) Imported(ctx context.Context, tenant, kbNo, docID, name, content string) error {
	if !s.tasks.opts.Enabled {
		return nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	fileHash := sha256.Sum256([]byte(content))
	idHash := sha256.Sum256([]byte(tenant + "/" + kbNo + "/" + docID + "/" + hex.EncodeToString(fileHash[:])))
	draftID := "import-" + hex.EncodeToString(idHash[:16])
	if previous, err := s.repo.GetDraft(ctx, draftID); err == nil && previous.AutoGenerate {
		return nil // 相同原文的幂等重试保留审核与生成进度。
	}
	if err := s.repo.InvalidateDocument(ctx, tenant, kbNo, docID); err != nil {
		return err
	}
	if !s.autoGenerate || strings.TrimSpace(content) == "" {
		return nil
	}
	draft := entity.EvalDraft{
		ID: draftID, Tenant: tenant, KBNo: kbNo,
		CreatedAt: time.Now().UTC(), AutoGenerate: true, GenerationState: "pending",
		Items: []entity.EvalCandidate{},
	}
	lines := strings.Split(content, "\n")
	sections := []entity.EvalCandidate{}
	for start := 0; start < len(lines); {
		end := min(start+100, len(lines))
		for end > start+1 && len([]rune(strings.Join(lines[start:end], "\n"))) > 4800 {
			end--
		}
		source := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if length := len([]rune(source)); length >= 400 && length <= 4800 {
			hash := sha256.Sum256([]byte(source))
			sections = append(sections, entity.EvalCandidate{
				DocID: docID, DocName: name, SourceType: "original_markdown", SourceFile: name,
				FileHash: hex.EncodeToString(fileHash[:]), StartLine: start + 1, EndLine: end,
				Source: source, SourceHash: hex.EncodeToString(hash[:]), Status: "draft",
			})
		}
		start = end
	}
	count := min(s.autoSampleCount, len(sections))
	for index := 0; index < count; index++ {
		candidate := sections[(2*index+1)*len(sections)/(2*count)]
		candidate.ID = fmt.Sprintf("q%03d", index+1)
		candidate.Difficulty = []string{"简单", "中等", "困难", "困难"}[index%4]
		draft.Items = append(draft.Items, candidate)
	}
	if len(draft.Items) == 0 {
		return nil
	}
	return s.repo.SaveDraft(ctx, draft, 0)
}

// Removed 在文档或知识库删除后清理关联草稿和题库。
func (s *DatasetService) Removed(ctx context.Context, tenant, kbNo, docID string) error {
	if !s.tasks.opts.Enabled {
		return nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.repo.InvalidateDocument(ctx, tenant, kbNo, docID)
}

// Start 启动后台出题 worker。
func (s *DatasetService) Start(ctx context.Context) error {
	if !s.tasks.opts.Enabled {
		return nil
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s.workerCancel = cancel
	s.workerDone = make(chan struct{})
	go func() {
		defer close(s.workerDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				s.generatePending(workerCtx)
			}
		}
	}()
	return nil
}

// Stop 停止后台出题 worker 并等待退出。
func (s *DatasetService) Stop(ctx context.Context) error {
	if s.workerCancel == nil {
		return nil
	}
	s.workerCancel()
	select {
	case <-s.workerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── 内部实现 ──

// generatePending 扫描所有待生成草稿，每份推进一题。
func (s *DatasetService) generatePending(ctx context.Context) {
	drafts, err := s.repo.ListDrafts(ctx)
	if err != nil {
		logsdk.Error(ctx, "读取自动评测草稿失败", logsdk.Err(err))
		return
	}
	for _, draft := range drafts {
		if ctx.Err() != nil {
			return
		}
		if !draft.AutoGenerate || draft.GenerationState != "pending" {
			continue
		}
		s.generateOne(ctx, draft.ID)
	}
}

// maxGenerationAttempts 限制单题重试次数：坏题跳过，不拖停整批。
const maxGenerationAttempts = 3

// generateOne 每个周期每份草稿只生成一题：多份草稿轮流推进，暂停也不会等待整批结束。
func (s *DatasetService) generateOne(ctx context.Context, id string) {
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil || draft.GenerationState != "pending" || s.generator == nil {
		return
	}
	target := ""
	for _, candidate := range draft.Items {
		if candidate.Status == "draft" && candidate.Question == "" &&
			candidate.GenerationAttempts < maxGenerationAttempts {
			target = candidate.ID
			break
		}
	}
	if target == "" {
		if hasMissingQuestions(draft.Items) {
			draft.GenerationState = "failed"
			draft.GenerationError = "有题目连续 3 次生成失败，已停止；稍后点「继续生成」会重试"
		} else {
			draft.GenerationState = "completed"
			draft.GenerationError = ""
		}
		if err := s.repo.SaveDraft(ctx, draft, draft.Version); err != nil {
			logsdk.Error(ctx, "更新自动出题状态失败", logsdk.Err(err))
		}
		return
	}
	for index := range draft.Items {
		candidate := &draft.Items[index]
		if candidate.ID != target {
			continue
		}
		generated, err := s.generator.Generate(ctx, generationSource(*candidate))
		if err != nil {
			logsdk.Error(ctx, "后台出题失败，稍后重试",
				logsdk.Any("draft", id), logsdk.Any("case", target), logsdk.Err(err))
			// 记一次失败后继续：同一题最多尝试 3 次，其他题仍会推进。
			latest, loadErr := s.repo.GetDraft(ctx, id)
			if loadErr != nil || latest.GenerationState != "pending" {
				return
			}
			for position := range latest.Items {
				if latest.Items[position].ID == target {
					latest.Items[position].GenerationAttempts++
					break
				}
			}
			latest.GenerationError = fmt.Sprintf("第 %s 题生成失败，稍后自动重试", target)
			if saveErr := s.repo.SaveDraft(ctx, latest, latest.Version); saveErr != nil {
				logsdk.Error(ctx, "记录自动出题失败次数失败", logsdk.Err(saveErr))
			}
			return
		}
		candidate.Question, candidate.Reference = generated.Question, generated.Reference
		draft.GenerationError = ""
		// 保存失败通常是用户同时保存了审核修改；下个周期按最新版本重试。
		if err := s.repo.SaveDraft(ctx, draft, draft.Version); err != nil {
			logsdk.Error(ctx, "保存自动出题结果失败", logsdk.Err(err))
		}
		return
	}
}
