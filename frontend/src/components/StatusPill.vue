<script setup lang="ts">
/** Engine status pill: colored dot + localized state label. */
import { computed } from "vue";
import { t } from "../lib/i18n";

const props = defineProps<{
  state: string;
  label?: string;
}>();

const cls = computed(() => `s-${props.state || "unknown"}`);
const text = computed(() => props.label ?? t(`state.${props.state || "unknown"}`));
</script>

<template>
  <span class="status-pill" :class="cls">
    <span class="dot" />
    {{ text }}
  </span>
</template>
