<script setup lang="ts">
import { ref } from 'vue'
import {
  ArrowRight,
  CheckCircle2,
  FileText,
  Link,
  SlidersHorizontal,
  Upload,
  UploadCloud,
} from '@lucide/vue'
import { post } from '../api'
import type { IngestResult, Workspace } from '../types'
import Notice from '../components/Notice.vue'
import BatchImport from '../components/BatchImport.vue'
const props = defineProps<{ workspace: Workspace }>()
const emit = defineEmits<{
  imported: []
  open: [id: string]
  documents: []
  search: []
  busy: [value: boolean]
}>()
const modes = [
  { id: 'file', label: '上传文件', icon: Upload },
  { id: 'batch', label: '批量上传', icon: UploadCloud },
  { id: 'paste', label: '粘贴文本', icon: FileText },
  { id: 'url', label: '链接导入', icon: Link },
]
const mode = ref('file'),
  name = ref(''),
  content = ref(''),
  url = ref(''),
  format = ref('markdown'),
  model = ref(''),
  chunkSize = ref<number | ''>(''),
  refresh = ref(true)
const file = ref<HTMLInputElement>(),
  fileName = ref(''),
  fileSize = ref(''),
  dragging = ref(false),
  busy = ref(false),
  reading = ref(false),
  error = ref(''),
  result = ref<IngestResult>(),
  elapsed = ref(''),
  immediatelyVisible = ref(true)
function batchBusy(value: boolean) {
  busy.value = value
  emit('busy', value)
}
let fileSequence = 0
async function readFile(selected?: File) {
  if (!selected || busy.value) return
  error.value = ''
  result.value = undefined
  if (!/\.(md|markdown|txt)$/i.test(selected.name)) {
    error.value = '请选择 Markdown（.md、.markdown）或纯文本（.txt）文件。'
    return
  }
  const seq = ++fileSequence
  reading.value = true
  try {
    const text = await selected.text()
    if (seq !== fileSequence) return
    content.value = text
    name.value = selected.name
    fileName.value = selected.name
    fileSize.value = `${(selected.size / 1024).toFixed(1)} KB`
    format.value = /\.(md|markdown)$/i.test(selected.name) ? 'markdown' : 'text'
  } catch {
    error.value = '读取文件失败，请重新选择。'
  } finally {
    if (seq === fileSequence) reading.value = false
  }
}
function dropped(event: DragEvent) {
  dragging.value = false
  readFile(event.dataTransfer?.files[0])
}
const drafts: Record<
  string,
  { name: string; content: string; url: string; format: string; fileName: string; fileSize: string }
> = {}
function selectMode(value: string) {
  drafts[mode.value] = {
    name: name.value,
    content: content.value,
    url: url.value,
    format: format.value,
    fileName: fileName.value,
    fileSize: fileSize.value,
  }
  mode.value = value
  const draft = drafts[value]
  name.value = draft?.name || ''
  content.value = draft?.content || ''
  url.value = draft?.url || ''
  format.value = draft?.format || 'markdown'
  fileName.value = draft?.fileName || ''
  fileSize.value = draft?.fileSize || ''
  error.value = ''
  result.value = undefined
}

