// 本文件把保存的逐题结果投影成检索工程指标，不调用模型，也不重新检索。
// 评分与任务执行见 task.go。

package evaluation

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
)

// ── 内部实现 ──

// percentile 计算给定分位的值。
func percentile(values []float64, quantile float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	value := values[max(0, int(math.Ceil(float64(len(values))*quantile))-1)]
	return &value
}

// retrievalDiagnostics 不调用模型，历史记录也可按保存的原始结果重新投影。
func retrievalDiagnostics(run entity.EvalRun) map[string]vo.EvalDiagnosticsVO {
	profiles := map[string][]entity.EvalRow{}
	for _, row := range run.Rows {
		profiles[row.Profile] = append(profiles[row.Profile], row)
	}
	labels := map[string][]string{}
	for _, testCase := range run.Dataset.Cases {
		if testCase.Answerable && len(testCase.ReferenceContextIDs) > 0 {
			labels[testCase.ID] = testCase.ReferenceContextIDs
		}
	}
	result := map[string]vo.EvalDiagnosticsVO{}
	for profile, rows := range profiles {
		stats := vo.EvalDiagnosticsVO{Requests: len(rows)}
		latencies := []float64{}
		scoringLatencies := []float64{}
		totalResults, characters, duplicates, applicable, scored := 0, 0, 0, 0, 0
		recall, precision, reciprocalRank, success, ndcg := 0.0, 0.0, 0.0, 0.0, 0.0
		for _, row := range rows {
			if row.Input != nil {
				applicable += len(row.Input.Metrics)
			}
			if row.Score != nil {
				for _, metric := range row.Score.Metrics {
					if metric.Status == "not_applicable" {
						applicable--
					}
					if metric.Status == "ok" {
						scored++
					}
				}
			}
			if row.ScoringLatencyMS > 0 {
				scoringLatencies = append(scoringLatencies, row.ScoringLatencyMS)
			}
			if row.Retrieval == nil || (row.RetrievalStatus != "ok" && row.RetrievalStatus != "empty") {
				continue
			}
			stats.Successful++
			latencies = append(latencies, row.RetrievalLatencyMS)
			if len(row.Retrieval.Items) == 0 {
				stats.Empty++
			}
			seenContent := map[string]bool{}
			for _, hit := range row.Retrieval.Items {
				totalResults++
				characters += utf8.RuneCountInString(hit.Content)
				content := strings.Join(strings.Fields(hit.Content), " ")
				if seenContent[content] {
					duplicates++
				}
				seenContent[content] = true
			}
			relevant := map[string]bool{}
			for _, id := range labels[row.CaseID] {
				relevant[id] = true
			}
			if len(relevant) == 0 {
				continue
			}
			stats.LabelledQueries++
			hits, first, dcg := 0, 0, 0.0
			seenID := map[string]bool{}
			for index, hit := range row.Retrieval.Items[:min(5, len(row.Retrieval.Items))] {
				if relevant[hit.ChunkID] && !seenID[hit.ChunkID] {
					hits++
					if first == 0 {
						first = index + 1
					}
					dcg += 1 / math.Log2(float64(index+2))
				}
				seenID[hit.ChunkID] = true
			}
			recall += float64(hits) / float64(len(relevant))
			precision += float64(hits) / 5 // 不足5条时缺失位置不算相关。
			if first > 0 {
				reciprocalRank += 1 / float64(first)
				success++
			}
			ideal := 0.0
			for index := 0; index < min(5, len(relevant)); index++ {
				ideal += 1 / math.Log2(float64(index+2))
			}
			ndcg += dcg / ideal
		}
		stats.FailureRate = float64(stats.Requests-stats.Successful) / float64(stats.Requests)
		if stats.Successful > 0 {
			stats.EmptyRate = float64(stats.Empty) / float64(stats.Successful)
			stats.AverageResults = float64(totalResults) / float64(stats.Successful)
			stats.AverageCharacters = float64(characters) / float64(stats.Successful)
		}
		if totalResults > 0 {
			stats.DuplicateRate = float64(duplicates) / float64(totalResults)
		}
		if applicable > 0 {
			stats.ScoreCoverage = float64(scored) / float64(applicable)
		}
		stats.P50MS, stats.P95MS = percentile(latencies, .5), percentile(latencies, .95)
		stats.ScoringP95MS = percentile(scoringLatencies, .95)
		if stats.LabelledQueries > 0 {
			average := func(sum float64) *float64 { value := sum / float64(stats.LabelledQueries); return &value }
			stats.RecallAt5, stats.PrecisionAt5 = average(recall), average(precision)
			stats.MRRAt5, stats.SuccessAt5, stats.BinaryNDCGAt5 = average(reciprocalRank), average(success), average(ndcg)
		}
		result[profile] = stats
	}
	return result
}
