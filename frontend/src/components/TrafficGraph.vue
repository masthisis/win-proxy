<script setup lang="ts">
/** Custom SVG sparkline: last 60 s of down/up traffic (bytes/s). */
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    down: number[];
    up: number[];
    width?: number;
    height?: number;
  }>(),
  { width: 110, height: 32 },
);

const MAX_SAMPLES = 60;

function points(series: number[]): string {
  const n = series.length;
  if (n === 0) return "";
  const w = props.width;
  const h = props.height;
  const max = Math.max(1024, ...series); // floor of 1 KB/s keeps idle lines flat
  const step = w / (MAX_SAMPLES - 1);
  const pts: string[] = [];
  for (let i = 0; i < n; i++) {
    const v = series[i];
    const x = w - (n - 1 - i) * step;
    const y = h - 1 - (Math.min(v, max) / max) * (h - 3);
    pts.push(`${x.toFixed(1)},${y.toFixed(1)}`);
  }
  return pts.join(" ");
}

const downPts = computed(() => points(props.down));
const upPts = computed(() => points(props.up));

const downArea = computed(() => {
  if (!downPts.value) return "";
  const h = props.height;
  const first = downPts.value.split(" ")[0].split(",")[0];
  return `${first},${h} ${downPts.value} ${props.width},${h}`;
});
</script>

<template>
  <svg
    class="traffic-graph"
    :width="width"
    :height="height"
    :viewBox="`0 0 ${width} ${height}`"
    role="img"
    aria-hidden="true"
  >
    <polygon v-if="downArea" :points="downArea" fill="var(--success)" opacity="0.12" />
    <polyline v-if="downPts" :points="downPts" fill="none" stroke="var(--success)" stroke-width="1.4" />
    <polyline v-if="upPts" :points="upPts" fill="none" stroke="var(--accent-2)" stroke-width="1.4" opacity="0.85" />
  </svg>
</template>
