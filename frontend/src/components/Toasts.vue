<script setup lang="ts">
/** Top-right toast region. */
import { toasts, dismissToast } from "../lib/toast";
import { t } from "../lib/i18n";
import { IconClose } from "./Icons";
</script>

<template>
  <div class="toast-region" aria-live="polite">
    <TransitionGroup name="toast">
      <div v-for="item in toasts" :key="item.id" class="toast" :class="item.type" role="status">
        <span class="t-bar" />
        <span class="t-msg">{{ item.msg }}</span>
        <button class="t-close btn-icon" :aria-label="t('common.close')" @click="dismissToast(item.id)">
          <IconClose :size="13" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
