<script setup lang="ts">
import { computed, ref } from 'vue'
import { FileText, Plus, Trash2, UploadCloud } from '@lucide/vue'
import { post } from '../api'
import type { IngestResult, Workspace } from '../types'
import Notice from './Notice.vue'
type Entry = {
  name: string
  content: string
  size: number
  format: string
  state: string
  error: string
  result?: IngestResult
}
const props = defineProps<{ workspace: Workspace }>()
const emit = defineEmits<{ imported: []; open: [id: string]; busy: [value: boolean] }>()
const entries = ref<Entry[]>([]),
  input = ref<HTMLInputElement>(),
  busy = ref(false),
  error = ref(''),
  reading = ref(false),
  refresh = ref(true),
  model = ref('')
const completed = computed(() => entries.value.filter((e) => e.state === 'success').length)
const ready = computed(() => entries.value.filter((e) => e.state === 'ready'))
async function add(files: FileList | null) {
  if (!files || busy.value || reading.value) return
  reading.value = true
  error.value = ''
  try {
    for (const f of Array.from(files)) {
      if (entries.value.length >= 100) {
        error.value = '每批最多 100 个文件，请分批导入。'
        break
      }
      if (!/\.(md|markdown|txt)$/i.test(f.name)) {
        error.value = `已跳过 ${f.name}：仅支持 Markdown 与 TXT。`
        continue
      }
      if (entries.value.some((e) => e.name === f.name)) {
        error.value = `已跳过同名文件 ${f.name}，请先移除旧文件再选择。`
        continue
      }
      const content = await f.text()
      entries.value.push({
        name: f.name,
        content,
        size: f.size,
        format: /\.(md|markdown)$/i.test(f.name) ? 'markdown' : 'text',
        state: content.trim() ? 'ready' : 'failed',
        error: content.trim() ? '' : '文件内容为空',
      })
    }
  } catch {
    error.value = '读取文件失败，请重新选择。'
  } finally {
    reading.value = false
    if (input.value) input.value.value = ''
  }
}
async function submit() {
  const selected = [...ready.value]
  if (!selected.length || busy.value) return
  busy.value = true
  emit('busy', true)
  error.value = ''
  selected.forEach((e) => {
    e.state = 'processing'
  })
  try {
    const result = await post<{
      success: number
      failed: number
      items: IngestResult[] | null
      errors: { doc_name: string; message: string }[] | null
    }>(
      '/api/v1/docs/batch',
      selected.map((e) => ({
        ...props.workspace,
        doc_name: e.name,
        content: e.content,
        format: e.format,
        refresh: refresh.value,
        ...(model.value.trim() ? { model: model.value.trim() } : {}),
      })),
    )
    for (const entry of selected) {
      const item = result.items?.find((i) => i.doc_name === entry.name)
      entry.result = item
      entry.state = item ? 'success' : 'failed'
      entry.error =
        result.errors?.find((e) => e.doc_name === entry.name)?.message ||
        (item ? '' : '未返回明确结果，请刷新文档列表确认后再决定是否重试。')
    }
    if (result.success) emit('imported')
  } catch (e) {
    error.value = (e as Error).message
    selected.forEach((entry) => {
      entry.state = 'uncertain'
      entry.error = '结果未确认，请先在文档管理中检查，避免重复导入。'
    })
  } finally {
    busy.value = false
    emit('busy', false)
  }
}
</script>
<template>
  <div class="batch-import">
    <div class="section-toolbar">
      <div>
        <h2>批量导入文件</h2>
        <p class="muted">一次选择多份文档，逐条查看导入结果。</p>
      </div>
      <span class="tag neutral">最多 100 份</span>
    </div>
    <input
      ref="input"
      class="sr-only"
      aria-label="选择批量文档"
      type="file"
      multiple
      accept=".md,.markdown,.txt"
      @change="add(($event.target as HTMLInputElement).files)"
    /><button
      class="dropzone batch-dropzone"
      :disabled="busy || reading"
      @click="input?.click()"
      @dragover.prevent
      @drop.prevent="add($event.dataTransfer?.files || null)"
    >
      <UploadCloud :size="30" /><strong>{{
        reading ? '正在读取文件…' : '选择多个文件，或将文件拖到这里'
      }}</strong
      ><span>Markdown / TXT · 同名文件会更新已有文档</span></button
    ><Notice :message="error" />
    <div v-if="entries.length" class="queue-summary">
      <strong>{{ entries.length }} 份文件</strong
      ><span>{{ completed }} 份成功 · {{ ready.length }} 份待导入</span
      ><button class="text-button" :disabled="busy || reading" @click="entries = []">
        清空列表
      </button>
    </div>
    <ul class="file-queue">
      <li v-for="(entry, index) in entries" :key="entry.name">
        <FileText :size="20" />
        <div>
          <strong>{{ entry.name }}</strong><small
            >{{ (entry.size / 1024).toFixed(1) }} KB
            <span v-if="entry.state === 'processing'">· 正在导入…</span
            ><span v-else-if="entry.state === 'success'"
              >· 已成功导入 {{ entry.result?.chunk_count }} 个切片</span
            ><span v-else-if="entry.state === 'ready'">· 待导入</span></small
          >
          <p v-if="entry.error" class="danger-text">{{ entry.error }}</p>
        </div>
        <button v-if="entry.result" class="text-button" @click="emit('open', entry.result.doc_id)">
          查看文档</button
        ><button
          v-else
          class="icon-button"
          :aria-label="`移除 ${entry.name}`"
          :disabled="busy || reading"
          @click="entries.splice(index, 1)"
        >
          <Trash2 :size="16" />
        </button>
      </li>
    </ul>
    <div class="field-grid">
      <label
        >向量模型<input
          v-model="model"
          :disabled="busy"
          maxlength="64"
          placeholder="默认使用服务配置"
        /><small>应与知识库现有文档的模型保持一致。</small></label
      ><label class="checkbox-line"
        ><input v-model="refresh" :disabled="busy" type="checkbox" /><span
          >导入后立即可检索</span
        ></label
      >
    </div>
    <div v-if="busy" class="notice progress" role="status">
      <span class="spinner"></span>正在批量导入。完成后会显示每份文件的真实结果。
    </div>
    <div class="form-actions">
      <button class="button" :disabled="busy || reading || !ready.length" @click="submit">
        <Plus :size="16" />{{ busy ? '正在导入…' : `导入 ${ready.length} 份文档` }}
      </button>
    </div>
  </div>
</template>