function reset() {
  ++fileSequence
  reading.value = false
  name.value = ''
  content.value = ''
  url.value = ''
  fileName.value = ''
  fileSize.value = ''
  error.value = ''
  result.value = undefined
  if (file.value) file.value.value = ''
}
async function submit() {
  error.value = ''
  result.value = undefined
  if (!props.workspace.tenant || !props.workspace.kb_no) {
    error.value = '请先选择有效的租户与知识库。'
    return
  }
  if (mode.value !== 'url' && (!name.value.trim() || !content.value.trim())) {
    error.value = '请填写文档名称，并上传文件或输入正文。'
    return
  }
  if (mode.value === 'url') {
    try {
      const parsed = new URL(url.value)
      if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error()
    } catch {
      error.value = '请输入完整的 HTTP 或 HTTPS 文档链接。'
      return
    }
  }
  const body: Record<string, unknown> = { ...props.workspace, refresh: refresh.value }
  if (name.value.trim()) body.doc_name = name.value.trim()
  if (mode.value === 'url') body.url = url.value.trim()
  else {
    body.format = format.value
    body.content = content.value
  }
  if (model.value.trim()) body.model = model.value.trim()
  if (chunkSize.value !== '') body.split_options = { chunk_size: Number(chunkSize.value) }
  busy.value = true
  emit('busy', true)
  const start = performance.now()
  try {
    result.value = await post<IngestResult>(
      mode.value === 'url' ? '/api/v1/docs/parse' : '/api/v1/docs',
      body,
    )
    elapsed.value = ((performance.now() - start) / 1000).toFixed(1)
    immediatelyVisible.value = refresh.value
    emit('imported')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
    emit('busy', false)
  }
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>导入文档</h1>
        <p>将文件、文本或在线文档添加到当前知识库。</p>
      </div>
      <button class="button secondary" @click="emit('documents')">
        返回文档列表<ArrowRight :size="16" />
      </button>
    </div>
    <div class="import-layout">
      <div class="panel import-panel">
        <div class="tabs import-tabs">
          <button
            v-for="item in modes"
            :key="item.id"
            :class="{ active: mode === item.id }"
            :disabled="busy || reading"
            @click="selectMode(item.id)"
          >
            <component :is="item.icon" :size="17" />{{ item.label }}
          </button>
        </div>
        <BatchImport
          v-show="mode === 'batch'"
          :workspace="workspace"
          @busy="batchBusy"
          @imported="emit('imported')"
          @open="emit('open', $event)"
        />
        <form v-show="mode !== 'batch'" class="import-form" @submit.prevent="submit">
          <fieldset :disabled="busy || reading">
            <template v-if="mode === 'file'"
              ><input
                ref="file"
                class="sr-only"
                type="file"
                accept=".md,.markdown,.txt"
                aria-label="选择文档文件"
                @change="readFile(($event.target as HTMLInputElement).files?.[0])"
              /><button
                type="button"
                :class="['dropzone', { dragging }]"
                @click="file?.click()"
                @dragover.prevent="dragging = true"
                @dragleave.prevent="dragging = false"
                @drop.prevent="dropped"
              >
                <span class="upload-symbol"><UploadCloud :size="30" /></span
                ><strong>{{
                  reading ? '正在读取文件…' : fileName || '点击选择文件，或拖拽到这里'
                }}</strong
                ><span>{{
                  fileName ? `${fileSize} · 点击重新选择` : '支持 Markdown 和 TXT 文档'
                }}</span>
              </button></template
            ><label v-if="mode === 'url'"
              >文档链接 <span class="required">*</span
              ><input
                v-model="url"
                type="url"
                required
                maxlength="2048"
                placeholder="https://example.com/产品手册.pdf"
              /><small>支持 PDF、Word、PPT 等格式，链接需可供解析服务访问。</small></label
            ><label
              >文档名称 <span v-if="mode !== 'url'" class="required">*</span
              ><input
                v-model="name"
                :required="mode !== 'url'"
                maxlength="512"
                :placeholder="
                  mode === 'url' ? '可选，默认使用链接中的文件名' : '例如：产品使用手册.md'
                "
              /><small>同名文档会覆盖更新，请确认名称后再导入。</small></label
            ><label v-if="mode !== 'url'"
              >{{ mode === 'file' ? '正文预览与编辑' : '文档正文'
              }}<textarea
                v-model="content"
                :required="mode !== 'url'"
                :rows="mode === 'file' ? 4 : 10"
                placeholder="# 文档标题&#10;&#10;在这里输入或粘贴文档内容…"
              /><small>{{ content.length.toLocaleString() }} 字符</small></label
            >
            <details class="advanced">
              <summary>
                <SlidersHorizontal :size="16" />导入设置<span>格式、切片与模型</span>
              </summary>
              <div class="field-grid">
                <label v-if="mode !== 'url'"
                  >文档格式<select v-model="format">
                    <option value="markdown">Markdown · 按标题分段</option>
                    <option value="text">纯文本 · 按段落分段</option>
                  </select></label
                ><label
                  >切片长度<input
                    v-model="chunkSize"
                    type="number"
                    min="100"
                    max="4000"
                    placeholder="默认使用服务配置" /></label
                ><label
                  >向量模型<input v-model="model" maxlength="64" placeholder="默认使用服务配置"
                /></label>
              </div>
              <p class="muted">
                向量模型填写服务端已配置的名称，同一知识库的导入与检索应使用一致的模型。
              </p>
            </details>
            <label class="checkbox-line"
              ><input v-model="refresh" type="checkbox" /><span
                >导入后立即可检索<small>关闭后可能需要等待约 30 秒。</small></span
              ></label
            >
          </fieldset>
          <Notice :message="error" />
          <div v-if="busy" class="notice progress" role="status">
            <span class="spinner"></span
            >{{
              mode === 'url'
                ? '正在解析文档并导入，可能需要几十秒，请保持页面打开。'
                : '正在切分、生成向量并保存文档，请稍候…'
            }}
          </div>
          <div v-if="result" class="import-success" role="status">
            <CheckCircle2 :size="23" />
            <div>
              <strong>{{ result.created ? '文档导入成功' : '文档更新成功' }}</strong>
              <p>{{ result.doc_name }} · {{ result.chunk_count }} 个切片 · 用时 {{ elapsed }} 秒</p>
              <p v-if="!immediatelyVisible">检索切片可能需要约 30 秒后可见。</p>
              <button type="button" class="text-button" @click="emit('open', result.doc_id)">
                查看文档<ArrowRight :size="14" /></button
              ><button type="button" class="text-button" @click="emit('search')">
                去检索验证<ArrowRight :size="14" />
              </button>
            </div>
          </div>
          <div class="form-actions">
            <button
              class="button secondary"
              type="button"
              :disabled="busy || reading"
              @click="reset"
            >
              清空内容</button
            ><button class="button" type="submit" :disabled="busy || reading">
              <Upload :size="16" />{{
                busy ? '正在导入…' : mode === 'url' ? '解析并导入' : '导入到知识库'
              }}
            </button>
          </div>
          <details v-if="result" class="debug-data">
            <summary>查看响应数据</summary>
            <pre>{{ JSON.stringify(result, null, 2) }}</pre>
          </details>
        </form>
      </div>
      <aside class="import-guide">
        <div class="destination-card">
          <span class="guide-icon"><FileText :size="23" /></span
          ><span class="reader-kicker">导入位置</span>
          <h2>{{ workspace.kb_no }}</h2>
          <p>租户 {{ workspace.tenant }}</p>
          <div class="destination-rule"></div>
          <strong>文档将如何处理</strong>
          <ul class="process-list">
            <li>
              <span>1</span>
              <div>解析内容<small>保留文档标题和正文结构</small></div>
            </li>
            <li>
              <span>2</span>
              <div>切分与索引<small>生成可检索的知识片段</small></div>
            </li>
            <li>
              <span>3</span>
              <div>验证效果<small>导入后用真实问题进行检索</small></div>
            </li>
          </ul>
        </div>
        <div class="import-note">
          <strong>关于同名文档</strong>
          <p>相同知识库中，同名导入会更新原文和切片。需要保留两个版本时，请使用不同的文档名称。</p>
        </div>
      </aside>
    </div>
  </section>
</template>
