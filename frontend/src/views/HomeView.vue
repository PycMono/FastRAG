<script setup lang="ts">
import { ArrowUpRight, FileText, Search, Settings2, Activity, Layers, Check } from '@lucide/vue'
defineEmits<{ navigate: [page: string] }>()
</script>
<template>
  <section class="home-page">
    <div class="home-hero">
      <span class="hero-product">FastRAG</span>
      <h1>知识就位。<br />答案，更进一步。</h1>
      <p>
        将文档变成可查找的知识。<br class="mobile-break" />从整理内容，到找到来源，一切井然有序。
      </p>
      <div class="hero-actions">
        <button class="button" @click="$emit('navigate', 'import')">导入你的文档</button
        ><button class="hero-link" @click="$emit('navigate', 'search')">
          开始检索<ArrowUpRight :size="19" />
        </button>
      </div>
    </div>
    <div class="knowledge-stage" aria-label="文档转为可检索知识的能力示意">
      <div class="stage-caption">从一份文档，到每一次有据可循的查找。<small>功能示意</small></div>
      <div class="stage-flow">
        <div class="stage-document">
          <div class="stage-doc-icon"><FileText :size="32" /></div>
          <span>你的文档</span>
          <h3>让内容，<br />保持完整。</h3>
          <div class="stage-lines"><i></i><i></i><i></i><i></i></div>
          <span class="stage-pill">Markdown / TXT / 在线文档</span>
        </div>
        <div class="stage-connector"><span></span><Layers :size="23" /><span></span></div>
        <div class="stage-result">
          <div class="stage-search"><Search :size="19" /><span>从知识库中查找…</span></div>
          <span class="stage-result-label">相关内容与原文，始终相连</span>
          <h3>查得到。<br />也找得到出处。</h3>
          <div class="stage-source">
            <span><Check :size="16" />原文溯源</span><span><Layers :size="16" />结构化片段</span>
          </div>
        </div>
      </div>
    </div>
    <div class="home-intro">
      <span>一个工作空间，贯穿整个流程。</span>
      <h2>内容、配置、运行状态。<br />都在掌握之中。</h2>
    </div>
    <div class="home-features">
      <button @click="$emit('navigate', 'documents')">
        <FileText :size="26" />
        <h3>你的知识，一目了然。</h3>
        <p>浏览文档、阅读原文，查看每一个知识片段。</p>
        <span>管理文档<ArrowUpRight :size="15" /></span></button
      ><button @click="$emit('navigate', 'config')">
        <Settings2 :size="26" />
        <h3>适合你的，才是好配置。</h3>
        <p>管理模型和服务连接，保存前校验，修改更从容。</p>
        <span>管理配置<ArrowUpRight :size="15" /></span></button
      ><button @click="$emit('navigate', 'logs')">
        <Activity :size="26" />
        <h3>服务运行，心中有数。</h3>
        <p>查看 INFO 与错误日志，按关键词追踪服务事件。</p>
        <span>查看日志<ArrowUpRight :size="15" /></span>
      </button>
    </div>
  </section>
</template>
