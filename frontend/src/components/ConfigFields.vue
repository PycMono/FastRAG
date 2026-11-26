<script setup lang="ts">
import { computed, ref } from 'vue'
defineOptions({ name: 'ConfigFields' })
const props = defineProps<{
  value: Record<string, any>
  path?: string
  exclude?: string[]
  scalarOnly?: boolean
}>()
const error = ref('')
const fields = computed(() =>
  Object.fromEntries(
    Object.entries(props.value).filter(
      ([key, item]) =>
        !props.exclude?.includes(key) && (!props.scalarOnly || typeof item !== 'object'),
    ),
  ),
)
const labels: Record<string, string> = {
  bm25_top: '关键词召回上限',
  knn_top: '向量召回上限',
  num_candidates: '向量近似搜索候选池',
  rank_constant: 'RRF 排名常数',
  dense_weight: '默认语义检索权重',
  default_retrieve_count: '默认精排候选预算',
  default: '默认模型',
  models: '模型列表',
  host: '服务地址',
  port: '端口',
  password: '密码',
  api_key: 'API Key',
  url: '服务端点',
  model: '模型名称',
  protocol: '接口协议',
  dim: '向量维度',
  batch_size: '批量大小',
  timeout_ms: '超时（毫秒）',
  query_prefix: '查询前缀',
  user: '用户名',
  database: '数据库名称',
  index: '索引名称',
  addrs: '连接地址',
  username: '用户名',
  enabled: '启用',
  trust_request_tenant: '信任调用方租户字段',
  top_n: '精排数量',
  max_open: '最大连接数',
  max_idle: '空闲连接数',
  debug: '调试模式',
  app: '应用名称',
  judge: '评估模型（MiniMax / OpenAI 兼容）',
  auto_generate: '导入后自动生成待审核测试题（消耗模型额度）',
  auto_sample_count: '每篇文档自动取样数量（短文可能不足）',
  retention_days: '评测任务保留天数（超期任务每天清理）',
  base_url: 'API Base URL（需与 API Key 所属平台一致）',
  scorer_base_url: '内部 Ragas 评分服务地址',
  request_timeout_seconds: '评分请求超时（秒）',
  run_timeout_seconds: '整个评测超时（秒）',
  environment: '评测环境标识',
  git_commit: '被测代码版本',
}
function isSecret(key: string) {
  return /password|secret|token|api_key/i.test(key)
}
function updateArray(key: string, event: Event) {
  props.value[key] = (event.target as HTMLTextAreaElement).value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}
</script>
<template>
  <div class="config-fields">
    <template v-for="(item, key) in fields" :key="key"
      ><details
        v-if="item && typeof item === 'object' && !Array.isArray(item)"
        class="config-nested"
        open
      >
        <summary>
          {{ labels[key] || key }}<code>{{ key }}</code>
        </summary>
        <ConfigFields :value="item" :path="`${path || ''}.${key}`" />
      </details>
      <label v-else-if="typeof item === 'boolean'" class="config-switch"
        ><span
          >{{ labels[key] || key }}<small>{{ key }}</small></span
        ><input v-model="value[key]" type="checkbox" role="switch" /></label
      ><label v-else class="config-field"
        ><span
          >{{ labels[key] || key }}<code>{{ key }}</code></span
        ><textarea
          v-if="Array.isArray(item)"
          :value="item.join('\n')"
          rows="3"
          @change="updateArray(key, $event)"
        /><input
          v-else-if="typeof item === 'number'"
          v-model.number="value[key]"
          type="number"
        /><input
          v-else
          v-model="value[key]"
          :type="isSecret(key) ? 'password' : 'text'"
          autocomplete="off"
          :placeholder="isSecret(key) ? '留空表示清除，圆点表示保留原值' : ''"
        /><small v-if="isSecret(key)"
          >圆点表示保留已保存的凭据；填写新值替换，清空则删除凭据。</small
        ></label
      ></template
    >
    <p v-if="error" role="alert" class="danger-text">{{ error }}</p>
  </div>
</template>
