<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { X } from '@lucide/vue'
defineProps<{ title: string; wide?: boolean; locked?: boolean }>()
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement>()
let previous: HTMLElement | null = null
function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    emit('close')
  }
  if (event.key !== 'Tab') return
  const nodes = Array.from(
    panel.value?.querySelectorAll<HTMLElement>(
      'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]',
    ) || [],
  ).filter((el) => el.getClientRects().length)
  const first = nodes[0]
  const last = nodes.at(-1)
  if (!first) {
    event.preventDefault()
    panel.value?.focus()
    return
  }
  if (
    event.shiftKey &&
    (document.activeElement === first || document.activeElement === panel.value)
  ) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}
onMounted(() => {
  previous = document.activeElement as HTMLElement
  document.body.style.overflow = 'hidden'
  panel.value?.focus()
  document.addEventListener('keydown', keydown)
})
onBeforeUnmount(() => {
  document.body.style.overflow = ''
  document.removeEventListener('keydown', keydown)
  previous?.focus()
})
</script>
<template>
  <Teleport to="body"
    ><div class="overlay" @mousedown.self="!locked && $emit('close')">
      <section
        ref="panel"
        :class="['modal', { drawer: wide }]"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        tabindex="-1"
      >
        <header class="modal-header">
          <h2>{{ title }}</h2>
          <button class="icon-button" :disabled="locked" aria-label="关闭" @click="$emit('close')">
            <X :size="20" />
          </button>
        </header>
        <div class="modal-body"><slot /></div>
      </section></div
  ></Teleport>
</template>
