<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  Check,
  ChevronDown,
  Database,
  FileText,
  LayoutGrid,
  Search,
  Settings2,
  Upload,
  Home,
  Activity,
  X,
} from '@lucide/vue'
import HomeView from './views/HomeView.vue'
import ConfigView from './views/ConfigView.vue'
import LogsView from './views/LogsView.vue'
import DocumentsView from './views/DocumentsView.vue'
import ImportView from './views/ImportView.vue'
import SearchView from './views/SearchView.vue'
import DocumentDrawer from './components/DocumentDrawer.vue'
import type { Workspace } from './types'

const configDirty = ref(false)
const nav = [
  { id: 'home', label: '概览', icon: Home },
  { id: 'documents', label: '文档管理', icon: FileText },
  { id: 'import', label: '导入文档', icon: Upload },
  { id: 'search', label: '检索测试', icon: Search },
  { id: 'config', label: '配置管理', icon: Settings2 },
  { id: 'logs', label: '运行日志', icon: Activity },
]
const page = ref('home')
function syncPage() {
  const id = location.hash.slice(1)
  const next = nav.some((n) => n.id === id) ? id : 'home'
  if (
    page.value === 'config' &&
    next !== 'config' &&
    configDirty.value &&
    !window.confirm('配置有未保存修改，确定离开吗？')
  ) {
    location.hash = 'config'
    return
  }
  if (next !== 'config') configDirty.value = false
  page.value = next
}
syncPage()
const initialQuery = new URLSearchParams(location.search).get('q') || ''
if (initialQuery) {
  page.value = 'search'
  location.hash = 'search'
}
let saved: Partial<Workspace> = {}
try {
  saved = JSON.parse(localStorage.getItem('fastrag.workspace') || '{}') || {}
} catch {
  /* storage unavailable */
}
const workspace = ref<Workspace>({
  tenant: typeof saved.tenant === 'string' && saved.tenant.trim() ? saved.tenant.trim() : 'demo',
  kb_no: typeof saved.kb_no === 'string' && saved.kb_no.trim() ? saved.kb_no.trim() : 'demo-kb',
})
const processing = ref(false)
const recentWorkspaces = ref<Workspace[]>([])
try {
  const saved = JSON.parse(localStorage.getItem('fastrag.recent-workspaces') || '[]')
  if (Array.isArray(saved))
    recentWorkspaces.value = saved
      .filter((w) => w && typeof w.tenant === 'string' && typeof w.kb_no === 'string')
      .slice(0, 5)
} catch {
  /* unavailable */
}
function rememberWorkspace() {
  recentWorkspaces.value = [
    { ...workspace.value },
    ...recentWorkspaces.value.filter(
      (w) => w.tenant !== workspace.value.tenant || w.kb_no !== workspace.value.kb_no,
    ),
  ].slice(0, 5)
  try {
    localStorage.setItem('fastrag.recent-workspaces', JSON.stringify(recentWorkspaces.value))
  } catch {
    /* unavailable */
  }
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (processing.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}
const draft = ref({ ...workspace.value })
const editing = ref(false)
const revision = ref(0)
const docId = ref('')
const ready = ref<'checking' | 'ready' | 'error'>('checking')
const workspaceKey = computed(() => JSON.stringify(workspace.value))
function navigate(id: string) {
  location.hash = id
  syncPage()
}
function toggleWorkspace() {
  draft.value = { ...workspace.value }
  editing.value = !editing.value
}
function applyWorkspace() {
  if (processing.value) return
  workspace.value = { tenant: draft.value.tenant.trim(), kb_no: draft.value.kb_no.trim() }
  try {
    localStorage.setItem('fastrag.workspace', JSON.stringify(workspace.value))
  } catch {
    /* private mode */
  }
  rememberWorkspace()
  docId.value = ''
  editing.value = false
}
function focusMain() {
  document.getElementById('main')?.focus()
}
function showDoc(id: string) {
  docId.value = id
}
let healthTimer: ReturnType<typeof setInterval>
const showHealth = ref(false)
const healthChecks = ref<Record<string, string>>({})
const healthController = new AbortController()
function toggleHealth() {
  showHealth.value = !showHealth.value
  checkHealth()
}
function chooseWorkspace(item: Workspace) {
  draft.value = { ...item }
  applyWorkspace()
}
async function checkHealth() {
  try {
    const response = await fetch('/ready', { signal: healthController.signal })
    const data = await response.json()
    healthChecks.value = data.checks || {}
    ready.value = response.ok && data.status === 'ready' ? 'ready' : 'error'
  } catch {
    ready.value = 'error'
    healthChecks.value = { connection: '无法连接后端服务' }
  }
}
onMounted(() => {
  window.addEventListener('hashchange', syncPage)
  window.addEventListener('beforeunload', beforeUnload)
  rememberWorkspace()
  checkHealth()
  healthTimer = setInterval(checkHealth, 30000)
})
onUnmounted(() => {
  window.removeEventListener('hashchange', syncPage)
  window.removeEventListener('beforeunload', beforeUnload)
  clearInterval(healthTimer)
  healthController.abort()
})
</script>

<template>
  <a class="skip-link" href="#main" @click.prevent="focusMain">跳转到主要内容</a>
  <div class="site-shell">
    <header class="site-header">
      <a class="site-brand" href="#home" aria-label="FastRAG 首页"
        ><Database :size="25" /><strong>FastRAG</strong></a
      >
      <nav class="site-nav" aria-label="主要导航">
        <a
          v-for="item in nav"
          :key="item.id"
          :href="`#${item.id}`"
          :aria-current="page === item.id ? 'page' : undefined"
          :class="{ active: page === item.id }"
          >{{ item.label }}</a
        >
      </nav>
      <button
        class="workspace-switch"
        :disabled="processing"
        :aria-expanded="editing"
        aria-label="切换工作区"
        @click="toggleWorkspace"
      >
        <span>{{ workspace.kb_no }}</span><ChevronDown :size="14" />
      </button>
    </header>
    <main id="main" :class="['site-main', { 'is-home': page === 'home' }]" tabindex="-1">
      <form v-if="editing" class="workspace-editor panel" @submit.prevent="applyWorkspace">
        <label
          >租户标识<input
            v-model="draft.tenant"
            required
            maxlength="64"
            pattern=".*\S.*"
            autocomplete="off" /></label
        ><label
          >知识库编号<input
            v-model="draft.kb_no"
            required
            maxlength="64"
            pattern=".*\S.*"
            autocomplete="off" /></label
        ><button class="button" type="submit"><Check :size="16" />应用工作区</button
        ><button class="icon-button" type="button" aria-label="取消切换" @click="editing = false">
          <X :size="18" />
        </button>
        <div v-if="recentWorkspaces.length" class="recent-workspaces">
          <span>最近使用</span
          ><button
            v-for="item in recentWorkspaces"
            :key="item.tenant + '/' + item.kb_no"
            class="workspace-chip"
            type="button"
            @click="chooseWorkspace(item)"
          >
            <strong>{{ item.kb_no }}</strong><small>{{ item.tenant }}</small>
          </button>
        </div>
        <p class="muted">填写已有知识库编号。工作区选择不会创建知识库，也不代表访问授权。</p>
      </form>
      <HomeView v-show="page === 'home'" @navigate="navigate" />
      <div v-show="page !== 'home'" class="workspace-context">
        <span>{{ workspace.tenant }}<span class="slash">/</span>{{ workspace.kb_no }}</span
        ><button :class="['status', ready]" @click="toggleHealth">
          <span></span
          >{{
            ready === 'ready' ? '服务已就绪' : ready === 'checking' ? '检查服务中' : '服务未就绪'
          }}
        </button>
      </div>
      <div v-if="showHealth && page !== 'home'" class="health-details">
        <div>
          <strong>依赖服务状态</strong
          ><button class="text-button" @click="checkHealth">重新检查</button>
        </div>
        <p v-for="(state, key) in healthChecks" :key="key">
          <span>{{
            key === 'mysql'
              ? 'MySQL 数据库'
              : key === 'elasticsearch'
                ? 'Elasticsearch 检索'
                : '服务连接'
          }}</span><span :class="state === 'ok' ? 'healthy-text' : 'danger-text'">{{
            state === 'ok' ? '运行正常' : state
          }}</span>
        </p>
      </div>
      <div :key="workspaceKey">
        <DocumentsView
          v-show="page === 'documents'"
          :workspace="workspace"
          :revision="revision"
          @import="navigate('import')"
          @open="showDoc"
        /><ImportView
          v-show="page === 'import'"
          :workspace="workspace"
          @imported="revision++"
          @busy="processing = $event"
          @search="navigate('search')"
          @open="showDoc"
          @documents="navigate('documents')"
        /><SearchView
          v-show="page === 'search'"
          :workspace="workspace"
          :initial-query="initialQuery"
          @open="showDoc"
        />
      </div>
      <ConfigView v-if="page === 'config'" @dirty="configDirty = $event" />
      <LogsView v-if="page === 'logs'" :active="page === 'logs'" />
    </main>
    <footer class="site-footer">
      <span>FastRAG</span>
      <p>让每一次查找，都有据可循。</p>
      <a href="#documents">前往知识库</a>
    </footer>
    <DocumentDrawer
      v-if="docId"
      :key="workspaceKey + docId"
      :doc-id="docId"
      :workspace="workspace"
      @close="docId = ''"
    />
  </div>
</template>
