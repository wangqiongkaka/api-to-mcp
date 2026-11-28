<script setup lang="ts">
import { ref } from "vue";
import type { GrafanaConnection } from "./types";

const props = defineProps<{
  connections: GrafanaConnection[];
  allowedOrigins: string[];
}>();

const emit = defineEmits<{
  (e: "refresh"): void;
  (e: "notify", message: string): void;
  (e: "error", message: string): void;
}>();

const authLabels: Record<GrafanaConnection["authType"], string> = {
  bearer: "Bearer Token",
  basic: "Basic Auth",
  cookie: "Cookie",
};

const editingId = ref<number | null>(null);
const showForm = ref(false);
const busy = ref(false);
const testingId = ref<number | null>(null);

const name = ref("");
const baseUrl = ref("");
const authType = ref<GrafanaConnection["authType"]>("bearer");
const token = ref("");
const username = ref("");
const password = ref("");
const cookie = ref("");

function openCreate() {
  editingId.value = null;
  name.value = "";
  baseUrl.value = "";
  authType.value = "bearer";
  token.value = "";
  username.value = "";
  password.value = "";
  cookie.value = "";
  showForm.value = true;
  emit("error", "");
}

function openEdit(conn: GrafanaConnection) {
  editingId.value = conn.id;
  name.value = conn.name;
  baseUrl.value = conn.baseUrl;
  authType.value = conn.authType;
  token.value = "";
  username.value = conn.username ?? "";
  password.value = "";
  cookie.value = "";
  showForm.value = true;
  emit("error", "");
}

function cancelForm() {
  showForm.value = false;
  editingId.value = null;
  token.value = "";
  password.value = "";
  cookie.value = "";
}

