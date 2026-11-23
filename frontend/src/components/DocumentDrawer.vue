<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ExternalLink, FileText, Layers, RefreshCw } from '@lucide/vue'
import { get } from '../api'
import type { ChunkPage, DocDetail, Workspace } from '../types'
import Modal from './Modal.vue'
import Notice from './Notice.vue'
import Pagination from './Pagination.vue'
import MarkdownContent from './MarkdownContent.vue'
import CopyButton from './CopyButton.vue'
const props = defineProps<{ docId: string; workspace: Workspace }>()
defineEmits<{ close: [] }>()
const doc = ref<DocDetail>(),
  error = ref(''),
  busy = ref(false),
  tab = ref('content')
const chunks = ref<ChunkPage>(),
  chunkError = ref(''),
  chunkBusy = ref(false),
  chunkPage = ref(1)
const preview = ref(true)
function download() {
  if (!doc.value) return
  const blob = new Blob([doc.value.content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = doc.value.doc_name.replace(/[\\/]/g, '_')
  link.click()
  URL.revokeObjectURL(url)
}
const source = computed(() => {
  try {
    const url = new URL(doc.value?.source_url || '')
    return ['https:', 'http:'].includes(url.protocol) ? url.href : ''
  } catch {
    return ''
  }
})
async function load() {
  busy.value = true
  error.value = ''
  try {
    doc.value = await get<DocDetail>('/api/v1/docs/detail', {
      tenant: props.workspace.tenant,
      doc_id: props.docId,
    })
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function loadChunks(page = 1) {
  tab.value = 'chunks'
  chunkBusy.value = true
  chunkError.value = ''
  chunks.value = undefined
  try {
    chunks.value = await get<ChunkPage>('/api/v1/docs/chunks', {
      tenant: props.workspace.tenant,
      doc_id: props.docId,
      limit: 20,
      offset: (page - 1) * 20,
    })
    chunkPage.value = page
  } catch (e) {
    chunkError.value = (e as Error).message
  } finally {
    chunkBusy.value = false
  }
}
onMounted(load)
</script>
<template>
  <Modal :title="doc?.title || doc?.doc_name || '文档详情'" wide @close="$emit('close')"
    ><div v-if="busy" class="loading-line" role="status">
      <span class="spinner"></span>正在加载文档…
    </div>
    <Notice :message="error" /><button v-if="error" class="button secondary" @click="load">
      重试加载</button
    ><template v-if="doc"
      ><div class="detail-meta">
        <span class="tag">{{ doc.chunk_count }} 个切片</span
        ><span class="muted wrap">{{ doc.doc_name }}</span>
      </div>
      <p class="doc-identifier">文档编号 {{ doc.doc_id }}</p>
      <a v-if="source" :href="source" target="_blank" rel="noopener noreferrer" class="source-link"
        >查看来源<ExternalLink :size="14"
      /></a>
      <div class="tabs">
        <button :class="{ active: tab === 'content' }" @click="tab = 'content'">
          <FileText :size="16" />文档原文</button
        ><button :class="{ active: tab === 'chunks' }" :disabled="chunkBusy" @click="loadChunks()">
          <Layers :size="16" />检索切片
        </button>
      </div>
      <template v-if="tab === 'content'">
        <template v-if="doc.has_content"
          ><div class="reader-toolbar">
            <div class="segmented">
              <button :class="{ active: preview }" @click="preview = true">阅读预览</button
              ><button :class="{ active: !preview }" @click="preview = false">原始文本</button>
            </div>
            <div class="reader-actions">
              <CopyButton :text="doc.content" /><button class="text-button" @click="download">
                下载文档
              </button>
            </div>
          </div>
          <MarkdownContent v-if="preview" :content="doc.content" />
          <pre v-else class="document-content">{{ doc.content }}</pre>
        </template>
        <div v-else class="empty-state">
          <Layers :size="32" />
          <h3>这篇文档仅包含切片</h3>
          <p>导入时未保存原文，可在「检索切片」中查看内容。</p>
          <button class="button secondary" @click="loadChunks()">查看切片</button>
        </div></template
      ><template v-else
        ><Notice :message="chunkError" />
        <div class="section-toolbar">
          <span class="muted">{{
            chunks ? `共 ${chunks.total} 个切片` : '读取检索切片'
          }}</span><button class="text-button" :disabled="chunkBusy" @click="loadChunks(chunkPage)">
            <RefreshCw :size="14" />刷新
          </button>
        </div>
        <div v-if="chunkBusy" class="loading-line" role="status">
          <span class="spinner"></span>正在加载切片…
        </div>
        <div v-else-if="chunks && !chunks.total" class="empty-state">
          <Layers :size="32" />
          <h3>暂无可见切片</h3>
          <p>刚导入的文档可能需要等待约 30 秒。请稍后刷新。</p>
        </div>
        <article v-for="chunk in chunks?.items || []" :key="chunk.chunk_id" class="chunk-card">
          <div class="section-toolbar">
            <strong>{{ chunk.title || '未命名片段' }}</strong><span class="tag neutral"
              >切片 {{ chunk.order + 1 }}</span
            >
          </div>
          <p v-if="chunk.heading_path" class="muted wrap">{{ chunk.heading_path }}</p>
          <pre>{{ chunk.content }}</pre>
          <CopyButton :text="chunk.content" label="复制片段" />
        </article>
        <Pagination
          v-if="chunks?.total"
          :page="chunkPage"
          :pages="Math.ceil(chunks.total / 20)"
          :total="chunks.total"
          :busy="chunkBusy"
          @change="loadChunks" /></template></template
  ></Modal>
</template>
