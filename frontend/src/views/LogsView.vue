<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RefreshCw, Search, Pause, Play, Activity } from '@lucide/vue'
import { adminRequest } from '../admin'
import AdminGate from '../components/AdminGate.vue'
import Notice from '../components/Notice.vue'
import Pagination from '../components/Pagination.vue'
type Entry = {
  id: number
  time: string
  level: string
  message: string
  fields?: Record<string, unknown>
}
type Logs = {
  items: Entry[]
  total: number
  page: number
  limit: number
  retained: number
  counts: Record<string, number>
  persistence_error: boolean
}
const props = defineProps<{ active: boolean }>()
const result = ref<Logs>(),
  level = ref(''),
  query = ref(''),
  since = ref(''),
  until = ref(''),
  page = ref(1),
  busy = ref(false),
  error = ref(''),
  auto = ref(false),
  unlocked = ref(false),
  updated = ref(''),
  status = ref<{ uptime_seconds: number; log_scope: string; log_disk_retention: string }>()
let timer: ReturnType<typeof setInterval> | undefined
let sequence = 0
const pages = computed(() => Math.max(1, Math.ceil((result.value?.total || 0) / 50)))
async function load(target = page.value) {
  if (!unlocked.value) return
  const current = ++sequence
  busy.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({
      level: level.value,
      q: query.value,
      page: String(target),
      limit: '50',
    })
    if (since.value) params.set('since', new Date(since.value).toISOString())
    if (until.value) params.set('until', new Date(until.value).toISOString())
    if (since.value && until.value && since.value > until.value)
      throw new Error('开始时间不能晚于结束时间')
    const data = await adminRequest<Logs>(`logs?${params}`)
    if (current !== sequence) return
    result.value = data
    page.value = data.page
    updated.value = new Date().toLocaleTimeString('zh-CN')
  } catch (e) {
    if (current === sequence) error.value = (e as Error).message
  } finally {
    if (current === sequence) busy.value = false
  }
}
async function unlock() {
  unlocked.value = true
  try {
    status.value = await adminRequest('status')
  } catch {}
  load(1)
}
function lock() {
  unlocked.value = false
  auto.value = false
  ++sequence
  result.value = undefined
  status.value = undefined
}
function polling() {
  clearInterval(timer)
  if (auto.value && props.active && unlocked.value)
    timer = setInterval(() => {
      if (!busy.value && document.visibilityState === 'visible') load(1)
    }, 5000)
}
watch([auto, () => props.active, unlocked], polling)
onBeforeUnmount(() => {
  clearInterval(timer)
  ++sequence
})
function fmt(value: string) {
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}
</script>
<template>
  <section class="logs-page">
    <div class="page-heading">
      <div>
        <h1>运行中的每一个细节。</h1>
        <p>查看服务事件、定位错误，了解请求如何运行。</p>
      </div>
    </div>
    <AdminGate @unlock="unlock" @lock="lock"
      ><div class="runtime-overview">
        <div>
          <Activity :size="20" /><span
            >当前进程运行时间<strong>{{
              status
                ? `${Math.floor(status.uptime_seconds / 3600)} 小时 ${Math.floor((status.uptime_seconds % 3600) / 60)} 分钟（连接时）`
                : '读取中'
            }}</strong></span
          >
        </div>
        <div>
          <span
            >保留日志<strong>{{ result?.retained ?? '—' }} 条</strong></span
          >
        </div>
        <div class="log-counts">
          <span
            >INFO <b>{{ result?.counts.info || 0 }}</b></span
          ><span
            >WARN <b>{{ result?.counts.warn || 0 }}</b></span
          ><span
            >ERROR <b>{{ result?.counts.error || 0 }}</b></span
          >
        </div>
      </div>
      <div class="log-console">
        <form class="log-filters" @submit.prevent="load(1)">
          <label class="filter-input"
            ><Search :size="17" /><input
              v-model="query"
              aria-label="搜索日志"
              maxlength="500"
              placeholder="搜索消息、错误、路径或 Trace ID" /></label
          ><select v-model="level" aria-label="日志级别" @change="load(1)">
            <option value="">全部级别</option>
            <option value="info">INFO</option>
            <option value="warn">WARN</option>
            <option value="error">ERROR</option>
            <option value="debug">DEBUG</option></select
          ><button class="button" :disabled="busy">搜索</button
          ><button
            type="button"
            class="button secondary"
            :aria-pressed="auto"
            @click="auto = !auto"
          >
            <component :is="auto ? Pause : Play" :size="14" />{{ auto ? '暂停刷新' : '自动刷新' }}
          </button>
        </form>
        <div class="log-timefilters">
          <label>开始时间<input v-model="since" type="datetime-local" /></label
          ><label>结束时间<input v-model="until" type="datetime-local" /></label
          ><button class="text-button" :disabled="busy" @click="load(1)">
            <RefreshCw :size="14" />应用时间范围</button
          ><span
            >{{ auto ? '每 5 秒刷新首页 · ' : '' }}{{ updated ? `更新于 ${updated}` : '' }}</span
          >
        </div>
        <Notice :message="error" /><Notice
          v-if="result?.persistence_error"
          message="日志文件写入异常，当前页面仍显示内存中的日志，请检查磁盘权限与空间。"
        />
        <div v-if="busy && !result" class="empty-state">
          <span class="spinner"></span>
          <p>正在读取日志…</p>
        </div>
        <div v-else-if="result && !result.items.length" class="empty-state">
          <Activity :size="35" />
          <h3>没有匹配的日志</h3>
          <p>调整级别、关键词或时间范围。仅显示本服务保留的应用日志。</p>
        </div>
        <div v-else class="log-lines">
          <details v-for="entry in result?.items || []" :key="entry.id" class="log-entry">
            <summary>
              <time>{{ fmt(entry.time) }}</time><span :class="['log-level', entry.level]">{{
                entry.level.toUpperCase()
              }}</span
              ><span class="log-message">{{ entry.message }}</span>
            </summary>
            <pre>{{ JSON.stringify(entry.fields || {}, null, 2) }}</pre>
          </details>
        </div>
        <Pagination
          v-if="result"
          :page="page"
          :pages="pages"
          :total="result.total"
          :busy="busy"
          @change="load"
        />
      </div>
      <div class="logs-scope">
        <p>{{ status?.log_scope }}</p>
        <p>
          {{ status?.log_disk_retention }}。月度日志保存在 logs/，文件名为 service-YYYY-MM.jsonl。
        </p>
      </div></AdminGate
    >
  </section>
</template>
