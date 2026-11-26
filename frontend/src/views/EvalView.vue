<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { adminRequest } from '../admin'
import AdminGate from '../components/AdminGate.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Pagination from '../components/Pagination.vue'
import { BookOpen, ChevronRight, Play, RefreshCw, Trash2, X } from '@lucide/vue'
import type { Workspace } from '../types'
import EvaluationReview from '../components/EvaluationReview.vue'
import { evaluationConclusion } from '../evaluationResult'

type Dataset = {
  tenant?: string
  kb_nos?: string[]
  name: string
  version: string
  cases: number
  profiles: number
  search_requests: number
  profile_summary?: string
}

type Summary = {
  mean: number | null
  ok: number
  errors: number
  not_applicable: number
  coverage: number
}

type Run = {
  diagnostics?: Record<
    string,
    {
      requests: number
      successful: number
      empty: number
      failure_rate: number
      empty_rate: number
      p50_ms: number | null
      p95_ms: number | null
      scoring_p95_ms: number | null
      average_results: number
      average_characters: number
      duplicate_rate: number
      score_coverage: number
      labelled_queries: number
      recall_at_5: number | null
      precision_at_5: number | null
      mrr_at_5: number | null
      success_at_5: number | null
      binary_ndcg_at_5: number | null
    }
  >
  created_at?: string
  failure_reasons?: string[]
  scoring_failures?: number
  purpose?: string
  id: string
  dataset?: string
  profile_summary?: string
  experiment_name: string
  status: string
  done: number
  expected: number
  retrieval_failures: number
  boundary_failures: number
  rerank_degraded: number
  summary: Record<string, Summary>
}

type Row = {
  case_id: string
  question: string
  reference: string
  profile: string
  retrieval_status: string
  retrieval_error?: string
  retrieval_latency_ms: number
  scoring_latency_ms: number
  boundary_violation: boolean
  retrieval?: { items: { chunk_id: string; title: string; content: string }[] }
  score?: {
    metrics: {
      name: string
      status: string
      value: number | null
      error?: string
      error_details?: Record<string, string | number>
      computed_by?: string
    }[]
  }
}

const props = defineProps<{ active: boolean; workspace: Workspace }>()
const mode = ref<'home' | 'diagnostic' | 'review' | 'history' | 'result'>('home')
const report = ref<{
  verdict: string
  reasons: string[]
  deltas: Record<string, number>
  regressed_cases: string[]
}>()
const baselineMessage = ref('')
const policy = ref({ max_recall_drop: 0.05, max_precision_drop: 0.05, max_latency_ratio: 3 })
const question = ref('')
const datasets = ref<Dataset[]>([]),
  runs = ref<Run[]>([]),
  selected = ref('')
const name = ref(''),
  hypothesis = ref('')
const current = ref<Run>(),
  rows = ref<Row[]>([]),
  page = ref(1),
  total = ref(0)
const busy = ref(false),
  error = ref(''),
  unlocked = ref(false)
const notice = ref('')
type ConfirmOptions = {
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}
const confirmDialog = ref<(ConfirmOptions & { resolve: (value: boolean) => void }) | null>(null)
function askConfirm(options: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    confirmDialog.value = { ...options, resolve }
  })
}
function resolveConfirm(value: boolean) {
  const dialog = confirmDialog.value
  confirmDialog.value = null
  dialog?.resolve(value)
}
const chosen = computed(() => datasets.value.find((dataset) => dataset.name === selected.value))
function datasetTitle(dataset: Dataset) {
  const prefix = dataset.name.startsWith('md-original')
    ? '原文题库'
    : dataset.name.startsWith('sample')
      ? '样例题库'
      : '题库'
  const kb = dataset.kb_nos?.join('、') || dataset.tenant || '知识库'
  return `${kb} · ${prefix}`
}
function datasetLabel(dataset: Dataset) {
  return `${datasetTitle(dataset)} · ${dataset.cases} 题`
}
const currentDatasetTitle = computed(() =>
  chosen.value ? datasetTitle(chosen.value) : '尚未选择题库',
)
function formatRunName(name?: string) {
  const value = name || '评估'
  if (value.startsWith('失败项重评')) return value
  if (!value.includes('失败项重评')) return value
  const base = value.replace(/( · 失败项重评)+/g, '').trim()
  return `${base || '评估'} · 重评`
}
const pages = computed(() => Math.max(1, Math.ceil(total.value / 20)))
const metricNames: Record<string, string> = {
  ContextRecall: '必要信息覆盖',
  ContextPrecision: '证据排序质量',
  ContextRelevance: '上下文相关性',
}
const statusNames: Record<string, string> = {
  queued: '等待执行',
  running: '正在评测',
  succeeded: '执行完成',
  partial: '部分步骤失败',
  failed: '执行失败',
  cancelled: '已取消',
  timed_out: '任务超时',
  interrupted: '执行中断',
  ok: '成功',
  empty: '未找到结果',
  error: '失败',
  timeout: '超时',
  not_applicable: '不适用',
}
const scoringFailed = computed(() =>
  Object.values(current.value?.summary || {}).some((summary) => summary.errors > 0),
)
const resultMetrics = computed(() => {
  const summaries = current.value?.summary || {}
  return [
    { name: 'ContextRelevance', title: '上下文相关性', description: '找到的资料是否与问题相关' },
    { name: 'ContextRecall', title: '必要信息覆盖', description: '是否找全参考答案需要的证据' },
    { name: 'ContextPrecision', title: '证据排序质量', description: '有用的资料是否排在前面；有参考片段 ID 时按 ID 确定性计算' },
  ].map((metric) => {
    const entries = Object.entries(summaries).filter(([key]) => key.endsWith('/' + metric.name))
    return { ...metric, entries }
  })
})
const diagnosticResult = computed(
  () =>
    current.value?.purpose === 'diagnostic' ||
    (rows.value.length > 0 && rows.value.every((row) => !row.reference)),
)
const diagnosticRow = computed(() => rows.value[0])
const diagnosticQuestion = computed(() => diagnosticRow.value?.question || '')
const diagnosticEvidence = computed(() => diagnosticRow.value?.retrieval?.items || [])
const diagnosticRelevance = computed(() => {
  const entry = Object.entries(current.value?.summary || {}).find(([key]) =>
    key.endsWith('/ContextRelevance'),
  )
  const mean = entry?.[1]?.mean
  return mean === null || mean === undefined ? null : mean * 100
})
const diagnosticStats = computed(() => Object.values(current.value?.diagnostics || {})[0])
function reDiagnose() {
  setMode('diagnostic')
  requestAnimationFrame(() => {
    const input = document.querySelector('.eval-diagnostic input') as HTMLInputElement | null
    input?.focus()
    input?.select()
  })
}
const conclusion = computed(() =>
  evaluationConclusion({
    status: current.value?.status || 'queued',
    diagnostic: diagnosticResult.value,
    retrievalFailures: current.value?.retrieval_failures || 0,
    boundaryFailures: current.value?.boundary_failures || 0,
    scoringFailed: scoringFailed.value,
    hasReferenceMetrics: resultMetrics.value
      .filter((metric) => metric.name !== 'ContextRelevance')
      .every(
        (metric) =>
          metric.entries.length > 0 &&
          metric.entries.every(
            ([, summary]) => summary.mean !== null && summary.ok > 0 && summary.errors === 0,
          ),
      ),
    report: report.value,
  }),
)
const failureOnly = ref(false)
const displayedRows = computed(() =>
  rows.value.filter(
    (row) =>
      !failureOnly.value ||
      row.retrieval_status === 'error' ||
      row.retrieval_status === 'timeout' ||
      row.score?.metrics.some((metric) =>
        ['error', 'timeout', 'cancelled'].includes(metric.status),
      ),
  ),
)