async function requestJSON<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api${path}`, {
    credentials: "same-origin",
    ...options,
    headers: { "Content-Type": "application/json", ...options.headers },
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || `请求失败（${response.status}）`);
  }
  return data as T;
}

async function saveConnection() {
  busy.value = true;
  emit("error", "");
  try {
    const payload: Record<string, string> = {
      name: name.value.trim(),
      baseUrl: baseUrl.value.trim(),
      authType: authType.value,
    };
    if (authType.value === "bearer") payload.token = token.value;
    if (authType.value === "basic") {
      payload.username = username.value;
      payload.password = password.value;
    }
    if (authType.value === "cookie") payload.cookie = cookie.value;
    const isEdit = editingId.value !== null;
    await requestJSON<GrafanaConnection>(
      isEdit ? `/grafana/connections/${editingId.value}` : "/grafana/connections",
      { method: isEdit ? "PUT" : "POST", body: JSON.stringify(payload) },
    );
    cancelForm();
    emit("refresh");
    emit("notify", isEdit ? "Grafana 连接已更新" : "Grafana 连接已创建");
  } catch (e) {
    emit("error", e instanceof Error ? e.message : "保存失败");
  } finally {
    busy.value = false;
  }
}

async function testConnection(conn: GrafanaConnection) {
  testingId.value = conn.id;
  emit("error", "");
  try {
    const res = await requestJSON<{ ok: boolean; datasourceCount: number }>(
      `/grafana/connections/${conn.id}/test`,
      { method: "POST", body: "{}" },
    );
    emit("notify", `连接 ${conn.name} 成功，检测到 ${res.datasourceCount} 个数据源`);
  } catch (e) {
    emit("error", e instanceof Error ? e.message : "连接测试失败");
  } finally {
    testingId.value = null;
  }
}

async function deleteConnection(conn: GrafanaConnection) {
  if (!window.confirm(`确定删除 Grafana 连接「${conn.name}」吗？`)) return;
  emit("error", "");
  try {
    await requestJSON(`/grafana/connections/${conn.id}`, { method: "DELETE" });
    if (editingId.value === conn.id) cancelForm();
    emit("refresh");
    emit("notify", "Grafana 连接已删除");
  } catch (e) {
    emit("error", e instanceof Error ? e.message : "删除失败");
  }
}

function date(value: string) {
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
</script>

<template>
  <header class="page-header">
    <div>
      <div class="breadcrumb">管理 / 数据源连接</div>
      <h1>
        Grafana 连接<span class="heading-count">{{ connections.length }}</span>
      </h1>
      <p>统一维护 Grafana 实例地址与认证凭据。所有成员建工具时可选用，凭据加密保存且不向成员暴露。</p>
    </div>
    <button class="button primary" @click="openCreate">
      <span aria-hidden="true">＋</span>新建连接
    </button>
  </header>

  <form v-if="showForm" class="panel grafana-conn-form" @submit.prevent="saveConnection">
    <div class="panel-heading">
      <h2>{{ editingId === null ? "新建 Grafana 连接" : "编辑 Grafana 连接" }}</h2>
    </div>
    <div class="form-content">
      <div class="grafana-conn-grid">
        <label>
          连接名称
          <input v-model="name" required maxlength="80" placeholder="例如：生产 Grafana" />
        </label>
        <label>
          认证方式
          <select v-model="authType">
            <option value="bearer">Service Account Token / API Key (Bearer)</option>
            <option value="basic">Basic Auth（用户名 + 密码）</option>
            <option value="cookie">Cookie</option>
          </select>
        </label>
      </div>
      <label>
        Grafana 地址
        <input
          v-model="baseUrl"
          type="url"
          required
          maxlength="2048"
          placeholder="https://grafana.example.com"
        />
        <span v-if="allowedOrigins.length" class="field-help">
          仅允许已配置在 UPSTREAM_ORIGINS 中的源地址：{{ allowedOrigins.join("、") }}
        </span>
      </label>

      <label v-if="authType === 'bearer'">
        Token（Service Account Token / API Key）
        <input
          v-model="token"
          type="password"
          :required="editingId === null"
          autocomplete="off"
          :placeholder="editingId === null ? '输入 glsa_... 或 API Token' : '留空则保持当前 Token 不变'"
        />
      </label>

      <div v-else-if="authType === 'basic'" class="grafana-conn-grid">
        <label>
          用户名
          <input
            v-model="username"
            required
            maxlength="256"
            autocomplete="off"
            placeholder="Grafana 用户名"
          />
        </label>
        <label>
          密码
          <input
            v-model="password"
            type="password"
            :required="editingId === null"
            autocomplete="off"
            :placeholder="editingId === null ? '输入密码' : '留空则保持当前密码不变'"
          />
        </label>
      </div>

      <label v-else>
        Cookie 请求头
        <input
          v-model="cookie"
          type="password"
          :required="editingId === null"
          autocomplete="off"
          :placeholder="editingId === null ? '例如：grafana_session=...' : '留空则保持当前 Cookie 不变'"
        />
      </label>

      <div class="button-row">
        <button class="button primary" :disabled="busy">
          {{ busy ? "正在保存…" : "保存连接" }}
        </button>
        <button type="button" class="button" :disabled="busy" @click="cancelForm">取消</button>
      </div>
    </div>
  </form>

  <section class="panel table-wrap">
    <table>
      <thead>
        <tr>
          <th>名称</th>
          <th>Grafana 地址</th>
          <th>认证方式</th>
          <th>最近更新</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="c in connections" :key="c.id">
          <td>
            <strong>{{ c.name }}</strong>
            <div v-if="c.authType === 'basic' && c.username" class="muted">
              用户：{{ c.username }}
            </div>
          </td>
          <td>
            <code class="row-url">{{ c.baseUrl }}</code>
          </td>
          <td>
            <span class="badge neutral">{{ authLabels[c.authType] }}</span>
          </td>
          <td class="muted nowrap">{{ date(c.updatedAt) }}</td>
          <td class="nowrap">
            <button
              class="text-button"
              :disabled="testingId === c.id"
              @click="testConnection(c)"
            >
              {{ testingId === c.id ? "测试中…" : "测试连接" }}
            </button>
            <button class="text-button" @click="openEdit(c)">编辑</button>
            <button class="text-button danger-text" @click="deleteConnection(c)">删除</button>
          </td>
        </tr>
        <tr v-if="!connections.length">
          <td colspan="5" class="no-results">
            尚未配置 Grafana 连接。点击右上角「新建连接」后，成员即可在接口库创建 Grafana 查询工具。
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
