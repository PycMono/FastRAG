<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'
defineProps<{ page: number; pages: number; total: number; busy?: boolean }>()
defineEmits<{ change: [page: number] }>()
</script>
<template>
  <div class="pagination">
    <span>共 {{ total }} 条记录</span>
    <div>
      <button
        class="icon-button"
        aria-label="上一页"
        :disabled="page <= 1 || busy"
        @click="$emit('change', page - 1)"
      >
        <ChevronLeft :size="16" /></button
      ><span>第 {{ page }} / {{ Math.max(1, pages) }} 页</span
      ><button
        class="icon-button"
        aria-label="下一页"
        :disabled="page >= pages || busy"
        @click="$emit('change', page + 1)"
      >
        <ChevronRight :size="16" />
      </button>
    </div>
  </div>
</template>
