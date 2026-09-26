<template>
  <div class="placeholder-page">
    <h2>{{ title }}</h2>
    <p class="tip">功能开发中，计划见《企业IM-MVP开发任务清单》</p>
  </div>
</template>

<script setup lang="ts">
defineProps<{ title: string }>()
</script>

<style scoped>
.placeholder-page { background: #fff; border-radius: 12px; padding: 32px; }
.tip { color: #9aa3ae; font-size: 13px; }
</style>
