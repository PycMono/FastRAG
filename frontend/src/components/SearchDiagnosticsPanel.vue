<script setup lang="ts">
import { computed } from 'vue'
import { SlidersHorizontal } from '@lucide/vue'
import type { SearchDiagnostics, SearchMeta, SearchHit } from '../types'
const props = defineProps<{ value: SearchDiagnostics; meta?: SearchMeta; hit?: SearchHit }>()
const counts: Record<string, string> = {
  bm25: '关键词召回',
  knn: '向量召回',
  fused: '融合去重',
  alive: '有效片段',
  after_fusion_threshold: '融合阈值后',
  rerank_candidates: '送入精排',
  returned: '最终返回',
}
const timings: Record<string, string> = {
  knowledge_lookup: '知识库查询',
  embedding: '查询向量生成',
  retrieval: '两路召回（整体）',
  bm25: '关键词召回',
  knn: '向量召回',
  fusion: '融合排序',
  alive_filter: '文档存活校验',
  rerank: '精排阶段',
  final_filter: '结果过滤',
  total: '服务端总耗时',
}
const detail = computed(() => props.hit?.diagnostics)
const stages = computed(() =>
  Object.entries(props.value.timings_ms).filter(([key]) => key in timings),
)
const percent = computed(() => Math.round(props.value.dense_weight * 100))
function score(value?: number) {
  return value == null ? '—' : value.toFixed(4)
}
</script>
<template>
  <details class="retrieval-diagnostics">
    <summary>
      <SlidersHorizontal :size="16" /><strong>检索诊断</strong><span>候选、排序与耗时</span>
    </summary>
    <div class="diagnostics-body">
      <div class="diagnostic-counts">
        <div v-for="(label, key) in counts" :key="key">
          <span>{{ label }}</span><strong>{{ value.counts[key] ?? 0 }}</strong>
        </div>
      </div>
      <div class="diagnostic-columns">
        <section>
          <h3>本次生效参数</h3>
          <dl>
            <div>
              <dt>关键词召回上限</dt>
              <dd>{{ value.bm25_top }}</dd>
            </div>
            <div>
              <dt>向量召回上限</dt>
              <dd>{{ value.knn_top }}</dd>
            </div>
            <div>
              <dt>向量候选池 / 子句 / 分片</dt>
              <dd>{{ value.num_candidates }}</dd>
            </div>
            <div>
              <dt>精排候选预算</dt>
              <dd>{{ value.rerank_budget }}</dd>
            </div>
            <div>
              <dt>关键词 / 语义权重</dt>
              <dd>{{ 100 - percent }}% / {{ percent }}%</dd>
            </div>
            <div>
              <dt>RRF 排名常数</dt>
              <dd>{{ value.rank_constant }}</dd>
            </div>
            <div>
              <dt>精排实际状态</dt>
              <dd>{{ meta?.rerank_applied ? '已执行' : '未执行' }}</dd>
            </div>
          </dl>
          <p class="diagnostic-note">
            上限来自服务配置；纯关键词或纯语义模式会跳过另一路。精排预算受最终返回数量与 200
            条上限约束。
          </p>
        </section>
        <section>
          <h3>阶段耗时</h3>
          <dl>
            <div v-for="[key, time] in stages" :key="key">
              <dt>{{ timings[key] }}</dt>
              <dd>{{ time.toFixed(1) }} ms</dd>
            </div>
          </dl>
          <p class="diagnostic-note">
            两路召回并行执行，子阶段耗时不能直接相加。总耗时不含浏览器网络传输。
          </p>
        </section>
      </div>
      <section v-if="detail" class="diagnostic-hit">
        <h3>当前片段的排序来源</h3>
        <p>{{ hit?.title || hit?.doc_name }}</p>
        <div class="diagnostic-score-grid">
          <div>
            <span>关键词排名</span
            ><strong>{{ detail.bm25_rank ? `第 ${detail.bm25_rank} 名` : '未命中' }}</strong
            ><small>原始分 {{ score(detail.bm25_score) }}</small>
          </div>
          <div>
            <span>向量排名</span
            ><strong>{{ detail.knn_rank ? `第 ${detail.knn_rank} 名` : '未命中' }}</strong
            ><small>原始分 {{ score(detail.knn_score) }}</small>
          </div>
          <div>
            <span>融合分</span><strong>{{ score(detail.fusion_score) }}</strong
            ><small>按两路排名与权重计算</small>
          </div>
          <div>
            <span>精排分</span><strong>{{ score(detail.rerank_score) }}</strong
            ><small>{{
              detail.rerank_score == null ? '本片段未取得精排分' : '模型返回的相关性评分'
            }}</small>
          </div>
        </div>
        <p class="diagnostic-note">
          不同阶段分数尺度不同，均不表示答案正确率。选择其他片段可查看其排序来源。
        </p>
      </section>
    </div>
  </details>
</template>
<style scoped>
.retrieval-diagnostics {
  margin: 18px 0 24px;
  border: 1px solid #e1e5ec;
  border-radius: 18px;
  background: #fff;
  overflow: hidden;
}
summary {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 18px 22px;
  cursor: pointer;
  color: #3e4652;
}
summary strong {
  font-size: 14px;
}
summary span {
  margin-left: auto;
  font-size: 12px;
  color: #6e6e73;
}
.diagnostics-body {
  padding: 0 22px 22px;
}
.diagnostic-counts {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: 10px;
  padding: 18px 0;
  border-top: 1px solid #eceef2;
  border-bottom: 1px solid #eceef2;
}
.diagnostic-counts > div {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.diagnostic-counts span {
  font-size: 11px;
  color: #6e6e73;
}
.diagnostic-counts strong {
  font-size: 24px;
  font-weight: 550;
  color: #1d1d1f;
}
.diagnostic-columns {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 40px;
  padding-top: 22px;
}
h3 {
  margin: 0 0 16px;
  font-size: 14px;
  font-weight: 600;
  color: #1d1d1f;
}
dl {
  margin: 0;
}
dl > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin: 11px 0;
  font-size: 12px;
  line-height: 1.6;
}
dt {
  color: #6e6e73;
}
dd {
  margin: 0;
  color: #333b47;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.diagnostic-note {
  font-size: 11px;
  line-height: 1.8;
  color: #6e6e73;
  margin: 16px 0 0;
}
.diagnostic-hit {
  padding-top: 22px;
  margin-top: 22px;
  border-top: 1px solid #eceef2;
}
.diagnostic-hit > p:not(.diagnostic-note) {
  font-size: 12px;
  color: #515159;
}
.diagnostic-score-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin-top: 20px;
}
.diagnostic-score-grid > div {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.diagnostic-score-grid span,
.diagnostic-score-grid small {
  font-size: 11px;
  color: #6e6e73;
}
.diagnostic-score-grid strong {
  font-size: 17px;
  color: #1d1d1f;
  font-weight: 550;
}
@media (max-width: 700px) {
  summary {
    padding: 16px;
  }
  summary span {
    font-size: 10px;
  }
  .diagnostics-body {
    padding: 0 16px 18px;
  }
  .diagnostic-counts {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 20px 10px;
  }
  .diagnostic-columns {
    grid-template-columns: 1fr;
    gap: 22px;
  }
  .diagnostic-score-grid {
    grid-template-columns: 1fr 1fr;
    gap: 22px 12px;
  }
}
</style>
