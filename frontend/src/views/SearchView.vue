<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArrowUpRight, FileText, Search, SlidersHorizontal, Sparkles } from '@lucide/vue'
import { post } from '../api'
import type { SearchHit, Workspace, SearchResult } from '../types'
import SearchDiagnosticsPanel from '../components/SearchDiagnosticsPanel.vue'
import Notice from '../components/Notice.vue'
import Pagination from '../components/Pagination.vue'
import CopyButton from '../components/CopyButton.vue'
const props = defineProps<{ workspace: Workspace; initialQuery: string }>()
const emit = defineEmits<{ open: [id: string] }>()
const query = ref(props.initialQuery),
  limit = ref(10),
  dense = ref(0.5),
  minScore = ref(0),
  rerank = ref(true),
  embedModel = ref(''),
  rerankModel = ref(''),
  retrieveCount = ref<number | ''>(''),
  fusionMinScore = ref(0),
  includeDiagnostics = ref(true),
  resultInfo = ref<SearchResult>()
const busy = ref(false),
  error = ref(''),
  items = ref<SearchHit[]>([]),
  searched = ref(false),
  page = ref(1),
  elapsed = ref(''),
  lastQuery = ref(''),
  raw = ref<unknown>()
const expanded = ref<string[]>([])
const selectedId = ref('')
const selected = computed(
  () => shown.value.find((hit) => hit.chunk_id === selectedId.value) || shown.value[0],
)
const presets = [
  { id: 'hybrid', label: '均衡检索', weight: 0.5 },
  { id: 'keyword', label: '关键词优先', weight: 0 },
  { id: 'semantic', label: '语义优先', weight: 1 },
]
const preset = computed(() => presets.find((p) => p.weight === dense.value)?.id || 'custom')
const semanticPercent = computed({
  get: () => Math.round(dense.value * 100),
  set: (value: number | string) => {
    const percent = Number(value)
    if (Number.isFinite(percent)) dense.value = Math.min(100, Math.max(0, percent)) / 100
  },
})
const weightDescription = computed(() => {
  if (dense.value === 0) return '仅使用关键词检索，适合术语、编号和准确名称。'
  if (dense.value === 1) return '仅使用语义检索，适合自然语言问题和不同表述。'
  return '融合关键词匹配与语义理解；开启精排后，会再由模型调整结果排序。'
})
const historyKey = `fastrag.history.${JSON.stringify(props.workspace)}`
const history = ref<string[]>([])
try {
  const saved = JSON.parse(localStorage.getItem(historyKey) || '[]')
  if (Array.isArray(saved)) history.value = saved.filter((v) => typeof v === 'string').slice(0, 8)
} catch {
  /* storage unavailable */
}
function chooseHistory(value: string) {
  query.value = value
  search()
}
function clearHistory() {
  history.value = []
  try {
    localStorage.removeItem(historyKey)
  } catch {
    /* storage unavailable */
  }
}
function toggleHit(id: string) {
  expanded.value = expanded.value.includes(id)
    ? expanded.value.filter((i) => i !== id)
    : [...expanded.value, id]
}
function changePage(value: number) {
  selectedId.value = ''
  page.value = value
  document.getElementById('search-results')?.scrollIntoView({ block: 'start' })
}
const shown = computed(() => items.value.slice((page.value - 1) * 10, page.value * 10))
async function search() {
  if (!query.value.trim() || busy.value) return
  busy.value = true
  error.value = ''
  searched.value = false
  items.value = []
  raw.value = undefined
  resultInfo.value = undefined
  const body: Record<string, unknown> = {
    tenant: props.workspace.tenant,
    kb_nos: [props.workspace.kb_no],
    query: query.value.trim(),
    limit: limit.value,
    dense_weight: dense.value,
    min_score: rerank.value ? minScore.value : 0,
    fusion_min_score: fusionMinScore.value,
    diagnostics: includeDiagnostics.value,
    rerank_switch: rerank.value,
  }
  if (embedModel.value.trim()) body.embed_model = embedModel.value.trim()
  if (rerank.value && rerankModel.value.trim()) body.rerank_model = rerankModel.value.trim()
  if (rerank.value && retrieveCount.value !== '') body.retrieve_count = Number(retrieveCount.value)
  const start = performance.now()
  try {
    const result = await post<SearchResult>('/api/v1/search', body)
    items.value = result.items || []
    raw.value = result
    resultInfo.value = result
    lastQuery.value = String(body.query)
    searched.value = true
    elapsed.value = ((performance.now() - start) / 1000).toFixed(2)
    page.value = 1
    expanded.value = []
    history.value = [lastQuery.value, ...history.value.filter((q) => q !== lastQuery.value)].slice(
      0,
      8,
    )
    try {
      localStorage.setItem(historyKey, JSON.stringify(history.value))
    } catch {
      /* storage unavailable */
    }
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  if (props.initialQuery) search()
})
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>检索测试</h1>
        <p>用真实问题检验知识库，找到答案对应的文档片段。</p>
      </div>
      <span class="tag search-tag"><Sparkles :size="14" />混合检索</span>
    </div>
    <form class="panel search-panel" @submit.prevent="search">
      <label class="search-label" for="search-query">你想在知识库中查找什么？</label>
      <div class="search-input">
        <Search :size="21" /><input
          id="search-query"
          v-model="query"
          required
          maxlength="1000"
          placeholder="输入一个问题或关键词，例如：如何配置向量模型？"
        /><button class="button" :disabled="busy">{{ busy ? '检索中…' : '开始检索' }}</button>
      </div>
      <div class="search-presets" aria-label="检索模式">
        <span>检索模式</span
        ><button
          v-for="item in presets"
          :key="item.id"
          type="button"
          :class="{ active: preset === item.id }"
          :aria-pressed="preset === item.id"
          :disabled="busy"
          @click="dense = item.weight"
        >
          {{ item.label }}</button
        ><span v-if="preset === 'custom'" class="tag neutral">自定义权重</span>
      </div>
      <fieldset class="retrieval-balance" :disabled="busy">
        <div class="balance-heading">
          <div>
            <h2>混合检索权重</h2>
            <p>拖动滑块，调整两种检索方式的占比。</p>
          </div>
          <div class="balance-exact">
            <label for="semantic-percent">语义权重</label>
            <div>
              <input
                id="semantic-percent"
                v-model.number="semanticPercent"
                type="number"
                min="0"
                max="100"
                step="1"
                required
                aria-label="语义权重百分比"
              /><span>%</span>
            </div>
          </div>
        </div>
        <div class="balance-endpoints">
          <span>关键词 {{ 100 - semanticPercent }}%</span><span>语义 {{ semanticPercent }}%</span>
        </div>
        <input
          v-model.number="semanticPercent"
          class="balance-range"
          type="range"
          min="0"
          max="100"
          step="1"
          aria-label="语义检索权重"
          :aria-valuetext="`语义 ${semanticPercent}%，关键词 ${100 - semanticPercent}%`"
          aria-describedby="weight-description"
        />
        <div class="balance-scale" aria-hidden="true">
          <span>关键词匹配</span><span>均衡</span><span>语义理解</span>
        </div>
        <p id="weight-description" class="balance-description">{{ weightDescription }}</p>
        <small>权重控制融合排序，不代表命中率。调整后点击「开始检索」生效。</small>
      </fieldset>
      <div v-if="history.length" class="recent-queries">
        <span>最近检索</span
        ><button
          v-for="item in history"
          :key="item"
          type="button"
          :disabled="busy"
          :title="item"
          @click="chooseHistory(item)"
        >
          {{ item }}</button
        ><button type="button" class="clear-history" @click="clearHistory">清空记录</button>
      </div>
      <details class="advanced">
        <summary><SlidersHorizontal :size="16" />检索设置<span>调整召回与排序</span></summary>
        <fieldset :disabled="busy">
          <div class="field-grid search-settings">
            <label
              >返回结果数量<input
                v-model.number="limit"
                type="number"
                required
                min="1"
                max="100"
              /><small>最多返回 100 条；页面分页浏览本次结果。</small></label
            ><label v-if="rerank"
              >精排最低分数<input
                v-model.number="minScore"
                type="number"
                required
                min="0"
                step="any"
              /><small>仅过滤已取得精排分的结果；精排失败时跳过。0 为不过滤。</small></label
            ><label
              >融合分最低阈值<input
                v-model.number="fusionMinScore"
                type="number"
                min="0"
                step="0.0001"
                required
              /><small>在精排前过滤融合分；0 为不过滤。与精排分数独立。</small></label
            ><label
              >向量模型<input
                v-model="embedModel"
                maxlength="64"
                placeholder="默认使用服务配置"
              /><small>应与导入文档使用的向量模型一致。</small></label
            ><label v-if="rerank"
              >精排模型<input
                v-model="rerankModel"
                maxlength="64"
                placeholder="默认使用服务配置" /></label
            ><label v-if="rerank"
              >精排候选数量<input
                v-model="retrieveCount"
                type="number"
                min="1"
                max="200"
                placeholder="默认使用服务配置"
              /><small>融合后最多送入精排的数量，不少于返回数量，最多 200 条。</small></label
            >
          </div>
          <label class="checkbox-line"
            ><input v-model="rerank" type="checkbox" /><span
              >启用模型精排<small>对召回结果再次评估相关性，让更匹配的片段排在前面。</small></span
            ></label
          >
          <label class="checkbox-line"
            ><input v-model="includeDiagnostics" type="checkbox" /><span
              >记录本次检索诊断<small>返回各路排名、评分来源、候选数量与阶段耗时。</small></span
            ></label
          >
        </fieldset>
      </details>
      <Notice :message="error" />
    </form>
    <div v-if="busy" class="panel empty-state" role="status">
      <span class="spinner large"></span>
      <h3>正在查找相关内容</h3>
      <p>检索与排序完成后，结果会显示在这里。</p>
    </div>
    <template v-else-if="searched"
      ><div id="search-results" class="results-heading">
        <h2>
          检索结果 <span class="count">{{ items.length }}</span>
        </h2>
        <span class="muted">用时 {{ elapsed }} 秒 · 按相关性排序</span>
      </div>
      <p class="query-caption">
        检索内容：{{ lastQuery
        }}<span v-if="resultInfo?.meta">
          ·
          {{
            resultInfo.meta.score_type === 'rerank'
              ? '精排评分'
              : resultInfo.meta.score_type === 'mixed'
                ? '部分精排评分'
                : '融合评分'
          }}</span
        >
      </p>
      <div v-if="resultInfo?.meta?.warnings?.length" class="search-degradation" role="status">
        <p v-for="warning in resultInfo.meta.warnings" :key="warning">{{ warning }}</p>
      </div>
      <SearchDiagnosticsPanel
        v-if="resultInfo?.diagnostics"
        :value="resultInfo.diagnostics"
        :meta="resultInfo.meta"
        :hit="selected"
      />
      <div v-if="!items.length" class="panel empty-state">
        <Search :size="38" />
        <h3>没有找到相关内容</h3>
        <p>试试更简短的关键词、降低最低分数，或确认文档已经导入当前知识库。</p>
      </div>
      <div v-if="items.length" class="search-workbench">
        <div class="search-matches" aria-label="检索结果列表">
          <div class="matches-label">
            相关片段 <span>{{ items.length }} 条</span>
          </div>
          <button
            v-for="(hit, i) in shown"
            :key="hit.chunk_id"
            :class="['match-item', { selected: selected?.chunk_id === hit.chunk_id }]"
            :aria-pressed="selected?.chunk_id === hit.chunk_id"
            @click="selectedId = hit.chunk_id"
          >
            <div class="match-top">
              <span class="result-rank">{{ (page - 1) * 10 + i + 1 }}</span><span
                class="match-score"
                >{{ hit.score.toFixed(4) }}</span
              >
            </div>
            <strong>{{ hit.title || hit.doc_name }}</strong>
            <p>{{ hit.content.slice(0, 95) }}…</p>
            <small><FileText :size="12" />{{ hit.doc_name }}</small>
          </button>
        </div>
        <article v-if="selected" class="result-reader">
          <header class="reader-heading">
            <div>
              <span class="reader-kicker">片段预览</span>
              <h2>{{ selected.title || selected.doc_name }}</h2>
              <p>{{ selected.heading_path || selected.doc_name }}</p>
            </div>
            <span class="score-badge"
              >{{
                selected.diagnostics
                  ? selected.diagnostics.rerank_score != null
                    ? '精排分'
                    : '融合分'
                  : '相关性'
              }}
              {{ selected.score.toFixed(4) }}</span
            >
          </header>
          <div class="reader-toolstrip">
            <span><FileText :size="14" />{{ selected.doc_name }}</span
            ><CopyButton :text="selected.content" label="复制片段" /><button
              class="text-button"
              @click="emit('open', selected.doc_id)"
            >
              查看原文<ArrowUpRight :size="14" />
            </button>
          </div>
          <div class="reader-page">
            <pre
              :class="['result-content', { collapsed: !expanded.includes(selected.chunk_id) }]"
              >{{ selected.content }}</pre>
            <button class="text-button expand-content" @click="toggleHit(selected.chunk_id)">
              {{ expanded.includes(selected.chunk_id) ? '收起片段' : '展开完整片段' }}
            </button>
          </div>
        </article>
      </div>
      <Pagination
        v-if="items.length"
        :page="page"
        :pages="Math.ceil(items.length / 10)"
        :total="items.length"
        @change="changePage"
      />
      <details class="debug-data">
        <summary>查看响应数据</summary>
        <pre>{{ JSON.stringify(raw, null, 2) }}</pre>
      </details>
      <p class="footnote">最多返回本次设置的结果数量，分页仅浏览这次检索返回的片段。</p></template
    >
    <div v-else-if="!error" class="panel empty-state search-empty">
      <span class="search-empty-icon"><Search :size="32" /></span>
      <h3>从一个问题开始</h3>
      <p>输入关键词或完整的问题，系统会结合语义和关键词查找相关片段。</p>
      <div class="search-hints">
        <span>语义匹配</span><span>关键词召回</span><span>原文溯源</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.retrieval-balance {
  margin: 22px 0 0;
  padding: 24px;
  min-width: 0;
  border: 1px solid #e5e8ee;
  border-radius: 18px;
  background: #fafbfd;
}
.balance-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}
.balance-heading h2 {
  margin: 0;
  color: #1d1d1f;
  font-size: 15px;
  font-weight: 600;
}
.balance-heading p {
  margin: 6px 0 0;
  color: #6e6e73;
  font-size: 12px;
}
.balance-exact {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}
.balance-exact label {
  margin: 0;
  color: #6e6e73;
  font-size: 12px;
}
.balance-exact > div {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #515159;
}
.balance-exact input {
  width: 72px;
  min-height: 36px;
  padding: 7px 8px;
  background: #fff;
  border: 1px solid #d7dfe9;
  border-radius: 9px;
  font-size: 14px;
  text-align: center;
}
.balance-endpoints {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-top: 25px;
  color: #3e4652;
  font-size: 13px;
  font-weight: 550;
  font-variant-numeric: tabular-nums;
}
.balance-endpoints span:last-child {
  color: #0071e3;
}
.retrieval-balance input.balance-range {
  display: block;
  width: 100%;
  height: 30px;
  min-height: 30px;
  padding: 0;
  margin: 7px 0 0;
  border: 0;
  border-radius: 8px;
  box-shadow: none;
  background: transparent;
  accent-color: #0071e3;
  cursor: pointer;
}
.balance-range:focus-visible {
  outline: 3px solid #83baff;
  outline-offset: 4px;
}
.balance-scale {
  display: flex;
  justify-content: space-between;
  color: #767680;
  font-size: 11px;
}
.balance-description {
  margin: 20px 0 5px;
  color: #515159;
  line-height: 1.65;
  font-size: 12px;
}
.retrieval-balance > small {
  display: block;
  color: #6e6e73;
  font-size: 11px;
  line-height: 1.7;
}
.retrieval-balance:disabled {
  opacity: 0.65;
}
@media (max-width: 600px) {
  .retrieval-balance {
    padding: 18px 16px;
    border-radius: 14px;
  }
  .balance-heading {
    align-items: flex-start;
    gap: 12px;
  }
  .balance-exact {
    flex-direction: column;
    align-items: flex-end;
    gap: 5px;
  }
  .balance-heading p {
    max-width: 170px;
    line-height: 1.6;
  }
  .balance-heading h2 {
    font-size: 14px;
  }
}
</style>
<style scoped>
.search-degradation {
  border: 1px solid #ecd6a5;
  border-radius: 12px;
  background: #fffaf0;
  padding: 10px 18px;
  margin: 16px 0;
  color: #755a27;
}
.search-degradation p {
  font-size: 12px;
  line-height: 1.8;
  margin: 4px 0;
}
</style>
