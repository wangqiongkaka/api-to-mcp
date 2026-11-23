<script setup lang="ts">
defineProps<{ label: string; text: string }>();
</script>

<template>
  <span class="help-hint">
    <button type="button" :aria-label="label" :aria-description="text">?</button>
    <span role="tooltip">{{ text }}</span>
  </span>
</template>

<style scoped>
.help-hint { display: inline-flex; position: relative; vertical-align: middle; }
button { display: inline-grid; place-items: center; width: 16px; height: 16px; min-height: 0; font: 600 11px/1 system-ui; padding: 0; border: 1px solid var(--border); border-radius: 50%; background: transparent; color: var(--muted); cursor: help; }
[role="tooltip"] { display: none; position: absolute; top: 100%; left: -24px; z-index: 4; width: min(230px, 58vw); padding: 8px 12px; border-radius: 6px; background: #202d45; color: white; font: 400 12px/1.6 system-ui; text-align: left; white-space: normal; }
.help-hint:hover [role="tooltip"], .help-hint:focus-within [role="tooltip"] { display: block; }
</style>
