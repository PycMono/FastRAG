<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
const props = defineProps<{ content: string }>()
const html = computed(() =>
  DOMPurify.sanitize(marked.parse(props.content, { async: false }) as string, {
    FORBID_TAGS: ['img', 'video', 'audio', 'iframe', 'form', 'input', 'style'],
    FORBID_ATTR: ['style', 'id', 'name'],
  }),
)
</script>
<template><div class="markdown-content" v-html="html"></div></template>
