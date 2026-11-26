<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()
const confirmButton = ref<HTMLButtonElement>()
const cancelButton = ref<HTMLButtonElement>()

function onKey(event: KeyboardEvent) {
  if (props.open && event.key === 'Escape') emit('cancel')
}
watch(
  () => props.open,
  (open) => {
    if (open)
      requestAnimationFrame(() =>
        (props.danger ? cancelButton : confirmButton).value?.focus(),
      )
  },
)
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <Transition name="confirm">
      <div v-if="open" class="confirm-backdrop" role="presentation" @click.self="$emit('cancel')">
        <div class="confirm-card" role="dialog" aria-modal="true" :aria-label="title">
          <h3>{{ title }}</h3>
          <p>{{ message }}</p>
          <div class="confirm-actions">
            <button ref="cancelButton" type="button" class="confirm-cancel" @click="$emit('cancel')">
              {{ cancelText || '取消' }}
            </button>
            <button
              ref="confirmButton"
              type="button"
              :class="['confirm-ok', { danger }]"
              @click="$emit('confirm')"
            >
              {{ confirmText || '确定' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.confirm-backdrop {
  position: fixed;
  inset: 0;
  z-index: 1000;
  background: rgba(0, 0, 0, 0.18);
  backdrop-filter: blur(6px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.confirm-card {
  width: min(420px, 100%);
  background: #fff;
  border-radius: 24px;
  padding: 26px;
  box-shadow: 0 24px 80px rgba(0, 0, 0, 0.18);
  animation: confirm-in 0.2s ease both;
}
.confirm-card h3 {
  margin: 0 0 8px;
  font-size: 20px;
  letter-spacing: -0.3px;
}
.confirm-card p {
  margin: 0;
  color: #6e6e73;
  line-height: 1.6;
  font-size: 14px;
}
.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 22px;
}
.confirm-cancel,
.confirm-ok {
  border-radius: 999px;
  padding: 10px 18px;
  font: inherit;
  font-size: 14px;
  cursor: pointer;
  transition:
    background 0.2s ease,
    transform 0.1s ease;
}
.confirm-cancel {
  border: 1px solid #d2d2d7;
  background: #fff;
  color: #1d1d1f;
}
.confirm-ok {
  border: 0;
  background: #0071e3;
  color: #fff;
}
.confirm-ok.danger {
  background: #d70015;
}
.confirm-cancel:hover {
  background: #f5f5f7;
}
.confirm-ok:hover {
  background: #0077ed;
}
.confirm-ok.danger:hover {
  background: #e3001b;
}
.confirm-cancel:active,
.confirm-ok:active {
  transform: scale(0.98);
}
.confirm-enter-active,
.confirm-leave-active {
  transition: opacity 0.18s ease;
}
.confirm-enter-from,
.confirm-leave-to {
  opacity: 0;
}
@keyframes confirm-in {
  from {
    opacity: 0;
    transform: translateY(8px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .confirm-card {
    animation: none;
  }
  .confirm-enter-active,
  .confirm-leave-active {
    transition: none;
  }
}
</style>