const failedMetricsOnPage = computed(() =>
  rows.value.flatMap((row) =>
    (row.score?.metrics || [])
      .filter((metric) => ['error', 'timeout', 'cancelled'].includes(metric.status))
      .map((metric) => ({ row, metric })),
  ),
)

const latestRun = computed(() => runs.value.find((run) => run.dataset === selected.value))
const recentRuns = computed(() => runs.value.slice(0, 3))
const cancelled = computed(() => current.value?.status === 'cancelled')
const canSetBaseline = computed(
  () =>
    current.value?.status === 'succeeded' &&
    current.value?.purpose === 'regression' &&
    (current.value?.retrieval_failures || 0) === 0 &&
    (current.value?.boundary_failures || 0) === 0 &&
    !scoringFailed.value,
)
const failureCount = computed(
  () => current.value?.scoring_failures || failedMetricsOnPage.value.length,
)
const metricTiles = computed(() =>
  resultMetrics.value.map((metric) => {
    const entries = metric.entries as [string, Summary][]
    const first = entries[0]?.[1]
    return {
      name: metric.name,
      title: metric.title,
      description: metric.description,
      value: first?.mean ?? null,
      ok: first?.ok ?? 0,
      errors: first?.errors ?? 0,
      coverage: first?.coverage ?? 0,
      profiles: entries.length,
    }
  }),
)
const primaryLabel = computed(() => {
  if (!current.value) return '返回记录'
  if (!terminals.has(current.value.status)) return '取消任务'
  if (diagnosticResult.value) return '运行完整题库'
  if (current.value.status === 'cancelled') return '重新运行'
  if (current.value.status === 'partial') {
    return failureCount.value > 0 ? '仅重评失败项' : '重新运行'
  }
  if (current.value.status === 'failed' || current.value.status === 'timed_out') return '重新运行'
  if (current.value.status === 'succeeded') {
    return baselineMessage.value ? '与基线比较' : '设为基线'
  }
  return '返回记录'
})
function primaryAction() {
  if (!current.value) return setMode('history')
  if (!terminals.has(current.value.status)) return cancel()
  if (diagnosticResult.value) return setMode('home')
  if (
    current.value.status === 'cancelled' ||
    current.value.status === 'failed' ||
    current.value.status === 'timed_out'
  )
    return create()
  if (current.value.status === 'partial') {
    return failureCount.value > 0 ? retryFailed() : create()
  }
  if (current.value.status === 'succeeded') {
    return baselineMessage.value ? compare() : setBaseline()
  }
}
function setMode(next: 'home' | 'diagnostic' | 'review' | 'history') {
  mode.value = next
  error.value = ''
  notice.value = ''
}
function dismissError() {
  error.value = ''
}
function relativeTime(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const now = new Date()
  const sameDay = date.toDateString() === now.toDateString()
  const time = date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  if (sameDay) return `今天 ${time}`
  const yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  if (date.toDateString() === yesterday.toDateString()) return `昨天 ${time}`
  return date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' })
}
function friendlyError(message?: string) {
  const text = message || ''
  if (text.includes('评测功能未开启')) return '评测功能未开启，请先在配置中打开评估并填写模型。'
  if (text.includes('已有评测任务')) return '已有评估任务在运行，请等它结束后再试。'
  if (text.includes('评测输入无效'))
    return '请检查题库：至少 3 道已通过题目，且题库与实验名称不能为空。'
  if (text.includes('评分调用失败') || text.includes('结构化输出'))
    return '评估模型调用失败。可先仅重评失败项；仍失败时检查模型配置。'
  if (text.includes('context canceled')) return '任务已取消。'
  return text
}
const pageTitle = computed(() => {
  if (mode.value === 'review') return '题库'
  if (mode.value === 'history') return '评估记录'
  if (mode.value === 'diagnostic') return '单题诊断'
  return '检索效果评估'
})
const pageSubtitle = computed(() => {
  if (mode.value === 'review') return '出题一次，之后重复评估。核对至少 3 道即可发布。'
  if (mode.value === 'history') return '查看每次运行的结果和失败原因。'
  if (mode.value === 'diagnostic') return '直接查一个问题，只评估相关性。'
  return '用固定题目检查：资料找全了吗？有用的内容排前面了吗？'
})
const historyFilter = ref<'all' | 'active' | 'failed' | 'succeeded' | 'library'>('all')
const filteredRuns = computed(() =>
  runs.value.filter((run) => {
    if (historyFilter.value === 'active')
      return !['succeeded', 'partial', 'failed', 'cancelled', 'timed_out', 'interrupted'].includes(
        run.status,
      )
    if (historyFilter.value === 'failed')
      return run.status === 'partial' || run.status === 'failed' || run.status === 'timed_out'
    if (historyFilter.value === 'succeeded') return run.status === 'succeeded'
    if (historyFilter.value === 'library') return run.purpose !== 'diagnostic'
    return true
  }),
)

