<script setup lang="ts">
/** Share modal (#102): node share link + QR + wg-quick download.
 * v2rayN/Qv2ray/V2RayW parity: share any node as a QR code or link. */
import { ref } from "vue";
import { Clipboard } from "@wailsio/runtime";
import { t } from "../lib/i18n";
import { toast } from "../lib/toast";
import Modal from "./Modal.vue";
import { IconShare, IconDownload, IconCheck } from "./Icons";
import type { ShareInfo } from "../../bindings/github.com/neurorift/neurorift/internal/profile/models.js";

const props = defineProps<{
  open: boolean;
  info: ShareInfo | null;
}>();

const emit = defineEmits<{ (e: "close"): void }>();

const copied = ref(false);

async function copyText(): Promise<void> {
  if (!props.info) return;
  try {
    await Clipboard.SetText(props.info.text);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1600);
    toast.success(t("share.copied"));
  } catch {
    toast.error(t("share.copyFailed"));
  }
}

function downloadConf(): void {
  if (!props.info) return;
  const blob = new Blob([props.info.text], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = (props.info.name || "wireguard").replace(/[\\/:*?"<>|]/g, "_") + ".conf";
  a.click();
  URL.revokeObjectURL(url);
}
</script>

<template>
  <Modal :open="open" :title="t('share.title')" @close="emit('close')">
    <div v-if="info" class="share-body">
      <div class="share-meta">
        <span class="badge violet">{{ info.type }}</span>
        <strong class="share-name">{{ info.name }}</strong>
      </div>
      <div class="share-qr-wrap">
        <img v-if="info.qr" class="share-qr" :src="info.qr" alt="QR code" />
        <p v-else class="dim small">{{ t("share.noQR") }}</p>
      </div>
      <textarea class="share-text" readonly rows="5" :value="info.text" @focus="($event.target as HTMLTextAreaElement).select()" />
      <p class="dim small">{{ info.kind === "wg-quick" ? t("share.wgHint") : t("share.qrHint") }}</p>
    </div>
    <template #actions>
      <button class="btn primary" @click="copyText()">
        <IconCheck v-if="copied" :size="14" />
        <IconShare v-else :size="14" />
        {{ t("share.copyLink") }}
      </button>
      <button v-if="info?.kind === 'wg-quick'" class="btn" @click="downloadConf()">
        <IconDownload :size="14" />
        {{ t("share.downloadConf") }}
      </button>
      <button class="btn ghost" @click="emit('close')">{{ t("common.close") }}</button>
    </template>
  </Modal>
</template>

<style scoped>
.share-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 320px;
  max-width: 420px;
}
.share-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}
.share-name {
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.share-qr-wrap {
  display: flex;
  justify-content: center;
}
.share-qr {
  width: 220px;
  height: 220px;
  border-radius: 10px;
  padding: 8px;
  background: #fff;
  image-rendering: pixelated;
}
.share-text {
  width: 100%;
  resize: none;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  line-height: 1.5;
  border-radius: 8px;
}
</style>
