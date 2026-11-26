<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Plus, Save, Trash2, RefreshCw, Settings2 } from '@lucide/vue'
import { adminRequest } from '../admin'
import AdminGate from '../components/AdminGate.vue'
import ConfigFields from '../components/ConfigFields.vue'
import Notice from '../components/Notice.vue'
import Modal from '../components/Modal.vue'
const emit = defineEmits<{ dirty: [value: boolean] }>()
type Snapshot = { config: Record<string, any>; version: string; requires_restart: boolean }
const editorMode = ref('form'),
  rawJSON = ref('')
function changeMode(mode: string) {
  if (busy.value || mode === editorMode.value) return
  if (mode === 'json') rawJSON.value = JSON.stringify(draft.value, null, 2)
  else {
    try {
      const value = JSON.parse(rawJSON.value)
      if (!value || Array.isArray(value) || typeof value !== 'object') throw new Error()
      draft.value = value
      error.value = ''
    } catch {
      error.value = 'JSON 格式无效，请修正后切换'
      return
    }
  }
  editorMode.value = mode
}
const snapshot = ref<Snapshot>(),
  draft = ref<Record<string, any>>({}),
  original = ref(''),
  section = ref('embedding'),
  error = ref(''),
  message = ref(''),
  busy = ref(false),
  adding = ref(false),
  newName = ref(''),
  deleteName = ref('')
