<script setup lang="ts">
import { ref } from 'vue'
import { Copy, Check } from '@lucide/vue'
const props = withDefaults(defineProps<{ text: string; label?: string }>(), { label: '复制内容' })
const status = ref('')
async function copy() {
  try {
    await navigator.clipboard.writeText(props.text)
    status.value = '已复制'
  } catch {
    status.value = '复制失败，请手动选择内容'
  }
}
</script>
<template>
  <button type="button" class="text-button" @click="copy">
    <component :is="status === '已复制' ? Check : Copy" :size="14" />{{ status || label }}
  </button>
</template>
