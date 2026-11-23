<script setup lang="ts">
import { AlertCircle, CheckCircle2 } from '@lucide/vue'
defineProps<{ message?: string; success?: boolean }>()
</script>
<template>
  <div v-if="message" :class="['notice', { success }]" :role="success ? 'status' : 'alert'">
    <component :is="success ? CheckCircle2 : AlertCircle" :size="18" /><span>{{ message }}</span>
  </div>
</template>
