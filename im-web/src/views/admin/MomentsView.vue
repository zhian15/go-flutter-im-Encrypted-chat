<template>
  <div class="page">
    <a-card>
      <div class="toolbar">
        <span class="title-text">朋友圈管理</span>
        <span class="muted" style="font-size:12px">屏蔽 = 仅发布者自己可见；删除 = 彻底移除（含评论）</span>
        <div style="flex:1"></div>
        <a-button type="primary" @click="showCreate = true">
          <template #icon><IconPlus /></template>
          以小助手身份发布
        </a-button>
        <a-button @click="load(1)">刷新</a-button>
      </div>

      <a-spin v-if="loading" style="display:block;text-align:center;padding:60px 0" />
      <template v-else>
        <div v-for="p in list" :key="p.id" class="post-card" :class="{ blocked: p.hidden }">
          <div class="post-head">
            <a-avatar :size="38" :image-url="p.senderAvatar || undefined" :style="!p.senderAvatar ? { background: peerColor(p.userId) } : {}">
              {{ firstChar(p.senderName) }}
            </a-avatar>
            <div class="post-head-info">
              <span class="post-name">
                {{ p.senderName || '未知' }}
                <a-tag v-if="p.assistant" color="orangered" size="small" class="official-tag">官方</a-tag>
                <a-tag v-if="p.hidden" color="red" size="small">已屏蔽（仅自己可见）</a-tag>
              </span>
              <span class="post-time">{{ p.createdAt }} · 动态ID {{ p.id }}</span>
            </div>
            <div class="post-ops">
              <a-popconfirm
                :content="p.hidden ? '取消屏蔽后所有人可见，确认？' : '屏蔽后仅发布者自己可见，确认？'"
                type="warning" @ok="toggleHidden(p)"
              >
                <a-button size="mini" :status="p.hidden ? 'normal' : 'warning'">{{ p.hidden ? '取消屏蔽' : '屏蔽' }}</a-button>
              </a-popconfirm>
              <a-popconfirm content="删除后不可恢复（含全部评论），确认删除？" type="warning" @ok="remove(p)">
                <a-button size="mini" status="danger">删除</a-button>
              </a-popconfirm>
            </div>
          </div>

          <div class="post-content">{{ p.content }}</div>
          <div v-if="p.images?.length" class="post-imgs">
            <a-image
              v-for="(img, i) in p.images" :key="i"
              :src="img" width="88" height="88" fit="cover" class="post-img"
              :preview-src-list="p.images"
            />
          </div>

          <!-- 点赞 -->
          <div class="post-social" v-if="p.likeCount > 0 || p.commentCount > 0">
            <div class="social-row" v-if="p.likeCount > 0">
              <span class="social-label">赞 · {{ p.likeCount }}</span>
              <span v-for="lk in p.likes" :key="lk.userId" class="like-user">
                <a-avatar :size="20" :image-url="lk.avatar || undefined" :style="!lk.avatar ? { background: peerColor(lk.userId) } : {}">
                  {{ firstChar(lk.name) }}
                </a-avatar>
                <span class="like-name">{{ lk.name || lk.userId }}</span>
              </span>
            </div>
            <div class="social-row" v-if="p.commentCount > 0">
              <span class="social-label">评论 · {{ p.commentCount }}</span>
              <div class="comment-list">
                <div v-for="cm in p.comments" :key="cm.id" class="comment-item">
                  <span class="comment-name">{{ cm.senderName || cm.userId }}：</span>
                  <span class="comment-text">{{ cm.content }}</span>
                  <span class="comment-time">{{ cm.createdAt }}</span>
                  <a-popconfirm content="删除该评论？" type="warning" @ok="removeComment(cm)">
                    <a-button size="mini" type="text" status="danger" class="comment-del">删除</a-button>
                  </a-popconfirm>
                </div>
              </div>
            </div>
          </div>
          <div v-else class="post-social muted-empty">暂无点赞与评论</div>
        </div>

        <a-empty v-if="!list.length" description="暂无朋友圈动态" />
        <div class="pager" v-if="total > 0">
          <a-pagination :current="page" :total="total" :page-size="pageSize" size="small" @page-change="load" />
        </div>
      </template>
    </a-card>

    <!-- 以小助手身份发布 -->
    <a-modal v-model:visible="showCreate" title="以小助手身份发布朋友圈" :confirm-loading="publishing" width="560" @ok="publish" okText="发布">
      <a-form layout="vertical">
        <a-form-item label="文字内容" required>
          <a-textarea v-model="newContent" placeholder="输入朋友圈文字（可与图片二选一，也可同时）" :max-length="2000" show-word-limit :auto-size="{ minRows: 3, maxRows: 6 }" />
        </a-form-item>
        <a-form-item label="图片（最多 9 张，选填）">
          <div class="img-grid">
            <div v-for="(img, i) in newImages" :key="i" class="img-slot">
              <img v-if="img" :src="img" class="img-prev" />
              <button v-if="img" type="button" class="img-remove" @click="newImages.splice(i, 1)"><IconClose /></button>
              <ImageUpload v-else :model-value="''" dir="moments/" :size="72" inline @update:model-value="(v: string) => onImg(i, v)" />
            </div>
            <ImageUpload v-if="newImages.length < 9" :model-value="''" dir="moments/" :size="72" inline hint="" @update:model-value="addImg" />
          </div>
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconPlus, IconClose } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
import ImageUpload from './ImageUpload.vue'

const list = ref<Array<Record<string, any>>>([])
const loading = ref(false)
const page = ref(1)
const pageSize = 10
const total = ref(0)

