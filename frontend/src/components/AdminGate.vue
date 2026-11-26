<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { LockKeyhole, ArrowRight } from '@lucide/vue'
import { adminRequest, setAdminToken, hasAdminToken } from '../admin'
import Notice from './Notice.vue'
const emit = defineEmits<{ unlock: []; lock: [] }>()
defineProps<{ quiet?: boolean }>()
const unlocked = ref(hasAdminToken()),
  token = ref(''),
  busy = ref(false),
  error = ref('')
async function unlock() {
  busy.value = true
  error.value = ''
  setAdminToken(token.value)
  try {
    await adminRequest('status')
    unlocked.value = true
    token.value = ''
    emit('unlock')
  } catch (e) {
    error.value = (e as Error).message
    setAdminToken('')
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  if (unlocked.value) emit('unlock')
})
function lock() {
  setAdminToken('')
  unlocked.value = false
  emit('lock')
}
</script>
<template>
  <div v-if="!unlocked" class="admin-gate">
    <span class="gate-icon"><LockKeyhole :size="27" /></span>
    <h2>管理访问</h2>
    <p>使用管理令牌查看配置和运行日志。</p>
    <form @submit.prevent="unlock">
      <label
        >管理令牌<input
          v-model="token"
          type="password"
          required
          autocomplete="off"
          placeholder="输入 FASTRAG_ADMIN_TOKEN" /></label
      ><Notice :message="error" /><button class="button" :disabled="busy">
        {{ busy ? '正在验证…' : '验证并继续' }}<ArrowRight :size="16" />
      </button>
    </form>
    <small
      >在服务启动环境设置至少 24 位的
      FASTRAG_ADMIN_TOKEN。令牌仅保存在当前页面内存，刷新后需重新输入。</small
    >
  </div>
  <template v-else
    ><div v-if="!quiet" class="admin-session">
      <span><LockKeyhole :size="13" />管理会话已解锁</span
      ><button class="text-button" @click="lock">锁定管理页面</button>
    </div>
    <slot
  /></template>
</template>
