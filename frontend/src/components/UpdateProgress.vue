<script setup lang="ts">
/**
 * Live self-update progress chip for the header (issue #41).
 * Visible from every view while the updater is downloading or installing —
 * previously progress was only rendered inside Settings → Updates (and the
 * app:update events never even reached the webview, see the forwarding fix).
 */
import { computed } from "vue";
import { store } from "../store";
import { t } from "../lib/i18n";
import { IconDownload } from "./Icons";

const upd = computed(() => store.appUpdate);
const active = computed(
  () => upd.value?.state === "downloading" || upd.value?.state === "restarting",
);
const percent = computed(() =>
  upd.value?.state === "restarting" ? 100 : Math.min(100, Math.max(2, upd.value?.percent ?? 0)),
);
</script>

<template>
  <div v-if="active" class="update-chip" :title="upd?.detail || t('update.state.downloading')">
    <IconDownload :size="12" class="spin-slow" />
    <div class="update-chip-body">
      <span class="update-chip-label">
        <template v-if="upd?.state === 'restarting'">{{ t("update.restarting") }}</template>
        <template v-else>{{ t("update.state.downloading") }} {{ Math.round(upd?.percent ?? 0) }}%</template>
      </span>
      <div class="progress thin" :class="{ indeterminate: upd?.state === 'restarting' }">
        <div class="bar" :style="{ width: `${percent}%` }" />
      </div>
    </div>
  </div>
</template>