function revealFailure(caseID: string) {
  failureOnly.value = true
  requestAnimationFrame(() => {
    const element = document.getElementById('eval-case-' + caseID) as HTMLDetailsElement | null
    if (element) {
      element.open = true
      element.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
  })
}

async function retryFailed() {
  if (!current.value || busy.value) return
  if (
    !(await askConfirm({
      title: '仅重评失败项',
      message: '复用已保存的检索结果，只重评失败指标。会调用模型，可能产生费用；旧记录保留。',
      confirmText: '开始重评',
    }))
  )
    return
  busy.value = true
  try {
    const result = await adminRequest<{ run_id: string }>(
      `eval/runs/${current.value.id}/retry`,
      {},
      'POST',
    )
    await show(result.run_id)
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

function metricName(key: string | number) {
  const parts = String(key).split('/')
  return metricNames[parts[parts.length - 1] || ''] || key
}

function scoringState(row: Row) {
  if (!row.score) return '未评分'
  return row.score.metrics.some(
    (metric) => metric.status === 'error' || metric.status === 'timeout',
  )
    ? '评分失败'
    : '评分完成'
}

const errorCategories: Record<string, string> = {
  incomplete_output: '模型输出被截断',
  invalid_output: '结构化输出无效',
  rate_limit: '模型限流',
  authentication: '模型鉴权失败',
  provider_http: '上游 HTTP 错误',
  timeout: '评分超时',
  unknown: '未分类',
}
function errorMessage(message?: string) {
  if (message === 'judge failed or incompatible API')
    return '评估模型调用失败；请检查模型配置和服务日志。此历史任务没有有效分数。'
  if (message?.includes('IncompleteOutput'))
    return '模型输出被截断，未得到完整评分；不是检索 0 分，可仅重评失败项。'
  return message || ''
}
function errorDetail(details?: Record<string, string | number>) {
  if (!details) return ''
  const parts: string[] = []
  const type = String(details.exception_type || '')
  if (details.stage) parts.push(`阶段 ${details.stage}`)
  if (details.category && details.category !== 'unknown') {
    parts.push(errorCategories[String(details.category)] || String(details.category))
  } else if (type.includes('IncompleteOutput')) {
    parts.push('模型输出被截断')
  }
  if (type) parts.push(type)
  if (details.http_status) parts.push(`HTTP ${details.http_status}`)
  return parts.join(' · ')
}
const terminals = new Set([
  'succeeded',
  'partial',
  'failed',
  'cancelled',
  'timed_out',
  'interrupted',
])
let timer: ReturnType<typeof setTimeout> | undefined
let sequence = 0

function stop() {
  clearTimeout(timer)
  timer = undefined
}

async function load() {
  try {
    datasets.value = await adminRequest<Dataset[]>('eval/datasets')
    runs.value = await adminRequest<Run[]>('eval/runs')
    if (!datasets.value.some((dataset) => dataset.name === selected.value)) {
      selected.value = datasets.value[0]?.name || ''
    }
  } catch (e) {
    error.value = friendlyError((e as Error).message)
  }
}

async function unlock() {
  unlocked.value = true
  await load()
}

function lock() {
  unlocked.value = false
  stop()
  ++sequence
  current.value = undefined
  rows.value = []
  runs.value = []
  datasets.value = []
}

async function show(id: string, target = 1, open = true) {
  stop()
  if (current.value?.id !== id) {
    report.value = undefined
    baselineMessage.value = ''
  }
  const version = ++sequence

  try {
    const run = await adminRequest<Run>(`eval/runs/${encodeURIComponent(id)}`)
    const data = await adminRequest<{ items: Row[]; total: number }>(
      `eval/runs/${encodeURIComponent(id)}/items?page=${target}`,
    )
    if (version !== sequence || !unlocked.value) return

    current.value = run
    if (open) mode.value = 'result'
    rows.value = data.items
    total.value = data.total
    page.value = target
    if (props.active && !terminals.has(run.status))
      timer = setTimeout(() => show(id, target, false), 1500)
    else void load()
  } catch (e) {
    if (version === sequence) error.value = friendlyError((e as Error).message)
  }
}

async function create() {
  if (!chosen.value) {
    setMode('review')
    return
  }
  if (
    !(await askConfirm({
      title: '运行评估',
      message: `将执行 ${chosen.value.search_requests} 次检索，并调用评估模型评分。费用另计。`,
      confirmText: '开始评估',
    }))
  ) {
    return
  }

  busy.value = true
  error.value = ''
  try {
    const experimentName =
      name.value.trim() || `评估 ${new Date().toLocaleString('zh-CN', { hour12: false })}`
    const data = await adminRequest<{ run_id: string }>(
      'eval/runs',
      { dataset: selected.value, experiment_name: experimentName, hypothesis: hypothesis.value },
      'POST',
    )
    await load()
    await show(data.run_id)
  } catch (e) {
    error.value = friendlyError((e as Error).message)
  } finally {
    busy.value = false
  }
}

async function diagnose() {
  busy.value = true
  error.value = ''
  try {
    const result = await adminRequest<{ run_id: string }>(
      'eval/diagnose',
      {
        tenant: props.workspace.tenant,
        kb_no: props.workspace.kb_no,
        question: question.value,
      },
      'POST',
    )
    await load()
    await show(result.run_id)
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function cancel() {
  if (!current.value) return
  try {
    await adminRequest(`eval/runs/${current.value.id}/cancel`, {}, 'POST')
    await show(current.value.id, page.value)
  } catch (e) {
    error.value = friendlyError((e as Error).message)
  }
}

async function deleteRunRecord(run: Run) {
  if (busy.value) return
  if (
    !(await askConfirm({
      title: '删除评估记录',
      message: `删除「${formatRunName(run.experiment_name)}」？该记录的原始结果和评分会被移除，题库不受影响。`,
      confirmText: '删除',
      danger: true,
    }))
  )
    return
  busy.value = true
  try {
    await adminRequest(`eval/runs/${run.id}`, undefined, 'DELETE')
    if (current.value?.id === run.id) {
      current.value = undefined
      rows.value = []
      total.value = 0
      report.value = undefined
      baselineMessage.value = ''
      mode.value = 'history'
    }
    notice.value = '记录已删除'
    await load()
    setTimeout(() => {
      if (notice.value === '记录已删除') notice.value = ''
    }, 3000)
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function deleteRun() {
  if (current.value) await deleteRunRecord(current.value)
}

async function setBaseline() {
  if (!canSetBaseline.value) {
    error.value = diagnosticResult.value
      ? '单题诊断不能设为基线；请先运行一份已复核题库。'
      : '只有完整成功、无失败项的回归任务才能设为基线。请先修复失败项。'
    return
  }
  if (!current.value) return
  if (
    !(await askConfirm({
      title: '设为基线',
      message: '将这次完整任务设为该题库的基线。之后运行同一题库时可以比较。',
      confirmText: '设为基线',
    }))
  )
    return
  try {
    await adminRequest(`eval/runs/${current.value.id}/baseline`, {}, 'POST')
    baselineMessage.value = '已设为基线。下一次运行同一题库后即可比较。'
    error.value = ''
  } catch (cause) {
    const text = (cause as Error).message
    error.value = text.includes('评测输入无效')
      ? '这个任务还不是「已复核题库 + 完整成功」的任务，不能设为基线。'
      : friendlyError(text)
  }
}

async function compare() {
  if (!current.value) return
  try {
    report.value = await adminRequest(`eval/runs/${current.value.id}/compare`, policy.value, 'POST')
  } catch (cause) {
    const text = (cause as Error).message
    error.value = text.includes('评测输入无效')
      ? '还没有可用的基线。先在完整成功的任务上点「设为基线」，再比较。'
      : friendlyError(text)
  }
}

function published(dataset: string) {
  selected.value = dataset
  mode.value = 'home'
  void load()
}
watch(
  () => props.active,
  (active) => {
    if (!active) {
      stop()
      ++sequence
    } else if (unlocked.value) {
      void load()
      if (current.value) void show(current.value.id, page.value, false)
    }
  },
)
onBeforeUnmount(() => {
  stop()
  ++sequence
})
</script>

<template>
  <section class="eval-page">
    <header v-if="mode !== 'result'" class="eval-hero">
      <div>
        <span class="eval-eyebrow">EVALUATION</span>
        <h1>{{ pageTitle }}</h1>
        <p>{{ pageSubtitle }}</p>
      </div>
      <div class="eval-workspace">
        <span>当前工作区</span>
        <strong>{{ workspace.tenant }} / {{ workspace.kb_no }}</strong>
        <small>如需切换，使用右上角「切换工作区」</small>
      </div>
    </header>

    <AdminGate quiet @unlock="unlock" @lock="lock">
      <div v-if="error" class="eval-banner" role="alert">
        <span>{{ error }}</span>
        <button aria-label="关闭" @click="dismissError"><X :size="15" /></button>
      </div>

      <nav class="eval-segments" aria-label="评估视图">
        <button :class="{ active: mode === 'home' }" @click="setMode('home')">评估</button>
        <button :class="{ active: mode === 'review' }" @click="setMode('review')">题库</button>
        <button :class="{ active: mode === 'history' }" @click="setMode('history')">记录</button>
        <button class="eval-link" @click="setMode('diagnostic')">单题诊断</button>
      </nav>

      <template v-if="mode === 'home'">
        <section v-if="!datasets.length" class="eval-card eval-empty">
          <span class="eval-empty-icon"><BookOpen :size="26" /></span>
          <h2>先准备一组题</h2>
          <p>从当前知识库抽取原文，生成问题与参考答案。核对至少 3 道后即可开始评估。</p>
          <button class="eval-primary" @click="setMode('review')">创建题库</button>
        </section>
        <template v-else>
          <section class="eval-card eval-launch">
            <div class="eval-launch-head">
              <div>
                <span class="eval-muted">当前题库</span>
                <h2>{{ currentDatasetTitle }}</h2>
                <p>{{ chosen?.cases }} 道题 · {{ chosen?.profile_summary || '1 组配置' }}</p>
              </div>
              <select v-if="datasets.length > 1" v-model="selected" aria-label="选择题库">
                <option v-for="d in datasets" :key="d.name" :value="d.name">{{ datasetLabel(d) }}</option>
              </select>
            </div>
            <div v-if="latestRun" class="eval-last">
              <span :class="['eval-dot', latestRun.status]"></span>
              <div>
                <strong>{{ statusNames[latestRun.status] || latestRun.status }}</strong>
                <p>
                  {{ latestRun.done }}/{{ latestRun.expected }} 题 ·
                  {{ relativeTime(latestRun.created_at) }}
                  <template
                    v-if="latestRun.scoring_failures && latestRun.status !== 'cancelled'"
                  >
                    · {{ latestRun.scoring_failures }} 项评分失败</template
                  >
                </p>
              </div>
              <button class="eval-secondary" @click="show(latestRun.id)">查看结果</button>
            </div>
            <div class="eval-launch-actions">
              <button class="eval-primary" :disabled="busy" @click="create">
                <Play :size="16" /> {{ busy ? '提交中…' : '运行评估' }}
              </button>
              <button class="eval-secondary" @click="setMode('review')">管理题库</button>
            </div>
            <p class="eval-muted">
              会执行 {{ chosen?.search_requests }} 次检索，并按 3 项指标调用模型。费用另计。
            </p>
          </section>

          <section v-if="recentRuns.length" class="eval-card">
            <div class="eval-card-head">
              <h2>最近结果</h2>
              <button class="eval-link" @click="setMode('history')">
                全部记录 <ChevronRight :size="14" />
              </button>
            </div>
            <ul class="eval-run-list">
              <li v-for="run in recentRuns" :key="run.id">
                <button @click="show(run.id)">
                  <span :class="['eval-dot', run.status]"></span>
                  <span class="eval-run-name"
                    ><strong>{{ formatRunName(run.experiment_name) }}</strong
                    ><small
                      >{{ relativeTime(run.created_at) }} · {{ run.done }}/{{
                        run.expected
                      }}
                      题</small
                    ></span
                  >
                  <span :class="['eval-status', run.status]">{{
                    statusNames[run.status] || run.status
                  }}</span>
                  <ChevronRight :size="16" />
                </button>
              </li>
            </ul>
          </section>
        </template>
      </template>

      <EvaluationReview
        v-if="mode === 'review'"
        :workspace="workspace"
        @published="published"
      />

      <template v-if="mode === 'history'">
        <div class="eval-history-bar">
          <div class="eval-chips" role="group" aria-label="筛选记录">
          <button :class="{ active: historyFilter === 'all' }" @click="historyFilter = 'all'">
            全部
          </button>
          <button :class="{ active: historyFilter === 'active' }" @click="historyFilter = 'active'">
            进行中
          </button>
          <button :class="{ active: historyFilter === 'failed' }" @click="historyFilter = 'failed'">
            有失败
          </button>
          <button
            :class="{ active: historyFilter === 'succeeded' }"
            @click="historyFilter = 'succeeded'"
          >
            已完成
          </button>
          <button
            :class="{ active: historyFilter === 'library' }"
            @click="historyFilter = 'library'"
          >
            题库评估
          </button>
          </div>
          <button class="eval-secondary" @click="load">
            <RefreshCw :size="15" />刷新
          </button>
        </div>
        <p v-if="notice" class="eval-notice" role="status">{{ notice }}</p>
        <p v-if="!filteredRuns.length" class="eval-card eval-empty">这里还没有记录。</p>
        <ul v-else class="eval-card eval-history">
          <li v-for="run in filteredRuns" :key="run.id" class="eval-history-row">
            <button class="eval-history-main" @click="show(run.id)">
              <span :class="['eval-dot', run.status]"></span>
              <span class="eval-run-name"
                ><strong>{{ formatRunName(run.experiment_name) }}</strong
                ><small
                  >{{ relativeTime(run.created_at) }} · {{ run.done }}/{{ run.expected }} 题<template
                    v-if="run.scoring_failures && run.status !== 'cancelled'"
                  >
                    · {{ run.scoring_failures }} 项失败</template
                  ></small
                ></span
              >
              <span :class="['eval-status', run.status]">{{
                statusNames[run.status] || run.status
              }}</span>
              <ChevronRight :size="16" />
            </button>
            <button
              v-if="terminals.has(run.status)"
              class="eval-history-delete"
              type="button"
              :disabled="busy"
              :aria-label="`删除记录 ${formatRunName(run.experiment_name)}`"
              title="删除记录"
              @click.stop="deleteRunRecord(run)"
            >
              <Trash2 :size="15" />
            </button>
          </li>
        </ul>
      </template>

      <form
        v-if="mode === 'diagnostic'"
        class="eval-card eval-diagnostic"
        @submit.prevent="diagnose"
      >
        <h2>直接查一个问题</h2>
        <p class="eval-muted">
          当前知识库：{{ workspace.tenant }} / {{ workspace.kb_no }} · 使用工作台默认配置（返回 10 条、精排开启）
        </p>
        <p>
          只评估相关性；不验证答案是否找全，也不能作为发布验收。会调用评估模型，可能产生费用。
        </p>
        <label
          >你的问题<input
            v-model="question"
            required
            maxlength="1000"
            placeholder="例如：什么是供需平衡？"
        /></label>
        <button class="eval-primary" :disabled="busy || !question.trim()">检索并诊断</button>
      </form>

      <template v-if="mode === 'result' && current">
        <template v-if="diagnosticResult">
          <section class="eval-card eval-diag-head">
            <div>
              <span class="eval-eyebrow">单题诊断</span>
              <h2>{{ diagnosticQuestion || '单题诊断结果' }}</h2>
              <p class="eval-muted">
                当前知识库：{{ workspace.tenant }} / {{ workspace.kb_no }} ·
                {{ current.profile_summary || '使用工作台默认配置' }}
              </p>
            </div>
            <div class="eval-inline-actions">
              <button class="eval-primary" @click="reDiagnose">再查一个</button>
              <button class="eval-secondary" @click="setMode('home')">返回评估</button>
            </div>
          </section>

          <section class="eval-diag-stats">
            <span :class="['eval-dot', current.status]"></span>
            <strong>{{ statusNames[current.status] || current.status }}</strong>
            <span>{{ diagnosticEvidence.length }} 条证据</span>
            <span v-if="diagnosticRelevance !== null">相关性 {{ diagnosticRelevance.toFixed(0) }}（模型判断）</span>
            <span v-if="diagnosticStats?.p95_ms != null">检索 P95 {{ diagnosticStats.p95_ms.toFixed(0) }} ms</span>
            <span v-if="current.rerank_degraded">精排降级</span>
          </section>

          <section class="eval-card eval-diag-evidence">
            <div class="eval-card-head">
              <h2>返回的原文证据</h2>
              <span class="eval-muted">按融合排序</span>
            </div>
            <p v-if="!terminals.has(current.status)" class="eval-muted">
              正在检索当前知识库…
            </p>
            <p v-else-if="!diagnosticEvidence.length" class="eval-muted">
              没有检索到内容。请确认当前工作区有资料，或换一个问题。
            </p>
            <details
              v-for="(hit, index) in diagnosticEvidence"
              :key="hit.chunk_id"
              :open="index === 0"
              class="eval-diag-hit"
            >
              <summary>
                <span class="eval-diag-rank">{{ index + 1 }}</span>
                <span class="eval-diag-hit-title">
                  <strong>{{ hit.title || hit.chunk_id }}</strong>
                  <small>{{ hit.content.slice(0, 90) }}…</small>
                </span>
                <span class="eval-muted">{{ hit.chunk_id.slice(0, 8) }}</span>
              </summary>
              <p>{{ hit.content }}</p>
            </details>
          </section>

          <section class="eval-card eval-diag-note">
            <strong>这只是相关性检查</strong>
            <p class="eval-muted">
              没有人工参考答案，所以不计算召回和排序质量，也不能作为发布验收。要判断资料是否找全，请运行已审核的题库。
            </p>
            <div class="eval-inline-actions">
              <button class="eval-primary" @click="reDiagnose">再查一个问题</button>
              <button class="eval-secondary" @click="setMode('history')">查看记录</button>
              <button
                v-if="terminals.has(current.status)"
                class="eval-danger-link"
                :disabled="busy"
                @click="deleteRun"
              >
                删除这条诊断
              </button>
            </div>
          </section>
        </template>
        <template v-else>
        <section :class="['eval-verdict', 'tone-' + conclusion.tone]">
          <span class="eval-eyebrow">本次结果</span>
          <h2>{{ conclusion.title }}</h2>
          <p>{{ conclusion.reason }}</p>
          <div class="eval-verdict-actions">
            <button class="eval-primary" :disabled="busy" @click="primaryAction">
              {{ primaryLabel }}
            </button>
            <button class="eval-secondary" @click="setMode('history')">返回记录</button>
            <button
              v-if="terminals.has(current.status)"
              class="eval-danger-link"
              :disabled="busy"
              @click="deleteRun"
            >
              删除记录
            </button>
          </div>
          <p v-if="baselineMessage" class="eval-muted">{{ baselineMessage }}</p>
        </section>

        <section class="eval-metrics">
          <article
            v-for="metric in metricTiles"
            :key="metric.name"
            :class="['eval-metric', { 'has-errors': metric.errors > 0 }]"
          >
            <span>{{ metric.title }}</span>
            <strong :class="{ partial: metric.errors > 0 }">{{
              metric.value === null ? '—' : (metric.value * 100).toFixed(0)
            }}</strong>
            <div class="eval-meter" :class="{ partial: metric.errors > 0 }">
              <span
                :style="{
                  width: `${
                    metric.ok + metric.errors ? (metric.ok / (metric.ok + metric.errors)) * 100 : 0
                  }%`,
                }"
              ></span>
            </div>
            <small
              >{{ metric.ok }} 项有效<template v-if="metric.errors">
                · {{ metric.errors }} 项失败</template
              ></small
            >
            <p>{{ metric.description }}</p>
          </article>
        </section>

        <section v-if="!cancelled && failureCount" class="eval-card eval-failures">
          <details>
            <summary>失败项 <span class="eval-count">{{ failureCount }}</span></summary>
            <div
              v-for="entry in failedMetricsOnPage"
              :key="entry.row.case_id + entry.metric.name"
              class="eval-failure"
            >
              <button class="eval-link" @click="revealFailure(entry.row.case_id)">
                {{ entry.row.case_id }} · {{ metricName(entry.metric.name) }} →
              </button>
              <span>{{ errorMessage(entry.metric.error) }}</span>
              <small v-if="entry.metric.error_details" class="eval-error-detail">
                {{ errorDetail(entry.metric.error_details) }}
              </small>
            </div>
            <button class="eval-secondary" :disabled="busy" @click="retryFailed">
              仅重评失败项
            </button>
          </details>
        </section>

        <section v-if="!cancelled" class="eval-card">
          <details>
            <summary>逐题分析</summary>
            <label class="eval-check"
              ><input v-model="failureOnly" type="checkbox" />只看失败题</label
            >
            <div v-if="!displayedRows.length" class="eval-muted">没有符合条件的题目。</div>
            <details
              v-for="row in displayedRows"
              :id="'eval-case-' + row.case_id"
              :key="row.case_id + row.profile"
              class="eval-row"
            >
              <summary>
                <span class="eval-row-id">{{ row.case_id }}</span>
                <span class="eval-row-question">{{ row.question }}</span>
                <span :class="['eval-status', row.retrieval_status]">{{
                  statusNames[row.retrieval_status] || row.retrieval_status
                }}</span>
                <span
                  :class="[
                    'eval-status',
                    scoringState(row) === '评分失败' ? 'partial' : 'succeeded',
                  ]"
                  >{{ scoringState(row) }}</span
                >
              </summary>
              <p><strong>问题：</strong>{{ row.question }}</p>
              <p><strong>参考答案：</strong>{{ row.reference }}</p>
              <div v-if="row.score" class="eval-row-metrics">
                <span
                  v-for="m in row.score.metrics"
                  :key="m.name"
                  :class="['eval-pill', m.status]"
                  >{{ metricName(m.name) }}
                  {{
                    m.status === 'ok' && m.value !== null
                      ? (m.value * 100).toFixed(0)
                      : statusNames[m.status] || m.status
                  }}<template v-if="m.computed_by === 'id_based'"> · ID</template></span
                >
              </div>
              <div v-if="row.retrieval?.items.length" class="eval-evidence">
                <details v-for="hit in row.retrieval.items" :key="hit.chunk_id">
                  <summary>{{ hit.title || hit.chunk_id }}</summary>
                  <p>{{ hit.content }}</p>
                </details>
              </div>
            </details>
            <Pagination
              :page="page"
              :pages="pages"
              :total="total"
              @change="show(current.id, $event)"
            />
          </details>
        </section>

        <section v-if="canSetBaseline || report || baselineMessage" class="eval-card eval-baseline">
          <div class="eval-card-head">
            <h2>回归基线</h2>
          </div>
          <p v-if="canSetBaseline && !baselineMessage" class="eval-muted">
            把这次完整结果设为基线。下次运行同一题库后，可以比较是否退步。
          </p>
          <div class="eval-inline-actions">
            <button
              v-if="canSetBaseline && !baselineMessage"
              class="eval-primary"
              :disabled="busy"
              @click="setBaseline"
            >
              设为基线
            </button>
            <button
              v-if="baselineMessage"
              class="eval-secondary"
              :disabled="busy"
              @click="compare"
            >
              与基线比较
            </button>
          </div>
          <p v-if="baselineMessage" class="eval-muted">{{ baselineMessage }}</p>
          <template v-if="report">
            <strong>{{
              { pass: '满足本次门槛', fail: '不满足门槛', insufficient: '证据不足' }[
                report.verdict
              ] || report.verdict
            }}</strong>
            <ul>
              <li v-for="reason in report.reasons" :key="reason">{{ reason }}</li>
            </ul>
            <p v-for="(delta, key) in report.deltas" :key="key">
              {{ metricName(key) }} 变化 {{ delta.toFixed(3) }}
            </p>
            <p>覆盖退步题：{{ report.regressed_cases.join('、') || '无' }}</p>
          </template>
        </section>
        </template>
      </template>
    </AdminGate>
    <ConfirmDialog
      :open="!!confirmDialog"
      :title="confirmDialog?.title || ''"
      :message="confirmDialog?.message || ''"
      :confirm-text="confirmDialog?.confirmText"
      :cancel-text="confirmDialog?.cancelText"
      :danger="confirmDialog?.danger"
      @confirm="resolveConfirm(true)"
      @cancel="resolveConfirm(false)"
    />
  </section>
</template>

<style scoped>
.eval-page {
  max-width: 1080px;
  margin: 0 auto;
  padding: 28px 24px 72px;
  color: #1d1d1f;
}
.eval-hero {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 24px;
  padding: 8px 0 28px;
}
.eval-hero h1 {
  font-size: 46px;
  letter-spacing: -1.6px;
  line-height: 1.04;
  margin: 8px 0 10px;
  font-weight: 600;
}
.eval-hero p {
  color: #6e6e73;
  font-size: 17px;
  line-height: 1.55;
  max-width: 560px;
  margin: 0;
}
.eval-eyebrow {
  font-size: 11px;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: #86868b;
  font-weight: 600;
}
.eval-workspace {
  text-align: right;
  color: #86868b;
  font-size: 12px;
}
.eval-workspace strong {
  display: block;
  color: #1d1d1f;
  font-size: 16px;
  font-weight: 500;
  margin: 4px 0;
}
.eval-workspace small {
  display: block;
  max-width: 220px;
  line-height: 1.4;
}
.eval-banner {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  background: #fff4e5;
  border: 1px solid #ffd9a8;
  color: #8a5300;
  border-radius: 16px;
  padding: 12px 16px;
  margin-bottom: 18px;
  font-size: 14px;
}
.eval-banner button {
  border: 0;
  background: none;
  color: inherit;
  cursor: pointer;
  display: inline-flex;
}
.eval-segments {
  display: inline-flex;
  background: #e8e8ed;
  border-radius: 999px;
  padding: 4px;
  gap: 2px;
  margin-bottom: 22px;
}
.eval-segments button {
  border: 0;
  background: transparent;
  border-radius: 999px;
  padding: 8px 18px;
  font-size: 14px;
  color: #3a3a3c;
  cursor: pointer;
}
.eval-segments button.active {
  background: #fff;
  color: #1d1d1f;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
}
.eval-link {
  border: 0;
  background: none;
  color: #0071e3;
  font-size: 14px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0;
}
.eval-card {
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 24px;
  padding: 24px;
  margin-bottom: 16px;
}
.eval-card h2 {
  font-size: 28px;
  letter-spacing: -0.6px;
  margin: 0 0 8px;
  font-weight: 600;
}
.eval-card p {
  color: #6e6e73;
  line-height: 1.6;
  margin: 6px 0;
}
.eval-empty {
  text-align: center;
  padding: 56px 24px;
}
.eval-empty-icon {
  display: inline-flex;
  width: 56px;
  height: 56px;
  border-radius: 18px;
  background: #f5f5f7;
  align-items: center;
  justify-content: center;
  color: #0071e3;
  margin-bottom: 14px;
}
.eval-launch-head {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-start;
}
.eval-launch-head h2 {
  font-size: 32px;
  margin-bottom: 4px;
}
.eval-launch-head select {
  border: 1px solid #d2d2d7;
  border-radius: 12px;
  padding: 9px 34px 9px 12px;
  background-color: #fff;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%2386868b' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 12px center;
  appearance: none;
  font: inherit;
  font-size: 13px;
  max-width: 280px;
}
.eval-muted {
  color: #86868b;
  font-size: 13px;
}
.eval-last {
  display: flex;
  align-items: center;
  gap: 12px;
  background: #f5f5f7;
  border-radius: 16px;
  padding: 12px 14px;
  margin: 18px 0;
}
.eval-last div {
  flex: 1;
  min-width: 0;
}
.eval-last p {
  margin: 2px 0 0;
  font-size: 13px;
}
.eval-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #c7c7cc;
  flex: none;
}
.eval-dot.succeeded {
  background: #34c759;
}
.eval-dot.partial,
.eval-dot.failed,
.eval-dot.timed_out {
  background: #ff9f0a;
}
.eval-dot.running,
.eval-dot.queued {
  background: #0071e3;
}
.eval-dot.cancelled,
.eval-dot.interrupted {
  background: #8e8e93;
}
.eval-launch-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 16px;
}
.eval-primary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  background: #0071e3;
  color: #fff;
  border: 0;
  border-radius: 999px;
  padding: 12px 22px;
  font-size: 15px;
  font-weight: 500;
  cursor: pointer;
}
.eval-primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.eval-secondary {
  background: #fff;
  border: 1px solid #d2d2d7;
  color: #1d1d1f;
  border-radius: 999px;
  padding: 10px 18px;
  font-size: 14px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.eval-secondary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.eval-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.eval-card-head h2 {
  margin: 0;
}
.eval-run-list,
.eval-history {
  list-style: none;
  padding: 0;
  margin: 0;
}
.eval-run-list li + li,
.eval-history li + li {
  border-top: 1px solid #f0f0f2;
}
.eval-run-list button,
.eval-history button {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 4px;
  background: none;
  border: 0;
  text-align: left;
  cursor: pointer;
  font: inherit;
}
.eval-run-name {
  flex: 1;
  min-width: 0;
}
.eval-run-name strong {
  display: block;
  font-size: 15px;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.eval-run-name small {
  color: #86868b;
  font-size: 12px;
}
.eval-status {
  font-size: 12px;
  color: #6e6e73;
  background: #f5f5f7;
  border-radius: 999px;
  padding: 3px 9px;
  white-space: nowrap;
}
.eval-status.succeeded {
  color: #1d7a35;
  background: #e8f8ed;
}
.eval-status.partial,
.eval-status.failed,
.eval-status.timed_out,
.eval-status.error,
.eval-status.timeout {
  color: #8a5300;
  background: #fff4e5;
}
.eval-status.running,
.eval-status.queued {
  color: #0057b8;
  background: #e8f1ff;
}
.eval-status.cancelled,
.eval-status.interrupted {
  color: #6e6e73;
  background: #f5f5f7;
}
.eval-chips {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.eval-chips button {
  border: 1px solid #d2d2d7;
  background: #fff;
  border-radius: 999px;
  padding: 6px 14px;
  font-size: 13px;
  cursor: pointer;
}
.eval-chips button.active {
  background: #1d1d1f;
  border-color: #1d1d1f;
  color: #fff;
}
.eval-verdict {
  border-radius: 24px;
  padding: 28px;
  margin-bottom: 16px;
  background: #f5f5f7;
}
.eval-verdict.tone-fail {
  background: #fff4e5;
}
.eval-verdict.tone-pass {
  background: #e8f8ed;
}
.eval-verdict h2 {
  font-size: 32px;
  margin: 8px 0 10px;
  letter-spacing: -0.8px;
}
.eval-verdict p {
  color: #3a3a3c;
  max-width: 680px;
  line-height: 1.6;
}
.eval-verdict-actions {
  display: flex;
  gap: 10px;
  margin-top: 18px;
  flex-wrap: wrap;
}
.eval-metrics {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px;
  margin-bottom: 16px;
}
.eval-metric {
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 20px;
  padding: 20px;
}
.eval-metric span {
  font-size: 13px;
  color: #6e6e73;
}
.eval-metric strong {
  display: block;
  font-size: 40px;
  font-weight: 600;
  letter-spacing: -1.5px;
  margin: 6px 0 2px;
}
.eval-metric small {
  color: #86868b;
}
.eval-metric p {
  font-size: 12px;
  color: #86868b;
  margin: 10px 0 0;
}
.eval-metric.has-errors {
  border-color: #ffd9a8;
  background: #fffaf2;
}
.eval-failures summary,
.eval-card details summary {
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
}
.eval-count {
  display: inline-flex;
  min-width: 20px;
  height: 20px;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: #fff4e5;
  color: #8a5300;
  font-size: 12px;
  margin-left: 4px;
}
.eval-failure {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 0;
  border-top: 1px solid #f0f0f2;
  font-size: 13px;
  color: #6e6e73;
}
.eval-check {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-size: 13px;
  color: #6e6e73;
  margin: 8px 0 12px;
}
.eval-row {
  border-top: 1px solid #f0f0f2;
  padding: 8px 0;
}
.eval-row summary {
  display: flex;
  gap: 10px;
  align-items: center;
  list-style: none;
}
.eval-row summary::-webkit-details-marker {
  display: none;
}
.eval-row-id {
  font-size: 12px;
  color: #86868b;
  min-width: 38px;
}
.eval-row-question {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 14px;
}
.eval-row p {
  font-size: 14px;
  color: #3a3a3c;
}
.eval-row-metrics {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin: 8px 0;
}
.eval-pill {
  font-size: 12px;
  border-radius: 999px;
  padding: 3px 9px;
  background: #f5f5f7;
  color: #6e6e73;
}
.eval-pill.ok {
  background: #e8f8ed;
  color: #1d7a35;
}
.eval-pill.error,
.eval-pill.timeout {
  background: #fff4e5;
  color: #8a5300;
}
.eval-evidence details {
  margin-top: 8px;
  font-size: 13px;
  color: #6e6e73;
}
.eval-inline-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin: 10px 0;
}
.eval-stats {
  margin-top: 12px;
  font-size: 13px;
  color: #6e6e73;
}
.eval-stats h4 {
  margin: 0 0 4px;
  color: #1d1d1f;
}
.eval-stats p {
  margin: 2px 0;
}
.eval-back {
  border: 0;
  background: none;
  color: #0071e3;
  padding: 0;
  margin-bottom: 14px;
  cursor: pointer;
  font-size: 14px;
}
.eval-diagnostic label {
  display: block;
  margin: 16px 0;
  font-size: 13px;
  color: #6e6e73;
}
.eval-diagnostic input {
  width: 100%;
  margin-top: 6px;
  border: 1px solid #d2d2d7;
  border-radius: 12px;
  padding: 12px 14px;
  font: inherit;
  font-size: 16px;
  box-sizing: border-box;
}
@media (max-width: 760px) {
  .eval-hero {
    display: block;
  }
  .eval-workspace {
    text-align: left;
    margin-top: 16px;
  }
  .eval-workspace small {
    max-width: none;
  }
  .eval-hero h1 {
    font-size: 34px;
  }
  .eval-metrics {
    grid-template-columns: 1fr;
  }
  .eval-verdict h2 {
    font-size: 26px;
  }
  .eval-launch-head {
    display: block;
  }
  .eval-launch-head select {
    margin-top: 10px;
    width: 100%;
  }
}

.eval-failure {
  flex-wrap: wrap;
}
.eval-error-detail {
  width: 100%;
  color: #86868b;
  font-size: 12px;
  margin-top: 4px;
}

.eval-meter {
  height: 4px;
  border-radius: 999px;
  background: #f0f0f2;
  margin: 10px 0 6px;
  overflow: hidden;
}
.eval-meter span {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: #34c759;
  transition: width 0.3s ease;
}
.eval-meter.partial span {
  background: #ff9f0a;
}
.eval-metric strong.partial {
  color: #8a5300;
  opacity: 0.72;
}
.eval-card,
.eval-verdict,
.eval-metric {
  transition:
    border-color 0.2s ease,
    box-shadow 0.2s ease;
}
.eval-metric:hover,
.eval-card:hover {
  border-color: #d9d9de;
}
.eval-run-list button:hover,
.eval-history button:hover {
  background: #fafafa;
}
.eval-primary,
.eval-secondary,
.eval-segments button {
  transition:
    background 0.2s ease,
    color 0.2s ease,
    box-shadow 0.2s ease,
    transform 0.1s ease;
}
.eval-primary:hover {
  background: #0077ed;
}
.eval-secondary:hover {
  background: #f5f5f7;
}
.eval-primary:active,
.eval-secondary:active {
  transform: scale(0.98);
}

.eval-page {
  background:
    radial-gradient(900px 420px at 8% -8%, rgba(0, 113, 227, 0.07), transparent 60%),
    radial-gradient(700px 360px at 95% 0%, rgba(175, 82, 222, 0.05), transparent 60%);
  background-repeat: no-repeat;
}
@keyframes eval-rise {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}
.eval-card,
.eval-verdict,
.eval-metrics {
  animation: eval-rise 0.35s ease both;
}
@media (prefers-reduced-motion: reduce) {
  .eval-card,
  .eval-verdict,
  .eval-metrics,
  .eval-primary,
  .eval-secondary,
  .eval-segments button,
  .eval-meter span {
    animation: none;
    transition: none;
  }
}

.eval-history-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.eval-history-bar .eval-chips {
  margin-bottom: 0;
}

select::picker-icon {
  display: none;
}

.eval-danger-link {
  border: 0;
  background: none;
  color: #b42318;
  font-size: 13px;
  cursor: pointer;
  padding: 8px 6px;
}
.eval-danger-link:hover {
  text-decoration: underline;
}
.eval-danger-link:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.eval-history-row {
  display: flex;
  align-items: center;
  gap: 4px;
}
.eval-history-main {
  flex: 1;
  min-width: 0;
  width: auto !important;
}
.eval-history-delete {
  width: 34px !important;
  height: 34px;
  padding: 0 !important;
  flex: none;
  justify-content: center;
  border: 0;
  border-radius: 50%;
  background: none;
  color: #a1a1a6;
  cursor: pointer;
  transition:
    color 0.2s ease,
    background 0.2s ease;
}
.eval-history-delete:hover {
  color: #b42318;
  background: #fff1f0;
}
.eval-history-delete:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}
.eval-notice {
  color: #1d7a35;
  font-size: 13px;
  margin: 0 0 10px;
}

.eval-diag-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 20px;
}
.eval-diag-head h2 {
  font-size: 30px;
  letter-spacing: -0.7px;
  margin: 6px 0 6px;
}
.eval-diag-stats {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  color: #6e6e73;
  font-size: 13px;
  margin: 0 0 14px;
  padding: 0 6px;
}
.eval-diag-hit {
  border-top: 1px solid #f0f0f2;
  padding: 18px 0;
}
.eval-diag-hit:first-of-type {
  border-top: 0;
  padding-top: 4px;
}
.eval-diag-hit > summary {
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
  list-style: none;
}
.eval-diag-hit > summary::-webkit-details-marker {
  display: none;
}
.eval-diag-hit-title {
  flex: 1;
  min-width: 0;
  display: grid;
  gap: 2px;
}
.eval-diag-hit-title strong {
  font-size: 15px;
  font-weight: 500;
}
.eval-diag-hit-title small {
  color: #86868b;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.eval-diag-rank {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #f0f5ff;
  color: #0071e3;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  flex: none;
}
.eval-diag-hit p {
  margin: 12px 0 0 34px;
  white-space: pre-wrap;
  color: #3a3a3c;
  line-height: 1.65;
  font-size: 14px;
}
.eval-diag-note {
  background: #f5f5f7;
  border: 0;
}
.eval-diag-note > strong {
  display: block;
  margin-bottom: 6px;
}
@media (max-width: 760px) {
  .eval-diag-head {
    display: block;
  }
  .eval-diag-head .eval-inline-actions {
    margin-top: 14px;
  }
}
</style>
