<script setup lang="ts">
/** Generic modal dialog (backdrop click / Escape closes). */
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import { t } from "../lib/i18n";
import { IconClose } from "./Icons";

const props = defineProps<{
  open: boolean;
  title: string;
}>();

const emit = defineEmits<{ (e: "close"): void }>();

const panel = ref<HTMLElement | null>(null);

function onKey(e: KeyboardEvent): void {
  if (e.key === "Escape" && props.open) emit("close");
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      window.addEventListener("keydown", onKey);
      await nextTick();
      panel.value?.focus();
    } else {
      window.removeEventListener("keydown", onKey);
    }
  },
);

onBeforeUnmount(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="modal-backdrop" @click.self="emit('close')">
        <div
          ref="panel"
          class="modal"
          role="dialog"
          aria-modal="true"
          :aria-label="title"
          tabindex="-1"
        >
          <header class="modal-head">
            <h3>{{ title }}</h3>
            <button class="btn-icon" :aria-label="t('common.close')" @click="emit('close')">
              <IconClose :size="15" />
            </button>
          </header>
          <div class="modal-body">
            <slot />
          </div>
          <footer class="modal-actions">
            <slot name="actions" />
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