const PEER_COLORS = ['#4E8CFF', '#7B61FF', '#FF7D00', '#00B42A', '#F53F3F', '#14C9C9', '#9A73FF', '#FF57A2']
function peerColor(id: any) {
  const s = String(id || '')
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return { backgroundColor: PEER_COLORS[h % PEER_COLORS.length] }
}
function firstChar(s: string) {
  return (s || '?').trim().charAt(0).toUpperCase() || '?'
}

onMounted(() => load(1))

async function load(p = 1) {
  loading.value = true
  try {
    const { data } = await adminApi.moments({ page: p, size: pageSize })
    if (data.code === 0) {
      list.value = data.data?.list || []
      total.value = data.data?.total || 0
      page.value = p
    } else {
      Message.error(data.message || '加载失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '加载失败（网络错误）')
  } finally {
    loading.value = false
  }
}

async function toggleHidden(p: Record<string, any>) {
  try {
    const { data } = await adminApi.momentHidden(p.id, !p.hidden)
    if (data.code === 0) {
      p.hidden = !p.hidden
      Message.success(p.hidden ? '已屏蔽（仅发布者可见）' : '已取消屏蔽')
    } else {
      Message.error(data.message || '操作失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '操作失败（网络错误）')
  }
}

async function remove(p: Record<string, any>) {
  try {
    const { data } = await adminApi.momentDelete(p.id)
    if (data.code === 0) {
      Message.success('已删除')
      load(page.value)
    } else {
      Message.error(data.message || '删除失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '删除失败（网络错误）')
  }
}

async function removeComment(cm: Record<string, any>) {
  try {
    const { data } = await adminApi.momentCommentDelete(cm.id)
    if (data.code === 0) {
      Message.success('评论已删除')
      load(page.value)
    } else {
      Message.error(data.message || '删除失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '删除失败（网络错误）')
  }
}

// ===== 以小助手身份发布 =====
const showCreate = ref(false)
const publishing = ref(false)
const newContent = ref('')
const newImages = ref<string[]>([])

function addImg(v: string) {
  if (v) newImages.value.push(v)
}
function onImg(i: number, v: string) {
  if (v) newImages.value[i] = v
}

async function publish() {
  const content = newContent.value.trim()
  const images = newImages.value.filter(Boolean)
  if (!content && !images.length) {
    Message.error('文字和图片至少填一项')
    return
  }
  publishing.value = true
  try {
    const { data } = await adminApi.momentCreate({ content, images })
    if (data.code === 0) {
      Message.success('已发布（小助手身份）')
      showCreate.value = false
      newContent.value = ''
      newImages.value = []
      load(1)
    } else {
      Message.error(data.message || '发布失败')
    }
  } catch (e: any) {
    Message.error(e?.message || '发布失败（网络错误）')
  } finally {
    publishing.value = false
  }
}
</script>

<style scoped>
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.title-text { font-size: 16px; font-weight: var(--app-font-weight-medium); color: var(--app-text-1); }
.muted { color: var(--app-text-3); }

.post-card {
  border: 1px solid var(--app-border-2);
  border-radius: var(--app-radius-md);
  padding: 14px 16px;
  margin-bottom: 12px;
  background: var(--app-bg-card);
}
.post-card.blocked { background: var(--app-fill-1, #f7f8fa); }
.post-head { display: flex; align-items: center; gap: 10px; }
.post-head-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; flex: 1; }
.post-name { font-size: 14px; font-weight: var(--app-font-weight-medium); color: var(--app-text-1); display: flex; align-items: center; gap: 6px; }
.official-tag { transform: scale(0.9); }
.post-time { font-size: 11px; color: var(--app-text-3); }
.post-ops { display: flex; gap: 6px; flex-shrink: 0; }

.post-content { margin: 10px 0 0 48px; font-size: 14px; line-height: 1.6; color: var(--app-text-1); white-space: pre-wrap; word-break: break-word; }
.post-imgs { margin: 8px 0 0 48px; display: flex; gap: 6px; flex-wrap: wrap; }
.post-img { border-radius: var(--app-radius-sm); }

.post-social { margin: 10px 0 0 48px; border-top: 1px dashed var(--app-border-2); padding-top: 8px; display: flex; flex-direction: column; gap: 8px; }
.muted-empty { font-size: 12px; color: var(--app-text-4, #c9cdd4); }
.social-row { display: flex; align-items: flex-start; gap: 8px; font-size: 12px; }
.social-label { color: var(--app-primary); font-weight: var(--app-font-weight-medium); flex-shrink: 0; line-height: 20px; }
.like-user { display: inline-flex; align-items: center; gap: 4px; margin-right: 10px; }
.like-name { color: var(--app-text-2); }
.comment-list { display: flex; flex-direction: column; gap: 4px; flex: 1; min-width: 0; }
.comment-item { display: flex; align-items: baseline; gap: 4px; line-height: 1.6; flex-wrap: wrap; }
.comment-name { color: var(--app-primary); font-weight: var(--app-font-weight-medium); }
.comment-text { color: var(--app-text-1); word-break: break-word; }
.comment-time { color: var(--app-text-4, #c9cdd4); font-size: 11px; }
.comment-del { margin-left: 4px; padding: 0 4px; }

.pager { margin-top: 16px; display: flex; justify-content: center; }

.img-grid { display: flex; gap: 8px; flex-wrap: wrap; }
.img-slot { position: relative; width: 72px; height: 72px; }
.img-prev { width: 72px; height: 72px; object-fit: cover; border-radius: var(--app-radius-sm); border: 1px solid var(--app-border-2); }
.img-remove {
  position: absolute; top: -6px; right: -6px; width: 20px; height: 20px;
  border-radius: 50%; border: none; cursor: pointer;
  background: rgba(0,0,0,.55); color: #fff;
  display: flex; align-items: center; justify-content: center;
}
.img-remove :deep(svg) { width: 12px; height: 12px; }
</style>
