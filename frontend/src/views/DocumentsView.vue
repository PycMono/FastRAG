<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  ArrowUpRight,
  FileText,
  FolderOpen,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  LayoutGrid,
  List,
} from '@lucide/vue'
import { get, post } from '../api'
import type { Doc, Page, Workspace } from '../types'
import Notice from '../components/Notice.vue'
import Pagination from '../components/Pagination.vue'
import Modal from '../components/Modal.vue'
const props = defineProps<{ workspace: Workspace; revision: number }>()
const emit = defineEmits<{ import: []; open: [id: string] }>()
const items = ref<Doc[]>([]),
  total = ref(0),
  page = ref(1),
  pages = ref(1),
  busy = ref(false),
  error = ref(''),
  loaded = ref(false)
const pending = ref<Doc | null>(null),
  deleting = ref(false),
  deleteError = ref('')
const formatTime = (ts: number) =>
  ts
    ? new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'short',
        hour12: false,
      }).format(new Date(ts))
    : '—'
const viewMode = ref<'list' | 'grid'>('list')
const filter = ref(''),
  contentType = ref('all'),
  pageSize = ref(10)
const visible = computed(() =>
  items.value.filter((doc) => {
    const matchesName = `${doc.title} ${doc.doc_name}`
      .toLocaleLowerCase()
      .includes(filter.value.toLocaleLowerCase())
    return (
      matchesName &&
      (contentType.value === 'all' ||
        (contentType.value === 'full' ? doc.has_content : !doc.has_content))
    )
  }),
)
function clearFilters() {
  filter.value = ''
  contentType.value = 'all'
}
let sequence = 0
async function load(target = page.value) {
  const request = ++sequence
  busy.value = true
  error.value = ''
  try {
    const result = await get<Page<Doc>>('/api/v1/docs', {
      ...props.workspace,
      page: target,
      page_size: pageSize.value,
    })
    if (request !== sequence) return
    const last = Math.max(1, result.pages || Math.ceil(result.total / pageSize.value))
    if (target > last) {
      await load(last)
      return
    }
    items.value = result.list || []
    total.value = result.total
    pages.value = last
    page.value = target
    loaded.value = true
  } catch (e) {
    if (request === sequence) {
      error.value = (e as Error).message
      items.value = []
      loaded.value = false
    }
  } finally {
    if (request === sequence) busy.value = false
  }
}
function confirmDelete(doc: Doc) {
  pending.value = doc
  deleteError.value = ''
}
async function remove() {
  if (!pending.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    await post('/api/v1/docs/delete', { ...props.workspace, doc_name: pending.value.doc_name })
    pending.value = null
    await load()
  } catch (e) {
    deleteError.value = (e as Error).message
  } finally {
    deleting.value = false
  }
}
onMounted(() => load(1))
watch(
  () => props.revision,
  () => load(1),
)
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>文档管理</h1>
        <p>你的知识，都在这里。管理文档、阅读原文，或检查检索切片。</p>
      </div>
      <button class="button" @click="emit('import')"><Plus :size="18" />导入文档</button>
    </div>
    <div class="panel document-panel">
      <div class="panel-toolbar">
        <h2>
          全部文档 <span class="count">{{ loaded ? total : '—' }}</span>
        </h2>
        <div class="document-view-controls">
          <div class="segmented view-toggle" aria-label="文档显示方式">
            <button
              :class="{ active: viewMode === 'list' }"
              aria-label="列表视图"
              :aria-pressed="viewMode === 'list'"
              @click="viewMode = 'list'"
            >
              <List :size="16" /></button
            ><button
              :class="{ active: viewMode === 'grid' }"
              aria-label="网格视图"
              :aria-pressed="viewMode === 'grid'"
              @click="viewMode = 'grid'"
            >
              <LayoutGrid :size="15" />
            </button>
          </div>
          <button class="button secondary compact" :disabled="busy" @click="load()">
            <RefreshCw :size="15" :class="{ spinning: busy }" />刷新列表
          </button>
        </div>
      </div>
      <div class="document-filters">
        <label class="filter-input"
          ><Search :size="17" /><input
            v-model="filter"
            aria-label="筛选当前页文档"
            placeholder="筛选当前页名称…" /></label
        ><select v-model="contentType" aria-label="文档内容类型">
          <option value="all">全部内容类型</option>
          <option value="full">完整文档</option>
          <option value="chunks">仅切片</option></select
        ><select v-model="pageSize" aria-label="每页文档数" :disabled="busy" @change="load(1)">
          <option :value="10">每页 10 篇</option>
          <option :value="20">每页 20 篇</option>
          <option :value="50">每页 50 篇</option>
        </select>
      </div>
      <p v-if="filter || contentType !== 'all'" class="filter-caption">
        当前页 {{ items.length }} 篇中匹配 {{ visible.length }} 篇；其他分页不参与筛选。<button
          class="text-button"
          @click="clearFilters"
        >
          清除筛选
        </button>
      </p>
      <Notice :message="error" />
      <div v-if="busy" class="loading-line" role="status">
        <span class="spinner"></span>正在加载文档…
      </div>
      <div v-if="!loaded && !busy" class="empty-state">
        <FolderOpen :size="38" />
        <h3>暂时无法加载文档</h3>
        <p>检查工作区与服务连接后重试。</p>
        <button class="button secondary" @click="load()">重新加载</button>
      </div>
      <div v-else-if="loaded && !items.length && !busy" class="empty-state">
        <FolderOpen :size="42" />
        <h3>知识库中还没有文档</h3>
        <p>导入第一篇文档，开始构建你的知识库。</p>
        <button class="button" @click="emit('import')"><Plus :size="16" />导入文档</button>
      </div>
      <div v-else-if="items.length && !visible.length && !busy" class="empty-state">
        <Search :size="32" />
        <h3>当前页没有匹配的文档</h3>
        <p>更换筛选词，或翻页查看其他文档。</p>
        <button class="button secondary" @click="clearFilters">清除筛选</button>
      </div>
      <div v-else-if="items.length && viewMode === 'grid'" class="document-grid">
        <article v-for="doc in visible" :key="doc.doc_id" class="document-tile">
          <button class="tile-open" @click="emit('open', doc.doc_id)">
            <span :class="['tile-file', { 'text-file': /\.txt$/i.test(doc.doc_name) }]"
              ><FileText :size="36" /></span
            ><strong>{{ doc.title || doc.doc_name }}</strong
            ><small>{{ doc.doc_name }}</small>
          </button>
          <div class="tile-footer">
            <span>{{ doc.chunk_count }} 个切片</span
            ><button
              class="icon-button danger-text"
              :aria-label="`删除 ${doc.doc_name}`"
              @click="confirmDelete(doc)"
            >
              <Trash2 :size="15" />
            </button>
          </div>
        </article>
      </div>
      <div v-else-if="items.length" class="table-scroll">
        <table :aria-busy="busy">
          <thead>
            <tr>
              <th>文档名称</th>
              <th>切片数量</th>
              <th>内容类型</th>
              <th>最近更新</th>
              <th class="align-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="doc in visible" :key="doc.doc_id">
              <td>
                <button class="document-link" @click="emit('open', doc.doc_id)">
                  <span :class="['file-icon', { 'text-file': /\.txt$/i.test(doc.doc_name) }]"
                    ><FileText :size="21" /></span
                  ><span
                    ><strong>{{ doc.title || doc.doc_name }}</strong
                    ><small>{{ doc.doc_name }}</small></span
                  >
                </button>
              </td>
              <td>
                <span class="number">{{ doc.chunk_count }}</span><span class="muted"> 个</span>
              </td>
              <td>
                <span class="tag" :class="{ neutral: !doc.has_content }">{{
                  doc.has_content ? '完整文档' : '仅切片'
                }}</span>
              </td>
              <td class="date-cell">{{ formatTime(doc.update_ts) }}</td>
              <td>
                <div class="row-actions">
                  <button class="text-button" @click="emit('open', doc.doc_id)">
                    查看<ArrowUpRight :size="14" /></button
                  ><button
                    class="icon-button danger-text"
                    :aria-label="`删除 ${doc.doc_name}`"
                    @click="confirmDelete(doc)"
                  >
                    <Trash2 :size="16" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination
        v-if="loaded && total"
        :page="page"
        :pages="pages"
        :total="total"
        :busy="busy"
        @change="load"
      />
    </div>
    <p class="footnote">同一知识库中导入同名文档会覆盖更新，文档编号保持不变。</p>
    <Modal v-if="pending" title="删除文档" :locked="deleting" @close="!deleting && (pending = null)"
      ><div class="delete-symbol"><Trash2 :size="25" /></div>
      <h3>确定删除这篇文档？</h3>
      <p class="wrap">{{ pending.doc_name }}</p>
      <p class="muted">文档及其检索切片将被删除。需要恢复时，请重新导入原文件。</p>
      <Notice :message="deleteError" />
      <div class="form-actions">
        <button class="button secondary" :disabled="deleting" @click="pending = null">取消</button
        ><button class="button danger" :disabled="deleting" @click="remove">
          {{ deleting ? '正在删除…' : '确认删除' }}
        </button>
      </div></Modal
    >
  </section>
</template>