const dirty = computed(
  () =>
    !!snapshot.value &&
    (editorMode.value === 'json'
      ? rawJSON.value !== JSON.stringify(snapshot.value.config, null, 2)
      : JSON.stringify(draft.value) !== original.value),
)
watch(dirty, (value) => emit('dirty', value))
const titles: Record<string, string> = {
  embedding: '向量模型',
  rerank: '精排模型',
  parser: '文档解析',
  mysql: '数据库',
  es: 'Elasticsearch',
  http: 'HTTP 服务',
  redis: 'Redis',
  search: '检索策略',
  security: '安全配置',
  evaluation: '检索评测 / MiniMax',
  general: '基础设置',
}
const groups = computed(() => [
  'general',
  ...Object.keys(draft.value).filter(
    (key) => typeof draft.value[key] === 'object' && draft.value[key] !== null,
  ),
])
const active = computed(() =>
  section.value === 'general' ? draft.value : draft.value[section.value] || {},
)
const models = computed<Record<string, any> | undefined>(() => active.value?.models)
async function load() {
  if (dirty.value && !window.confirm('重新加载将丢弃未保存修改，继续吗？')) return
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    snapshot.value = await adminRequest<Snapshot>('config')
    draft.value = JSON.parse(JSON.stringify(snapshot.value.config))
    if (!draft.value.evaluation)
      draft.value.evaluation = {
        enabled: false,
        data_dir: 'evaluation/datasets',
        run_dir: 'evaluation/runs',
        search_base_url: 'http://127.0.0.1:8080',
        scorer_base_url: 'http://127.0.0.1:18080',
        request_timeout_seconds: 240,
        run_timeout_seconds: 3600,
      }
    if (!draft.value.evaluation.judge)
      draft.value.evaluation.judge = {
        base_url: 'https://api.minimax.io/v1',
        model: '',
        api_key: '',
      }
    if (draft.value.evaluation.auto_generate === undefined)
      draft.value.evaluation.auto_generate = false
    if (draft.value.evaluation.auto_sample_count === undefined)
      draft.value.evaluation.auto_sample_count = 50
    original.value = JSON.stringify(draft.value)
    rawJSON.value = JSON.stringify(draft.value, null, 2)
    if (!groups.value.includes(section.value)) section.value = 'general'
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function save() {
  if (editorMode.value === 'json') {
    try {
      const value = JSON.parse(rawJSON.value)
      if (!value || Array.isArray(value) || typeof value !== 'object') throw new Error()
      draft.value = value
    } catch {
      error.value = 'JSON 格式无效，无法保存'
      return
    }
  }
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    const result = await adminRequest<Snapshot>('config', {
      version: snapshot.value?.version,
      config: draft.value,
    })
    snapshot.value = result
    draft.value = JSON.parse(JSON.stringify(result.config))
    original.value = JSON.stringify(draft.value)
    rawJSON.value = JSON.stringify(draft.value, null, 2)
    message.value =
      '配置已保存。评测模型的地址、名称和 API Key 下次评分时生效；其他配置当前服务仍使用启动时的配置，请手动重启后生效。'
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
function addModel() {
  const name = newName.value.trim()
  if (!name || models.value?.[name]) {
    error.value = '名称不能为空或与已有模型重复'
    return
  }
  const first = Object.values(models.value || {})[0]
  const model = first
    ? JSON.parse(JSON.stringify(first))
    : { protocol: 'openai', url: '', model: '', api_key: '' }
  for (const key of Object.keys(model)) {
    if (/password|secret|api_key|token/i.test(key)) model[key] = ''
  }
  model.model = name
  if (models.value) models.value[name] = model
  adding.value = false
  newName.value = ''
  error.value = ''
}
function remove() {
  if (models.value) delete models.value[deleteName.value]
  deleteName.value = ''
}
function unload(event: BeforeUnloadEvent) {
  if (dirty.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}
window.addEventListener('beforeunload', unload)
onBeforeUnmount(() => window.removeEventListener('beforeunload', unload))
function lock() {
  snapshot.value = undefined
  draft.value = {}
  original.value = ''
  emit('dirty', false)
}
</script>
<template>
  <section class="config-page">
    <div class="page-heading">
      <div>
        <h1>配置，让服务适合你。</h1>
        <p>管理模型与服务连接，所有保存将在重启后生效。</p>
      </div>
    </div>
    <AdminGate @unlock="load" @lock="lock"
      ><div class="config-workspace">
        <nav class="config-nav" aria-label="配置分组">
          <button
            v-for="key in groups"
            :key="key"
            :class="{ active: section === key }"
            @click="section = key"
          >
            {{ titles[key] || key }}<span>{{ key }}</span>
          </button>
        </nav>
        <div class="config-content">
          <div class="panel-toolbar">
            <h2><Settings2 :size="17" />{{ titles[section] || section }}</h2>
            <button class="button secondary compact" :disabled="busy" @click="load">
              <RefreshCw :size="14" />重新加载
            </button>
          </div>
          <Notice :message="error" /><Notice :message="message" success />
          <div v-if="!snapshot" class="empty-state">
            <p>{{ busy ? '正在读取配置…' : '请重新加载配置' }}</p>
          </div>
          <template v-else
            ><div class="config-editor-tabs segmented">
              <button
                :disabled="busy"
                :class="{ active: editorMode === 'form' }"
                @click="changeMode('form')"
              >
                分组编辑</button
              ><button
                :disabled="busy"
                :class="{ active: editorMode === 'json' }"
                @click="changeMode('json')"
              >
                完整 JSON
              </button>
            </div>
            <div v-if="editorMode === 'json'" class="config-json">
              <p>
                可新增、修改、删除配置字段或模型；保存前会按服务配置结构校验。已有凭据的圆点占位值表示保留。
              </p>
              <textarea
                v-model="rawJSON"
                :disabled="busy"
                aria-label="完整 JSON 配置"
                rows="24"
                spellcheck="false"
              /><small>请勿删除启动必需字段。未识别字段将被拒绝，不会静默丢弃。</small>
            </div>
            <fieldset v-else :disabled="busy">
              <ConfigFields
                :value="active"
                :exclude="['models']"
                :scalar-only="section === 'general'"
              /><template v-if="models"
                ><div class="model-toolbar">
                  <h3>
                    模型列表 <span class="count">{{ Object.keys(models).length }}</span>
                  </h3>
                  <button class="button secondary compact" @click="adding = true">
                    <Plus :size="15" />新增模型
                  </button>
                </div>
                <details v-for="(model, name) in models" :key="name" class="model-config" open>
                  <summary>
                    <strong>{{ name }}</strong><span v-if="active.default === name" class="tag"
                      >默认</span
                    ><button
                      class="icon-button danger-text"
                      :aria-label="`删除模型 ${name}`"
                      @click.prevent="deleteName = String(name)"
                    >
                      <Trash2 :size="16" />
                    </button>
                  </summary>
                  <ConfigFields :value="model" /></details
              ></template>
            </fieldset>
            <div class="config-savebar">
              <span
                >{{ dirty ? '有未保存的修改' : '配置与磁盘文件一致'
                }}<small>保存不会自动重启服务</small></span
              ><button class="button" :disabled="busy || !dirty" @click="save">
                <Save :size="16" />{{ busy ? '正在保存…' : '保存配置' }}
              </button>
            </div></template
          >
        </div>
      </div>
      <Modal v-if="adding" title="新增模型" @close="adding = false"
        ><form @submit.prevent="addModel">
          <label
            >模型配置名称<input
              v-model="newName"
              required
              maxlength="64"
              placeholder="例如 local-bge"
          /></label>
          <p class="muted">
            创建后填写端点、协议和凭据。需要设为默认时，同步修改上方默认模型名称。
          </p>
          <div class="form-actions">
            <button type="button" class="button secondary" @click="adding = false">取消</button
            ><button class="button">添加到配置</button>
          </div>
        </form></Modal
      ><Modal v-if="deleteName" title="删除模型配置" @close="deleteName = ''"
        ><p>
          将从配置草稿中删除 {{ deleteName }}。删除后需保存配置；如果它是默认模型，请先更换默认值。
        </p>
        <div class="form-actions">
          <button class="button secondary" @click="deleteName = ''">取消</button
          ><button class="button danger" @click="remove">确认删除配置</button>
        </div></Modal
      ></AdminGate
    >
  </section>
</template>
