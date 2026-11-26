<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { adminRequest } from '../admin'
import type { Workspace } from '../types'
import ConfirmDialog from './ConfirmDialog.vue'

type Candidate = {
  source_type?: string
  source_file?: string
  start_line?: number
  end_line?: number
  section?: string
  region?: string
  difficulty?: string
  id: string
  question: string
  reference: string
  doc_id: string
  doc_name: string
  source: string
  status: string
  critical: boolean
}
type Draft = {
  id: string
  version: number
  tenant: string
  kb_no: string
  items: Candidate[]
  auto_generate?: boolean
  generation_state?: string
  generation_error?: string
}
const props = defineProps<{ workspace: Workspace }>()
const emit = defineEmits<{ published: [name: string] }>()
const drafts = ref<Draft[]>([])
const current = ref<Draft>()
const count = ref(10)
const selectionCount = ref(10)
const randomSelection = ref(false)
const page = ref(1)
const pageSize = ref(1)
const settingsOpen = ref(false)
const reviewing = ref(false)
const indexOpen = ref(false)
const editing = ref(false)
const lastDecision = ref<{ id: string; status: string }>()
const filter = ref('all')
const search = ref('')
const saved = ref('')
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
const generating = ref(false)
const dirty = computed(() => !!current.value && JSON.stringify(current.value.items) !== saved.value)
const approved = computed(
  () => current.value?.items.filter((item) => item.status === 'approved').length || 0,
)
const pending = computed(
  () => current.value?.items.filter((item) => item.status === 'draft').length || 0,
)
const missing = computed(
  () =>
    current.value?.items.filter((item) => item.status === 'draft' && !item.question).length || 0,
)
const filtered = computed(() =>
  (current.value?.items || []).filter(
    (item) =>
      (filter.value === 'all' || item.status === filter.value) &&
      `${item.question} ${item.doc_name} ${item.id}`
        .toLowerCase()
        .includes(search.value.toLowerCase().trim()),
  ),
)
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize.value)))
function draftTitle(draft: Draft) {
  if (draft.items.some((item) => item.source_type === 'original_markdown')) return '原文题库'
  if (draft.id.startsWith('sample')) return '样例题库'
  return '题库'
}
function draftLabel(draft: Draft) {
  const ready = draft.items.filter((item) => item.question && item.reference).length
  return `${draft.kb_no} · ${draftTitle(draft)} · ${ready}/${draft.items.length} 题`
}
const visible = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
const needsGeneration = computed(() => {
  const item = visible.value[0]
  return !!item && (!item.question || !item.reference)
})
watch([filter, search, pageSize], () => {
  page.value = 1
})
watch(pages, (value) => {
  page.value = Math.min(page.value, value)
})
function remember() {
  saved.value = JSON.stringify(current.value?.items || [])
}
async function selectDraft(id: string) {
  if (busy.value) return
  if (
    dirty.value &&
    !(await askConfirm({
      title: '放弃未保存的修改？',
      message: '切换草稿会丢弃当前未保存的审核修改。',
      confirmText: '放弃修改',
      danger: true,
    }))
  )
    return
  const draft = drafts.value.find((draft) => draft.id === id)
  current.value = draft ? (JSON.parse(JSON.stringify(draft)) as Draft) : undefined
  page.value = 1
  filter.value = 'all'
  search.value = ''
  message.value = ''
  error.value = ''
  lastDecision.value = undefined
  editing.value = false
  remember()
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || busy.value || generating.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
const busy = ref(false)
const message = ref('')
const error = ref('')
const profile = ref({
  name: 'production',
  limit: 10,
  retrieve_count: 20,
  dense_weight: 0.5,
  rerank_switch: true,
  embed_model: '',
  rerank_model: '',
  min_score: 0,
  fusion_min_score: 0,
})
const generated = computed(
  () => current.value?.items.filter((item) => item.question && item.reference).length || 0,
)
const isGenerating = computed(() => current.value?.generation_state === 'pending')
const progressPercent = computed(() => {
  const total = current.value?.items.length || 0
  return total ? Math.round((generated.value / total) * 100) : 0
})
const profileSummary = computed(
  () =>
    `${profile.value.name} · 返回 ${profile.value.limit} 条 · 召回 ${profile.value.retrieve_count} 条 · ${
      profile.value.rerank_switch ? '精排开' : '精排关'
    }`,
)
function useProductionProfile() {
  profile.value = {
    name: 'production',
    limit: 10,
    retrieve_count: 20,
    dense_weight: 0.5,
    rerank_switch: true,
    embed_model: '',
    rerank_model: '',
    min_score: 0,
    fusion_min_score: 0,
  }
  message.value = '已使用生产配置：返回 10 条、召回 20 条、精排开启。'
}

async function decide(status: 'approved' | 'rejected') {
  const candidate = visible.value[0]
  if (!candidate || busy.value) return
  if (status === 'approved' && (!candidate.question.trim() || !candidate.reference.trim())) {
    error.value = '这道题还没有生成完成，请先等待模型出题。'
    return
  }
  const previous = candidate.status
  candidate.status = status
  busy.value = true
  error.value = ''
  try {
    await save()
    lastDecision.value = { id: candidate.id, status: previous }
    editing.value = false
    const next = filtered.value.findIndex(
      (item) => item.status === 'draft' && item.question && item.reference,
    )
    if (next >= 0) page.value = next + 1
    else page.value = Math.min(page.value + 1, pages.value)
    message.value = '审核已保存'
  } catch (cause) {
    candidate.status = previous
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function navigateTo(target: number) {
  if (busy.value || target < 1 || target > pages.value) return
  if (dirty.value) {
    busy.value = true
    try {
      await save()
    } catch (cause) {
      error.value = friendlyError((cause as Error).message)
      return
    } finally {
      busy.value = false
    }
  }
  page.value = target
  editing.value = false
}

async function undoDecision() {
  if (!lastDecision.value || !current.value || busy.value) return
  const candidate = current.value.items.find((item) => item.id === lastDecision.value?.id)
  if (!candidate) return
  const previous = candidate.status
  candidate.status = lastDecision.value.status
  busy.value = true
  try {
    await save()
    page.value = Math.max(1, filtered.value.findIndex((item) => item.id === candidate.id) + 1)
    lastDecision.value = undefined
    message.value = '已撤销上一次审核'
  } catch (cause) {
    candidate.status = previous
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

function keyboard(event: KeyboardEvent) {
  const target = event.target as HTMLElement
  if (!reviewing.value || target.closest('input, textarea, select, button, [contenteditable]'))
    return
  if (event.key === 'ArrowRight') {
    event.preventDefault()
    void navigateTo(page.value + 1)
  }
  if (event.key === 'ArrowLeft') {
    event.preventDefault()
    void navigateTo(page.value - 1)
  }
}
onMounted(() => window.addEventListener('keydown', keyboard))
onBeforeUnmount(() => window.removeEventListener('keydown', keyboard))

async function load() {
  try {
    drafts.value = await adminRequest<Draft[]>('eval/drafts')
    if (current.value && !drafts.value.some((draft) => draft.id === current.value?.id)) {
      current.value = undefined
    }
    if (!current.value) {
      const matching = drafts.value
        .filter(
          (draft) =>
            draft.tenant === props.workspace.tenant && draft.kb_no === props.workspace.kb_no,
        )
        .sort((left, right) => {
          const original = (draft: Draft) =>
            draft.items.some((item) => item.source_type === 'original_markdown') ? 1 : 0
          if (original(left) !== original(right)) return original(right) - original(left)
          const ready = (draft: Draft) =>
            draft.items.filter((item) => item.question && item.reference).length
          return ready(right) - ready(left)
        })
      if (matching[0]) await selectDraft(matching[0].id)
    }
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  }
}

async function refreshDraft() {
  if (
    dirty.value &&
    !(await askConfirm({
      title: '刷新并放弃修改？',
      message: '刷新会丢弃当前未保存的审核修改。',
      confirmText: '刷新',
      danger: true,
    }))
  )
    return
  const id = current.value?.id
  await load()
  if (id) {
    const updated = drafts.value.find((draft) => draft.id === id)
    if (updated) {
      current.value = JSON.parse(JSON.stringify(updated)) as Draft
      remember()
    }
  }
}

async function prepare(usePolicy = true) {
  if (
    dirty.value &&
    !(await askConfirm({
      title: '放弃修改并新建题库？',
      message: '当前草稿的未保存修改会被丢弃。',
      confirmText: '放弃修改',
      danger: true,
    }))
  )
    return
  if (usePolicy) {
    if (
      !(await askConfirm({
        title: '新建题库',
        message:
          '将从当前知识库按每篇 md 最多 50 段、总数最多 100 段抽取原文。只抽取原文，不调用模型。',
        confirmText: '抽取原文',
      }))
    )
      return
  } else if (!Number.isFinite(count.value) || count.value < 3 || count.value > 1000) {
    error.value = '题目数量需要填写 3～1000 之间的整数。'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const body = usePolicy
      ? {
          tenant: props.workspace.tenant,
          kb_no: props.workspace.kb_no,
          per_doc: 50,
          total_cap: 100,
        }
      : {
          tenant: props.workspace.tenant,
          kb_no: props.workspace.kb_no,
          count: count.value,
        }
    current.value = await adminRequest<Draft>('eval/drafts', body, 'POST')
    remember()
    page.value = 1
    filter.value = 'all'
    search.value = ''
    message.value = usePolicy
      ? `已抽取 ${current.value.items.length} 段原文（每篇最多 50 段，总数最多 100 段）。下一步：生成 ${current.value.items.length} 道题。`
      : `已准备 ${current.value.items.length} 段资料。下一步：点击「生成测试题」。`
    await load()
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}
function friendlyError(message?: string) {
  const text = message || ''
  if (text.includes('评测输入无效')) return '请检查抽取数量：总数至少 3 段，单篇上限 1000 段。'
  if (text.includes('评测功能未开启')) return '评测功能未开启，请先在配置中打开评估并填写模型。'
  if (text.includes('已有评测任务')) return '已有评估任务在运行，请等它结束后再试。'
  return text
}

async function removeDraft() {
  if (!current.value || busy.value) return
  if (
    !(await askConfirm({
      title: '删除草稿',
      message: `删除「${current.value.id}」？只删除草稿，已发布题库和评估记录不受影响。`,
      confirmText: '删除',
      danger: true,
    }))
  )
    return
  busy.value = true
  error.value = ''
  try {
    await adminRequest(`eval/drafts/${current.value.id}`, undefined, 'DELETE')
    current.value = undefined
    busy.value = false
    await load()
    message.value = '草稿已删除'
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function save() {
  if (!current.value) return
  const baseline = JSON.parse(saved.value || '[]') as Candidate[]
  const edits = current.value.items.filter((item) => {
    const previous = baseline.find((value) => value.id === item.id)
    return (
      !previous ||
      ['question', 'reference', 'status', 'critical'].some(
        (key) => item[key as keyof Candidate] !== previous[key as keyof Candidate],
      )
    )
  })
  const latest = await adminRequest<Draft[]>('eval/drafts')
  const server = latest.find((draft) => draft.id === current.value?.id)
  if (!server) throw new Error('题集已不存在，请返回题集首页重新选择。')
  for (const edit of edits) {
    const target = server.items.find((item) => item.id === edit.id)
    const previous = baseline.find((item) => item.id === edit.id)
    if (!target || !previous) throw new Error('题目已变化，请刷新后再试。')
    for (const field of ['question', 'reference', 'status', 'critical'] as const) {
      if (edit[field] === previous[field]) continue
      if (target[field] !== previous[field] && target[field] !== edit[field]) {
        throw new Error(`题目 ${edit.id} 已被其他操作修改，当前编辑仍保留，请先核对后刷新。`)
      }
    }
    if (edit.question !== previous.question) target.question = edit.question
    if (edit.reference !== previous.reference) target.reference = edit.reference
    if (edit.status !== previous.status) target.status = edit.status
    if (edit.critical !== previous.critical) target.critical = edit.critical
  }
  current.value = await adminRequest<Draft>(`eval/drafts/${current.value.id}`, {
    version: server.version,
    items: server.items.map((item) => ({
      id: item.id,
      question: item.question,
      reference: item.reference,
      status: item.status,
      critical: item.critical,
    })),
  })
  remember()
}

async function generateCurrent() {
  const item = visible.value[0]
  if (!current.value || !item || busy.value || generating.value || !needsGeneration.value) return
  if (
    !(await askConfirm({
      title: '生成本题',
      message: '只对当前这道题调用一次评估模型，生成问题和参考答案。',
      confirmText: '生成',
    }))
  )
    return
  generating.value = true
  error.value = ''
  try {
    await save()
    const latest = await adminRequest<Draft[]>('eval/drafts')
    const server = latest.find((draft) => draft.id === current.value?.id)
    if (!server) throw new Error('题库已不存在')
    const generated = await adminRequest<Draft>(
      `eval/drafts/${current.value.id}/generate`,
      { case_id: item.id, version: server.version },
      'POST',
    )
    current.value = generated
    remember()
    message.value = '当前题已生成，请对照原文核对。'
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    generating.value = false
  }
}

let progressTimer: number | undefined
function stopProgressPolling() {
  if (progressTimer !== undefined) {
    window.clearInterval(progressTimer)
    progressTimer = undefined
  }
}
function startProgressPolling() {
  stopProgressPolling()
  progressTimer = window.setInterval(() => {
    void refreshProgress()
  }, 4000)
}
async function refreshProgress() {
  const id = current.value?.id
  if (!id || !isGenerating.value) {
    stopProgressPolling()
    return
  }
  if (dirty.value || busy.value || generating.value) return
  try {
    const latest = await adminRequest<Draft[]>('eval/drafts')
    drafts.value = latest
    const server = latest.find((draft) => draft.id === id)
    if (!server) {
      stopProgressPolling()
      return
    }
    if (current.value?.id === id && !dirty.value) {
      current.value = JSON.parse(JSON.stringify(server)) as Draft
      remember()
    }
    if (server.generation_state !== 'pending') stopProgressPolling()
  } catch {
    // 进度刷新失败不打断审核，下个周期再试。
  }
}
watch(isGenerating, (value) => {
  if (value) startProgressPolling()
  else stopProgressPolling()
})
onBeforeUnmount(stopProgressPolling)

async function startGeneration() {
  if (!current.value || busy.value || generating.value || !missing.value) return
  if (
    !(await askConfirm({
      title: '后台生成题目',
      message: `将在服务端为 ${missing.value} 道题逐题调用模型，页面关掉也会继续，进度会自动刷新。可能产生费用。`,
      confirmText: '开始生成',
    }))
  )
    return
  busy.value = true
  error.value = ''
  try {
    await save()
    const latest = await adminRequest<Draft[]>('eval/drafts')
    const server = latest.find((draft) => draft.id === current.value?.id)
    if (!server) throw new Error('题库已不存在，请返回重新选择。')
    current.value = await adminRequest<Draft>(
      `eval/drafts/${current.value.id}/generate-all`,
      { version: server.version },
      'POST',
    )
    remember()
    message.value = `已在后台生成 ${missing.value} 道题，可以关闭页面。`
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function stopGeneration() {
  if (!current.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    current.value = await adminRequest<Draft>(
      `eval/drafts/${current.value.id}/generate-stop`,
      undefined,
      'POST',
    )
    remember()
    message.value = '已暂停后台生成，可以随时继续。'
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function saveEdits() {
  busy.value = true
  try {
    await save()
    message.value = '复核已保存，尚未发布。'
    await load()
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

async function publish() {
  if (!current.value || approved.value < 3) return
  if (
    !(await askConfirm({
      title: '发布题库',
      message: `将 ${randomSelection.value ? selectionCount.value : approved.value} 道已通过题保存为固定题库，其他题不会纳入。发布后不会覆盖旧版本。`,
      confirmText: '发布题库',
    }))
  )
    return
  busy.value = true
  error.value = ''
  try {
    await save()
    const result = await adminRequest<{ dataset: string }>(
      `eval/drafts/${current.value.id}/publish`,
      {
        version: current.value.version,
        profile: profile.value,
        count: randomSelection.value ? selectionCount.value : 0,
      },
      'POST',
    )
    message.value = `已发布 ${result.dataset}`
    emit('published', result.dataset)
  } catch (cause) {
    error.value = friendlyError((cause as Error).message)
  } finally {
    busy.value = false
  }
}

function continueReview() {
  const next = filtered.value.findIndex((item) => item.status === 'draft' && item.question)
  page.value = next >= 0 ? next + 1 : 1
  reviewing.value = true
}

onMounted(load)
</script>

<template>
  <section class="panel review-panel">
    <template v-if="!reviewing">
      <div v-if="!current" class="review-empty-card">
        <h3>还没有题库</h3>
        <p>从当前知识库抽取原文：每篇 md 最多 50 段，总数最多 100 段。你可以逐题修改或弃用。</p>
        <button class="button" :disabled="busy" @click="prepare()">
          {{ busy ? '正在准备…' : '创建第一个题库' }}
        </button>
      </div>

      <template v-else>
        <article class="review-library-card">
          <div class="review-library-main">
            <span class="review-library-icon">题</span>
            <div>
              <h3>{{ current.kb_no }} · {{ draftTitle(current) }}</h3>
              <p v-if="isGenerating">
                后台生成中 {{ generated }} / {{ current.items.length }} 题 · {{ progressPercent }}%
                · 可以关闭页面
              </p>
              <p v-else-if="generated === 0">
                {{ current.items.length }} 段原文已就绪 · 0 道题已生成
              </p>
              <p v-else>
                {{ current.items.length }} 道题 · {{ generated }} 道已生成 ·
                {{ approved }} 道已通过
              </p>
              <p class="review-library-config">{{ profileSummary }}</p>
            </div>
          </div>
          <div class="review-library-actions">
            <button
              v-if="missing"
              class="button"
              :disabled="busy || isGenerating"
              @click="startGeneration"
            >
              {{ isGenerating ? '后台生成中…' : `后台生成 ${missing} 道题` }}
            </button>
            <button v-else class="button" :disabled="busy" @click="continueReview">
              继续核对
            </button>
            <button v-if="isGenerating" class="review-tool" :disabled="busy" @click="stopGeneration">
              暂停生成
            </button>
            <button
              v-if="generated > 0 && missing"
              class="review-tool"
              :disabled="busy"
              @click="continueReview"
            >
              继续核对
            </button>
            <button
              v-if="generated === 0"
              class="review-tool"
              :disabled="busy || generating"
              @click="continueReview"
            >
              先看原文
            </button>
            <button v-if="approved >= 3" class="review-tool" :disabled="busy" @click="publish">
              发布题库
            </button>
          </div>
        </article>
        <div
          v-if="isGenerating"
          class="review-progress"
          role="progressbar"
          :aria-valuenow="generated"
          aria-valuemin="0"
          :aria-valuemax="current.items.length"
        >
          <span :style="{ width: `${progressPercent}%` }" />
        </div>

        <div class="review-library-foot">
          <label v-if="drafts.length > 1"
            >切换题库<select
              :value="current.id"
              @change="selectDraft(($event.target as HTMLSelectElement).value)"
            >
              <option v-for="draft in drafts" :key="draft.id" :value="draft.id">
                {{ draftLabel(draft) }}
              </option>
            </select></label
          >
          <div>
            <button type="button" class="review-tool" :disabled="busy" @click="prepare()">
              新建题库
            </button>
            <button
              type="button"
              class="review-tool danger"
              :disabled="busy"
              @click="removeDraft"
            >
              删除草稿
            </button>
          </div>
        </div>
        <p class="review-library-note">新建题库按每篇 md 最多 50 段、总数最多 100 段抽取；生成题目会逐题调用模型。</p>
      </template>

      <p v-if="current?.auto_generate" class="collection-hint">
        后台出题：
        <template v-if="isGenerating">
          {{ generated }}/{{ current.items.length }} 题，进度自动刷新，可关闭页面
        </template>
        <template v-else>
          {{
            {
              completed: '已完成，等待人工审核',
              failed: '已停止',
              stopped: '已暂停',
            }[current.generation_state || ''] || current.generation_state
          }}
        </template>
        <template v-if="current.generation_error"> · {{ current.generation_error }}</template>
        <button
          v-if="
            missing &&
            (current.generation_state === 'failed' || current.generation_state === 'stopped')
          "
          class="review-link"
          :disabled="busy"
          @click="startGeneration"
        >
          继续生成
        </button>
      </p>
      <p v-if="error" role="alert" class="review-error">{{ error }}</p>
      <p v-if="message" role="status" class="review-message">{{ message }}</p>
    </template>

    <template v-else>
      <button class="review-back" @click="reviewing = false">← 返回题集</button>
      <div class="review-title">
        <div>
          <span class="review-caption">知识库评估</span>
          <h2>看原文，核对答案。</h2>
          <p class="review-instruction">
            答案有原文依据，就通过；需要调整，点「修改问答」。切换题目时会先保存修改。
          </p>
        </div>
        <button
          class="review-tool"
          :aria-expanded="settingsOpen"
          @click="settingsOpen = !settingsOpen"
        >
          题集设置
        </button>
      </div>
      <p class="muted">
        从 {{ workspace.tenant }} /
        {{ workspace.kb_no }} 的资料生成问题和参考答案，核对后即可用来评估检索效果。
      </p>
      <ol class="review-steps" aria-label="准备测试题的步骤">
        <li :class="{ active: !current }"><b>1</b>选取资料</li>
        <li :class="{ active: current && missing > 0 }"><b>2</b>生成测试题</li>
        <li :class="{ active: current && missing === 0 }"><b>3</b>核对并保存题集</li>
      </ol>
      <details v-if="!current || settingsOpen" class="review-setup" :open="!current">
        <summary>{{ current ? '重新选取资料 / 创建新草稿' : '第 1 步：选取出题资料' }}</summary>
        <form class="review-toolbar" @submit.prevent="prepare(false)">
          <label
            >计划准备多少道题<input v-model.number="count" type="number" min="3" max="1000"
          /></label>
          <button class="button" :disabled="busy">
            {{ busy ? '处理中…' : '从当前知识库选取资料' }}
          </button>
        </form>
        <p class="muted">
          建议先准备 20 题。系统会从不同文档选取原文；生成问题和参考答案需要调用模型。
        </p>
      </details>
      <label v-if="settingsOpen || !current"
        >继续审核已有草稿<select
          :value="current?.id || ''"
          :disabled="busy"
          @change="
            selectDraft(($event.target as HTMLSelectElement).value)
            ;($event.target as HTMLSelectElement).value = current?.id || ''
          "
        >
          <option value="">选择草稿</option>
          <option v-for="draft in drafts" :key="draft.id" :value="draft.id">
            {{ draftLabel(draft) }} · v{{ draft.version }}
          </option>
        </select></label
      >
      <template v-if="current">
        <div class="review-stats">
          <span
            >全部题目 <strong>{{ current.items.length }}</strong></span
          >
          <span
            >待审核 <strong>{{ pending }}</strong></span
          >
          <span
            >已通过 <strong>{{ approved }}</strong></span
          >
          <span
            >已弃用 <strong>{{ current.items.length - pending - approved }}</strong></span
          >
        </div>
        <div v-if="settingsOpen" class="review-toolbar">
          <p class="muted">
            模型已生成 {{ current.items.length - missing }} /
            {{ current.items.length }} 题。后台出题时进度会自动刷新；审核修改请先保存。
          </p>
          <button class="review-refresh" :disabled="busy" @click="refreshDraft">
            刷新题目与进度
          </button>
        </div>
        <p class="muted">
          当前草稿：{{ current.tenant }} / {{ current.kb_no }}。只有人工通过的题目会进入评估题集。
        </p>
        <div v-if="missing || dirty || isGenerating" class="review-toolbar">
          <button v-if="isGenerating" class="review-tool" :disabled="busy" @click="stopGeneration">
            暂停后台生成
          </button>
          <button
            v-else-if="missing"
            class="review-tool"
            :disabled="busy || generating"
            @click="startGeneration"
          >
            后台生成 {{ missing }} 题
          </button>
          <span class="muted">{{
            isGenerating
              ? `后台生成中 ${generated}/${current.items.length} · 可关闭页面`
              : `约 ${missing} 次模型调用`
          }}</span>
          <span :class="dirty ? 'unsaved' : 'muted'" role="status">{{
            dirty ? '有未保存修改 · 翻页不会丢失，请及时保存' : '修改已保存'
          }}</span>
        </div>
        <div v-if="settingsOpen" class="review-toolbar review-filters">
          <label
            >审核状态<select v-model="filter" :disabled="busy">
              <option value="all">全部题目</option>
              <option value="draft">待审核</option>
              <option value="approved">已通过</option>
              <option value="rejected">已弃用</option>
            </select></label
          >
          <label class="review-search"
            >搜索题目或文档<input
              v-model="search"
              type="search"
              placeholder="输入问题、题号或文档名"
              :disabled="busy"
          /></label>
        </div>
        <div class="review-navigation">
          <button class="review-tool" :aria-expanded="indexOpen" @click="indexOpen = !indexOpen">
            第 {{ page }} / {{ pages }} 题 · 目录
          </button>
          <div>
            <span class="review-shortcut">← → 切换 · 修改自动保存后再切题</span>
            <button class="review-tool" :disabled="busy || page <= 1" @click="navigateTo(page - 1)">
              上一题</button
            ><button
              class="review-tool"
              :disabled="busy || page >= pages"
              @click="navigateTo(page + 1)"
            >
              下一题
            </button>
          </div>
        </div>
        <nav v-if="indexOpen" class="review-index" aria-label="题目目录">
          <button
            v-for="(item, index) in filtered"
            :key="item.id"
            :class="['review-index-item', item.status, { selected: index + 1 === page }]"
            :aria-label="`跳转第 ${index + 1} 题`"
            :disabled="busy"
            @click="navigateTo(index + 1)"
          >
            {{ index + 1
            }}<span>{{
              item.status === 'approved' ? '✓' : item.status === 'rejected' ? '−' : '·'
            }}</span>
          </button>
        </nav>
        <p v-if="!visible.length" class="review-empty">
          没有符合条件的题目，试试切换审核状态或清空搜索。
        </p>
        <div class="review-flow">
        <article v-for="item in visible" :key="item.id" class="review-item">
          <div class="review-item-heading">
            <h3>{{ item.id }} · {{ item.doc_name }}</h3>
            <button class="review-tool" :disabled="busy" @click="editing = !editing">
              {{ editing ? '完成编辑' : '修改问答' }}
            </button>
            <span :class="['review-status', item.status]">{{
              { draft: '待审核', approved: '已通过', rejected: '已弃用' }[item.status] ||
              item.status
            }}</span>
          </div>
          <p v-if="item.source_type === 'original_markdown'" class="source-provenance">
            独立原始 MD · {{ item.region }} · 难度目标：{{ item.difficulty }} · {{ item.section }} ·
            第 {{ item.start_line }}–{{ item.end_line }} 行（非 ES 切片）
          </p>
          <div class="review-columns">
            <details class="review-evidence" open>
              <summary>查看原文（必须核对）</summary>
              <p class="review-source">{{ item.source }}</p>
              <small>文档 {{ item.doc_id }}</small>
            </details>
            <div class="review-answer">
              <template v-if="!editing"
                ><div class="review-reading">
                  <span>测试问题</span>
                  <p>{{ item.question || '题目尚未生成，请先补齐问答。' }}</p>
                </div>
                <div class="review-reading">
                  <span>参考答案</span>
                  <p>{{ item.reference || '参考答案尚未生成。' }}</p>
                </div></template
              >
              <template v-else>
                <label
                  >问题<input
                    v-model="item.question"
                    :disabled="busy"
                    maxlength="1000"
                    placeholder="等待模型生成，无需从零输入"
                /></label>
                <label
                  >参考答案<textarea
                    v-model="item.reference"
                    :disabled="busy"
                    rows="4"
                    maxlength="10000"
                    placeholder="等待模型生成；生成后对照左侧原文核对即可"
                  />
                </label>
              </template>
            </div>
          </div>
          <details class="review-manual">
            <summary>更多审核选项</summary>
            <div class="review-toolbar">
              <label
                >人工结论<select v-model="item.status" :disabled="busy">
                  <option value="draft">待复核</option>
                  <option value="approved">已核对原文，通过</option>
                  <option value="rejected">弃用</option>
                </select></label
              ><label
                ><input v-model="item.critical" type="checkbox" :disabled="busy" />关键题</label
              >
            </div>
          </details>
        </article>
        <div class="review-actionbar">
          <div class="review-save-feedback">
            <span
              >{{ busy ? '正在保存本题…' : dirty ? '本题有修改' : '已保存到服务器' }} ·
              {{ approved }} 题已通过</span
            ><span v-if="error" role="alert" class="review-error">{{ error }}</span
            ><span v-else-if="message" role="status">{{ message }}</span>
            <span v-if="needsGeneration" class="review-hint">当前题还没有生成完整问答</span>
          </div>
          <div>
            <button v-if="lastDecision" class="review-tool" :disabled="busy" @click="undoDecision">
              撤销上次审核
            </button>
            <button v-if="dirty" class="review-tool" :disabled="busy" @click="saveEdits">
              仅保存修改
            </button>
            <button
              class="review-tool"
              :disabled="busy || !visible.length"
              @click="decide('rejected')"
            >
              弃用此题</button
            ><button
              v-if="needsGeneration"
              class="button"
              :disabled="busy || generating"
              @click="generateCurrent"
            >
              {{ generating ? '生成中…' : '生成本题' }}
            </button>
            <button v-else class="button" :disabled="busy" @click="decide('approved')">
              {{ busy ? '保存中…' : '通过并继续' }}
            </button>
          </div>
        </div>
        </div>
        <div class="review-publish">
          <label><input v-model="randomSelection" type="checkbox" />从已通过题中快速抽取</label>
          <label v-if="randomSelection"
            >本次题数<input
              v-model.number="selectionCount"
              type="number"
              min="3"
              :max="approved"
            /><small
              >输入 8、10 等数量；按文档和难度尽量均衡。抽中结果固定保存，重跑不再抽题。</small
            ></label
          >
          <details class="review-profile">
            <summary>评估配置 · {{ profileSummary }}</summary>
            <div class="review-toolbar">
              <label
                >返回条数<input
                  v-model.number="profile.limit"
                  type="number"
                  min="1"
                  max="100" /></label
              ><label
                >语义权重<input
                  v-model.number="profile.dense_weight"
                  type="number"
                  min="0"
                  max="1"
                  step="0.1" /></label
              ><label
                >精排候选数<input
                  v-model.number="profile.retrieve_count"
                  type="number"
                  min="1"
                  max="200" /></label
              ><label><input v-model="profile.rerank_switch" type="checkbox" />精排</label
              ><button type="button" class="review-tool" @click="useProductionProfile">
                使用生产配置
              </button>
            </div>
            <p class="muted">
              发布时把这组参数写进题库；之后每次评估都复用它。改动需重新发布题库。
            </p>
          </details>
          <div>
            <strong>{{
              approved >= 3
                ? `可保存 ${approved} 道题，用于评估`
                : `还需通过 ${3 - approved} 道题，才能创建题库`
            }}</strong>
            <p class="muted">
              只纳入已通过题目。保存后前往运行评估，不会部署或修改知识库。少量题仅用于跑通流程，不代表整体质量。
            </p>
          </div>
          <button
            class="button"
            :disabled="
              busy ||
              approved < 3 ||
              (randomSelection && (selectionCount < 3 || selectionCount > approved))
            "
            @click="publish"
          >
            发布题库 →
          </button>
        </div>
      </template>
    </template>
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
.review-panel {
  padding: 24px;
  display: grid;
  gap: 18px;
}
.source-provenance {
  grid-column: 1 / -1;
  margin: 0;
  font-size: 11px;
  color: #8490a2;
}
.collection-journey {
  display: flex;
  align-items: center;
  gap: 20px;
  margin-top: 28px;
  font-size: 13px;
  color: #6d7b90;
}
.collection-journey span {
  display: inline-flex;
  gap: 9px;
  align-items: center;
}
.collection-journey b {
  display: grid;
  place-items: center;
  width: 25px;
  height: 25px;
  border-radius: 50%;
  background: #eaf0fa;
  color: #537aaf;
  font-size: 12px;
}
.collection-journey i {
  font-style: normal;
  color: #b5bfcd;
}
.collection-hint,
.review-instruction {
  font-size: 13px;
  line-height: 1.8;
  color: #8490a2;
  max-width: 680px;
}
.review-panel .button {
  min-height: 44px;
  border-radius: 12px;
  box-shadow: 0 3px 10px #1769df12;
}
.review-panel .review-item {
  background: #fff;
  box-shadow: 0 8px 30px #273f6005;
}
@media (max-width: 640px) {
  .collection-journey {
    gap: 9px;
    flex-wrap: wrap;
    font-size: 12px;
  }
}
.review-reading {
  min-height: 0;
  display: grid;
  grid-template-rows: 22px minmax(0, 1fr);
  gap: 8px;
}
.review-reading > span {
  color: #8993a3;
  font-size: 12px;
  font-weight: 500;
}
.review-reading p {
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  margin: 0;
  padding: 14px 16px;
  border-radius: 12px;
  background: #fafbfd;
  border: 1px solid #edf0f4;
  color: #26364c;
  font-size: 14px;
  line-height: 1.85;
}
.review-index {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(42px, 1fr));
  gap: 8px;
  padding: 16px;
  background: #f6f8fb;
  border-radius: 14px;
}
.review-index-item {
  display: flex;
  justify-content: center;
  gap: 4px;
  padding: 9px 4px;
  background: white;
  border: 1px solid #e6eaf0;
  border-radius: 8px;
  color: #68758a;
  cursor: pointer;
}
.review-index-item.selected {
  outline: 2px solid #628ed5;
}
.review-index-item.approved {
  background: #eaf5ee;
  color: #357a54;
}
.review-index-item.rejected {
  opacity: 0.55;
}
.review-shortcut {
  font-size: 11px;
  color: #97a0ae;
  margin-right: 12px;
}
@media (max-width: 760px) {
  .review-shortcut {
    display: none;
  }
}
.collection-heading {
  padding: 16px 0 20px;
}
.collection-heading h2 {
  font-size: 28px;
  letter-spacing: -0.8px;
  color: #24364c;
}
.collection-heading p {
  color: #8490a2;
  font-size: 14px;
  margin-top: 12px;
}
.collection-card {
  display: flex;
  gap: 22px;
  align-items: center;
  padding: 28px;
  border: 1px solid #e4e9f0;
  border-radius: 18px;
  background: #fbfcfe;
}
.collection-card > div {
  flex: 1;
}
.collection-card h3 {
  margin: 0 0 8px;
  font-size: 18px;
  color: #24364c;
}
.collection-card p {
  margin: 0;
  font-size: 13px;
  color: #8490a2;
}
.collection-icon {
  display: grid;
  place-items: center;
  width: 56px;
  height: 64px;
  border-radius: 14px;
  background: #e9f0fc;
  color: #4674b3;
  font-size: 23px;
}
.collection-options {
  margin-top: 20px;
  font-size: 13px;
  color: #8490a2;
}
.collection-options label,
.collection-options form {
  margin-top: 18px;
}
.review-back {
  border: 0;
  background: none;
  color: #56799e;
  text-align: left;
  padding: 0;
  cursor: pointer;
}
@media (max-width: 640px) {
  .collection-card {
    flex-wrap: wrap;
    padding: 20px;
  }
  .collection-card .button {
    width: 100%;
  }
}
.review-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.review-caption {
  display: block;
  color: #8b95a5;
  font-size: 12px;
  margin-bottom: 8px;
}
.review-tool {
  appearance: none;
  background: transparent;
  color: #536174;
  border: 0;
  border-radius: 8px;
  padding: 10px 12px;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
.review-tool:hover {
  background: #edf1f6;
}
.review-tool:disabled {
  opacity: 0.4;
  cursor: default;
}
.review-navigation,
.review-actionbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  color: #8490a2;
  font-size: 12px;
}
.review-actionbar {
  position: sticky;
  bottom: 16px;
  padding: 16px 20px;
  border: 1px solid #e1e6ed;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 8px 32px #24364c12;
  z-index: 2;
}
.review-actionbar > div {
  display: flex;
  gap: 12px;
}
.review-manual {
  color: #8490a2;
  font-size: 12px;
}
@media (max-width: 640px) {
  .review-actionbar {
    flex-wrap: wrap;
    bottom: 8px;
  }
  .review-actionbar > div {
    width: 100%;
    justify-content: flex-end;
  }
}
.review-refresh {
  padding: 9px 14px;
  border: 1px solid #dce2ea;
  border-radius: 8px;
  background: #fff;
  color: #536174;
  cursor: pointer;
}
h2 {
  font-size: 19px;
  margin: 0;
}
.review-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: end;
  gap: 16px;
}
label {
  display: grid;
  gap: 8px;
  font-size: 13px;
  color: #536174;
}
input:not([type='checkbox']),
textarea,
select {
  padding: 11px;
  border: 1px solid #dce2ea;
  border-radius: 8px;
  font: inherit;
  color: #24364c;
  background: #fafbfd;
}
.review-item {
  display: grid;
  gap: 14px;
  padding: 20px;
  border: 1px solid #e5eaf1;
  border-radius: 12px;
}
.review-source {
  white-space: pre-wrap;
  max-height: 360px;
  overflow: auto;
  line-height: 1.8;
}
.review-error {
  color: #b42318;
}
.review-steps {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  list-style: none;
  padding: 0;
  margin: 0;
  gap: 12px;
}
.review-steps li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px;
  background: #f5f7fa;
  border-radius: 10px;
  color: #65748a;
  font-size: 14px;
}
.review-steps b {
  border-radius: 50%;
  padding: 4px 9px;
  background: #e4eaf2;
}
.review-steps .active {
  background: #eaf3ff;
  color: #1264c7;
}
.review-setup {
  border: 1px solid #e1e7ef;
  border-radius: 10px;
  padding: 16px;
}
.review-setup summary {
  cursor: pointer;
  font-weight: 600;
  font-size: 14px;
}
.review-setup[open] .review-toolbar {
  margin-top: 18px;
}
.review-stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}
.review-stats span {
  padding: 16px;
  background: #f5f8fc;
  border-radius: 10px;
  font-size: 13px;
  color: #59697f;
}
.review-stats strong {
  display: block;
  font-size: 25px;
  color: #24364c;
  margin-top: 8px;
}
.review-filters {
  padding: 16px;
  background: #f7f9fc;
  border-radius: 10px;
}
.review-search {
  flex: 1;
  min-width: 180px;
}
.review-item-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.review-item h3 {
  margin: 0;
  font-size: 14px;
  overflow-wrap: anywhere;
}
.review-status {
  white-space: nowrap;
  background: #edf1f6;
  color: #59697f;
  padding: 5px 9px;
  border-radius: 6px;
  font-size: 12px;
}
.review-status.approved {
  background: #e7f6ed;
  color: #237346;
}
.review-status.rejected {
  background: #fff0ed;
  color: #ad4836;
}
.review-columns {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 20px;
  height: 360px;
}
.review-columns > * {
  min-width: 0;
}
.review-evidence {
  background: #f7f9fc;
  padding: 16px;
  border-radius: 8px;
  font-size: 13px;
  height: 360px;
  box-sizing: border-box;
  overflow: auto;
}
.review-evidence summary {
  cursor: pointer;
  font-weight: 600;
}
.review-source {
  overflow-wrap: anywhere;
  max-height: none;
  overflow: visible;
}
.review-answer {
  display: grid;
  gap: 16px;
  align-content: start;
  grid-template-rows: 82px minmax(0, 1fr);
  height: 360px;
  min-height: 0;
}
.review-answer label {
  min-height: 0;
  grid-template-rows: 20px minmax(0, 1fr);
}
.review-answer input,
.review-answer textarea {
  width: 100%;
  box-sizing: border-box;
}
.review-answer textarea {
  resize: none;
  min-height: 0;
  height: 100%;
  overflow: auto;
}
.review-publish {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  border-top: 1px solid #e1e7ef;
  padding-top: 20px;
}
.review-publish .button {
  flex-shrink: 0;
}
.review-publish p {
  font-size: 12px;
  line-height: 1.8;
}
.unsaved {
  color: #9a610b;
  font-size: 12px;
}
.review-empty {
  text-align: center;
  padding: 32px;
  color: #65748a;
}
.collection-empty {
  display: grid;
  gap: 16px;
  align-items: start;
}
.collection-empty h3 {
  margin: 0 0 6px;
  font-size: 20px;
}
.collection-empty p {
  margin: 0;
  color: #6e6e73;
  line-height: 1.6;
}
.review-profile {
  border-top: 1px solid #eef0f4;
  padding-top: 14px;
  margin-top: 12px;
}
.review-profile summary {
  cursor: pointer;
  font-size: 13px;
  color: #6e6e73;
}
@media (max-width: 760px) {
  .review-columns {
    grid-template-columns: 1fr;
    height: auto;
  }
  .review-evidence {
    height: 240px;
  }
  .review-answer {
    height: 340px;
  }
  .review-stats {
    grid-template-columns: repeat(2, 1fr);
  }
  .review-steps {
    grid-template-columns: 1fr;
  }
  .review-publish {
    flex-direction: column;
    align-items: stretch;
  }
}

.review-library-head {
  padding: 4px 0 18px;
}
.review-library-head h2 {
  margin: 4px 0 6px;
  font-size: 32px;
  letter-spacing: -0.8px;
}
.review-library-head p {
  color: #6e6e73;
  margin: 0;
}
.review-empty-card {
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 24px;
  padding: 40px;
  text-align: center;
}
.review-empty-card h3 {
  margin: 0 0 8px;
  font-size: 22px;
}
.review-empty-card p {
  color: #6e6e73;
  margin: 0 0 20px;
}
.review-library-card {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  align-items: center;
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 24px;
  padding: 24px;
}
.review-library-main {
  display: flex;
  gap: 16px;
  align-items: center;
  min-width: 0;
}
.review-library-icon {
  width: 48px;
  height: 48px;
  border-radius: 14px;
  background: #f0f5ff;
  color: #0071e3;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  flex: none;
}
.review-library-main h3 {
  margin: 0 0 4px;
  font-size: 20px;
}
.review-library-main p {
  margin: 0;
  color: #6e6e73;
  font-size: 13px;
}
.review-library-config {
  margin-top: 4px !important;
  color: #86868b !important;
}
.review-library-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  flex: none;
}
.review-library-foot {
  display: flex;
  justify-content: space-between;
  align-items: end;
  gap: 16px;
  margin-top: 14px;
  flex-wrap: wrap;
}
.review-library-foot label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  color: #86868b;
  flex: 1;
  max-width: 420px;
}
.review-library-foot select {
  border: 1px solid #d2d2d7;
  border-radius: 10px;
  padding: 8px 34px 8px 10px;
  font: inherit;
  font-size: 13px;
  background-color: #fff;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%2386868b' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 12px center;
  appearance: none;
  width: 100%;
  min-width: 260px;
}
.review-library-foot > div {
  display: flex;
  gap: 8px;
}
.review-library-note {
  margin: 10px 0 0;
  color: #86868b;
  font-size: 12px;
}
.review-message {
  color: #1d7a35;
  font-size: 13px;
}
.review-tool.danger {
  color: #b42318;
  border-color: #fecdca;
}
@media (max-width: 760px) {
  .review-library-card {
    display: block;
  }
  .review-library-actions {
    margin-top: 16px;
  }
}

.review-actionbar {
  position: sticky;
  bottom: 0;
  z-index: 5;
  background: rgba(255, 255, 255, 0.94);
  backdrop-filter: blur(12px);
  border-top: 1px solid #e8e8ed;
  padding: 14px 0 8px;
}
.review-flow {
  min-width: 0;
}
@media (max-width: 760px) {
  .review-actionbar {
    padding: 12px 0 6px;
  }
}

.review-library-actions .review-tool,
.review-library-foot .review-tool {
  border: 1px solid #d2d2d7;
  background: #fff;
  border-radius: 999px;
  padding: 10px 16px;
  color: #1d1d1f;
  font-size: 14px;
  transition:
    background 0.2s ease,
    border-color 0.2s ease,
    transform 0.1s ease;
}
.review-library-actions .review-tool:hover,
.review-library-foot .review-tool:hover {
  background: #f5f5f7;
}
.review-library-actions .review-tool:active,
.review-library-foot .review-tool:active {
  transform: scale(0.98);
}
.review-library-foot .review-tool.danger {
  color: #b42318;
  border-color: #fecdca;
}

select::picker-icon {
  display: none;
}

.review-actionbar .button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.review-hint {
  color: #8a5300;
  font-size: 12px;
  margin-left: 8px;
}
.review-progress {
  height: 4px;
  margin-top: 12px;
  border-radius: 999px;
  background: #eef1f6;
  overflow: hidden;
}
.review-progress span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, #0a84ff, #5ac8fa);
  transition: width 0.6s ease;
}
.review-link {
  appearance: none;
  border: 0;
  background: none;
  color: #0071e3;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
  padding: 0 4px;
}
.review-link:disabled {
  opacity: 0.4;
  cursor: default;
}
@media (prefers-reduced-motion: reduce) {
  .review-progress span {
    transition: none;
  }
}
</style>
