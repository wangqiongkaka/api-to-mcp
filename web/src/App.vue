<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import type { AccessToken, CallResult, Definition, Tool, User } from "./types";
import { definitionOf, emptyDefinition, podExample } from "./types";

const user = ref<User | null>(null);
const loading = ref(true);
const registering = ref(false);
const username = ref("");
const password = ref("");
const authBusy = ref(false);
const error = ref("");
const toast = ref("");
let toastTimer: ReturnType<typeof setTimeout>;
const view = ref<"tools" | "tokens" | "users">("tools");
const tools = ref<Tool[]>([]);
const tokens = ref<AccessToken[]>([]);
const users = ref<User[]>([]);
const allowedOrigins = ref<string[]>([]);
const search = ref("");
const filter = ref("all");
const editor = ref<Definition | null>(null);
const selectedId = ref<number | null>(null);
const baseline = ref("");
const saving = ref(false);
const publishing = ref(false);
const calling = ref(false);
const callArgs = ref("{}");
const result = ref<CallResult | null>(null);
const callError = ref("");
const tokenName = ref("");
const createdToken = ref("");
const tokenBusy = ref(false);
const preview = ref<"schema" | "openapi" | "higress">("schema");
const exported = ref("");
const published = computed(
  () => tools.value.find((t) => t.id === selectedId.value)?.published ?? false,
);
const dirty = computed(
  () =>
    editor.value !== null && JSON.stringify(editor.value) !== baseline.value,
);
const visibleTools = computed(() =>
  tools.value.filter(
    (t) =>
      (filter.value === "all" ||
        (filter.value === "published" ? t.published : !t.published)) &&
      `${t.name} ${t.description} ${t.url} ${t.ownerName}`
        .toLowerCase()
        .includes(search.value.toLowerCase()),
  ),
);
const publishedCount = computed(
  () => tools.value.filter((t) => t.published).length,
);
const schema = computed(() => {
  const properties: Record<string, unknown> = Object.create(null);
  const required: string[] = [];
  for (const p of editor.value?.parameters ?? []) {
    if (p.fixed || !p.name) continue;
    properties[p.name] = {
      type: p.type,
      description: p.description,
      ...(p.type === "array" ? { items: {} } : {}),
      ...(p.type === "integer"
        ? { minimum: -9007199254740991, maximum: 9007199254740991 }
        : {}),
    };
    if (p.required) required.push(p.name);
  }
  return { type: "object", properties, required, additionalProperties: false };
});
const previewText = computed(() =>
  preview.value === "schema"
    ? JSON.stringify(schema.value, null, 2)
    : exported.value,
);
const endpoint = `${window.location.origin}/mcp`;

function notify(message: string) {
  toast.value = message;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    toast.value = "";
  }, 4500);
}

async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api${path}`, {
    credentials: "same-origin",
    ...options,
    headers: { "Content-Type": "application/json", ...options.headers },
  });
  const data = await response.json();
  if (!response.ok) {
    if (response.status === 401 && !path.startsWith("/auth/")) {
      user.value = null;
      editor.value = null;
      password.value = "";
      createdToken.value = "";
    }
    throw new Error(data.error || `请求失败（${response.status}）`);
  }
  return data as T;
}

const message = (e: unknown) =>
  e instanceof Error ? e.message : "请求失败，请稍后重试";

async function refreshTools() {
  tools.value = await api<Tool[]>("/tools");
}
async function afterLogin() {
  await Promise.all([
    refreshTools(),
    api<{ allowedOrigins: string[] }>("/settings").then((s) => {
      allowedOrigins.value = s.allowedOrigins;
    }),
  ]);
}

async function authenticate() {
  error.value = "";
  authBusy.value = true;
  try {
    user.value = await api<User>(
      registering.value ? "/auth/register" : "/auth/login",
      {
        method: "POST",
        body: JSON.stringify({
          username: username.value,
          password: password.value,
        }),
      },
    );
    password.value = "";
    view.value = "tools";
    editor.value = null;
    await afterLogin();
  } catch (e) {
    error.value = message(e);
  } finally {
    authBusy.value = false;
  }
}

function canLeave() {
  if (saving.value) {
    notify("正在保存，请稍后再操作");
    return false;
  }
  return !dirty.value || window.confirm("有未保存的修改，确定离开吗？");
}

async function logout() {
  if (!canLeave()) return;
  try {
    await api("/auth/logout", { method: "POST", body: "{}" });
    user.value = null;
    editor.value = null;
    registering.value = false;
    createdToken.value = "";
    error.value = "";
  } catch (e) {
    error.value = message(e);
  }
}

async function navigate(next: typeof view.value) {
  if (!canLeave()) return;
  editor.value = null;
  view.value = next;
  error.value = "";
  createdToken.value = "";
  try {
    if (next === "tools") await refreshTools();
    if (next === "tokens") tokens.value = await api<AccessToken[]>("/tokens");
    if (next === "users") users.value = await api<User[]>("/users");
  } catch (e) {
    error.value = message(e);
  }
}

function openEditor(definition: Definition, id: number | null = null) {
  if (!canLeave()) return;
  editor.value = definitionOf(definition);
  selectedId.value = id;
  baseline.value = JSON.stringify(editor.value);
  preview.value = "schema";
  exported.value = "";
  error.value = "";
  result.value = null;
  callError.value = "";
  const sample: Record<string, unknown> = Object.create(null);
  for (const p of definition.parameters) {
    if (p.fixed) continue;
    sample[p.name] =
      p.type === "boolean"
        ? false
        : p.type === "number" || p.type === "integer"
          ? 0
          : p.type === "object"
            ? {}
            : p.type === "array"
              ? []
              : p.name === "podIp"
                ? "10.60.144.183"
                : "";
  }
  callArgs.value = JSON.stringify(sample, null, 2);
}

function newTool(example = false) {
  openEditor(example ? podExample() : emptyDefinition());
}

function addParameter() {
  editor.value?.parameters.push({
    name: "",
    description: "",
    in: "query",
    type: "string",
    required: true,
    fixed: false,
    value: "",
  });
  preview.value = "schema";
}

function extractQuery() {
  if (!editor.value) return;
  try {
    const url = new URL(editor.value.url);
    const entries = [...url.searchParams];
    const names = new Set(editor.value.parameters.map((p) => p.name));
    for (const [name] of entries) {
      if (names.has(name))
        throw new Error(`参数 ${name} 重复，请先合并查询参数`);
      names.add(name);
    }
    for (const [name, value] of entries)
      editor.value.parameters.push({
        name,
        value,
        description: "",
        in: "query",
        type: "string",
        fixed: true,
        required: false,
      });
    url.search = "";
    editor.value.url = url.toString();
    notify(
      entries.length
        ? `已提取 ${entries.length} 个固定查询参数，可在表格中调整`
        : "地址中没有查询参数",
    );
  } catch (e) {
    error.value = message(e);
  }
}

async function save() {
  if (!editor.value) return;
  saving.value = true;
  error.value = "";
  try {
    const record = await api<Tool>(
      selectedId.value === null ? "/tools" : `/tools/${selectedId.value}`,
      {
        method: selectedId.value === null ? "POST" : "PUT",
        body: JSON.stringify(editor.value),
      },
    );
    await refreshTools();
    editor.value = definitionOf(record);
    selectedId.value = record.id;
    baseline.value = JSON.stringify(editor.value);
    preview.value = "schema";
    notify("接口配置已保存");
  } catch (e) {
    error.value = message(e);
  } finally {
    saving.value = false;
  }
}

async function publish() {
  if (selectedId.value === null) return;
  publishing.value = true;
  error.value = "";
  const next = !published.value;
  try {
    await api(`/tools/${selectedId.value}/publish`, {
      method: "POST",
      body: JSON.stringify({ published: next }),
    });
    await refreshTools();
    notify(next ? "工具已发布，可通过 MCP 调用" : "工具已停用，可以编辑配置");
  } catch (e) {
    error.value = message(e);
  } finally {
    publishing.value = false;
  }
}

async function removeTool(t: Tool) {
  if (!window.confirm(`确定删除 ${t.name}？删除后无法恢复。`)) return;
  try {
    await api(`/tools/${t.id}`, { method: "DELETE" });
    await refreshTools();
    notify("接口已删除");
  } catch (e) {
    error.value = message(e);
  }
}

async function runCall() {
  calling.value = true;
  result.value = null;
  callError.value = "";
  try {
    const args: unknown = JSON.parse(callArgs.value);
    if (args === null || typeof args !== "object" || Array.isArray(args))
      throw new Error("调用参数必须是 JSON 对象");
    result.value = await api<CallResult>(`/tools/${selectedId.value}/call`, {
      method: "POST",
      body: JSON.stringify({ arguments: args }),
    });
  } catch (e) {
    callError.value = message(e);
  } finally {
    calling.value = false;
  }
}

async function loadExport(format: "schema" | "openapi" | "higress") {
  preview.value = format;
  exported.value = "";
  if (format === "schema" || selectedId.value === null) return;
  const id = selectedId.value;
  try {
    const response = await fetch(`/api/tools/${id}/export?format=${format}`);
    if (!response.ok) throw new Error((await response.json()).error);
    const text = await response.text();
    if (preview.value === format && selectedId.value === id && !dirty.value)
      exported.value = text;
  } catch (e) {
    error.value = message(e);
  }
}

function downloadExport() {
  const blob = new Blob([previewText.value], {
    type: "text/plain;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${editor.value?.name || "tool"}-${preview.value}.${preview.value === "higress" ? "yaml" : "json"}`;
  a.click();
  URL.revokeObjectURL(url);
}

async function createToken() {
  tokenBusy.value = true;
  error.value = "";
  createdToken.value = "";
  try {
    const result = await api<{ token: string }>("/tokens", {
      method: "POST",
      body: JSON.stringify({ name: tokenName.value }),
    });
    createdToken.value = result.token;
    tokenName.value = "";
    tokens.value = await api<AccessToken[]>("/tokens");
  } catch (e) {
    error.value = message(e);
  } finally {
    tokenBusy.value = false;
  }
}

async function revokeToken(t: AccessToken) {
  if (
    !window.confirm(
      `撤销 ${t.name} 后，使用它的客户端将无法继续调用。确定撤销吗？`,
    )
  )
    return;
  try {
    await api(`/tokens/${t.id}`, { method: "DELETE" });
    tokens.value = await api<AccessToken[]>("/tokens");
    createdToken.value = "";
    notify("令牌已撤销");
  } catch (e) {
    error.value = message(e);
  }
}

async function toggleUser(u: User) {
  try {
    await api(`/users/${u.id}`, {
      method: "PATCH",
      body: JSON.stringify({ disabled: !u.disabled }),
    });
    users.value = await api<User[]>("/users");
    notify(u.disabled ? "账户已启用" : "账户已停用，登录和 MCP 访问已失效");
  } catch (e) {
    error.value = message(e);
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    notify("已复制");
  } catch {
    notify("复制失败，请手动选择文本复制");
  }
}
function formattedBody(value: string) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
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
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) {
    e.preventDefault();
    e.returnValue = "";
  }
}

onMounted(async () => {
  window.addEventListener("beforeunload", beforeUnload);
  try {
    user.value = await api<User>("/me");
    await afterLogin();
  } catch (e) {
    if (user.value) error.value = message(e);
  } finally {
    loading.value = false;
  }
});
onUnmounted(() => {
  window.removeEventListener("beforeunload", beforeUnload);
  clearTimeout(toastTimer);
});
</script>

<template>
  <div v-if="loading" class="loading-screen" role="status">正在连接工作台…</div>
  <div v-else-if="!user" class="auth-page">
    <section class="auth-story">
      <a class="brand" href="/"
        ><span class="brand-mark" aria-hidden="true"
          ><svg viewBox="0 0 32 32">
            <path d="M5 8h8v16H5zM19 8h8v16h-8zM13 16h6" /></svg></span
        ><span>接口工坊<small>API-to-MCP</small></span></a
      >
      <div class="auth-story-content">
        <span class="outline-label">让现有接口，成为 AI 的工具</span>
        <h1>从一个 API，<br />到一次工具调用。</h1>
        <p>
          定义请求和参数，在同一个工作台里完成测试、发布与连接。业务接口保持原样，调用方式更进一步。
        </p>
        <div
          class="connection-visual"
          aria-label="REST API 通过参数映射连接 MCP"
        >
          <span>REST API<small>你的业务接口</small></span>
          <div class="connection-line"><i></i><b>参数映射</b><i></i></div>
          <span>MCP<small>AI 工具调用</small></span>
        </div>
      </div>
      <p class="auth-footnote">Vue 3 + TypeScript · Go · PostgreSQL</p>
    </section>
    <section class="auth-form-section">
      <form class="auth-form" @submit.prevent="authenticate">
        <span class="section-kicker">开发者工作台</span>
        <h2>{{ registering ? "创建你的账户" : "欢迎回来" }}</h2>
        <p class="muted">
          {{
            registering
              ? "注册后即可管理自己的接口和 MCP 工具。"
              : "登录后继续配置和管理你的接口。"
          }}
        </p>
        <div v-if="error" class="alert error" role="alert">{{ error }}</div>
        <label
          >用户名<input
            v-model="username"
            name="username"
            required
            minlength="3"
            maxlength="32"
            pattern="[A-Za-z][A-Za-z0-9_-]{2,31}"
            autocomplete="username"
            placeholder="例如 zhangsan"
        /></label>
        <label
          >密码<input
            v-model="password"
            name="password"
            required
            type="password"
            minlength="12"
            maxlength="72"
            :autocomplete="registering ? 'new-password' : 'current-password'"
            placeholder="至少 12 位字符"
        /></label>
        <button class="button primary auth-submit" :disabled="authBusy">
          {{
            authBusy
              ? "正在处理…"
              : registering
                ? "注册并进入工作台"
                : "登录工作台"
          }}
        </button>
        <p class="auth-switch">
          {{ registering ? "已有账户？" : "还没有账户？"
          }}<button
            type="button"
            class="text-button"
            @click="
              registering = !registering;
              error = '';
            "
          >
            {{ registering ? "去登录" : "创建账户" }}
          </button>
        </p>
      </form>
    </section>
  </div>
  <div v-else class="workspace">
    <aside class="sidebar">
      <a class="brand" href="/" @click.prevent="navigate('tools')"
        ><span class="brand-mark" aria-hidden="true"
          ><svg viewBox="0 0 32 32">
            <path d="M5 8h8v16H5zM19 8h8v16h-8zM13 16h6" /></svg></span
        ><span>接口工坊<small>API-to-MCP</small></span></a
      >
      <div class="workspace-label">我的工作空间</div>
      <nav aria-label="主导航">
        <button
          :class="{ active: view === 'tools' }"
          @click="navigate('tools')"
        >
          <span class="nav-icon" aria-hidden="true">▦</span>接口库<span
            class="nav-count"
            aria-hidden="true"
            >{{ tools.length }}</span
          ></button
        ><button
          :class="{ active: view === 'tokens' }"
          @click="navigate('tokens')"
        >
          <span class="nav-icon" aria-hidden="true">⌁</span>连接与令牌</button
        ><button
          v-if="user.role === 'admin'"
          :class="{ active: view === 'users' }"
          @click="navigate('users')"
        >
          <span class="nav-icon" aria-hidden="true">♙</span>用户管理
        </button>
      </nav>
      <div class="sidebar-note">
        <span class="status-dot"></span>独立 MCP 服务
        <p>只发布你明确开放的工具。</p>
      </div>
      <div class="user-box">
        <div class="avatar">{{ user.username.slice(0, 1).toUpperCase() }}</div>
        <div>
          <strong>{{ user.username }}</strong
          ><small>{{ user.role === "admin" ? "管理员" : "成员" }}</small>
        </div>
        <button
          class="icon-button"
          title="退出登录"
          aria-label="退出登录"
          @click="logout"
        >
          ↪
        </button>
      </div>
    </aside>
    <main class="main-content">
      <div v-if="error" class="alert error main-alert" role="alert">
        <span>{{ error }}</span
        ><button class="icon-button" aria-label="关闭提示" @click="error = ''">
          ×
        </button>
      </div>
      <template v-if="view === 'tools' && !editor">
        <header class="page-header">
          <div>
            <div class="breadcrumb">工作空间 / 接口管理</div>
            <h1>
              接口库<span class="heading-count">{{ tools.length }}</span>
            </h1>
            <p>把现有 REST API，连接到你的 AI 工作流。</p>
          </div>
          <button class="button primary" @click="newTool()">
            <span aria-hidden="true">＋</span>新建接口
          </button>
        </header>
        <div class="toolbar">
          <div class="filter-tabs" aria-label="接口状态筛选">
            <button
              :class="{ selected: filter === 'all' }"
              @click="filter = 'all'"
            >
              全部接口 <span>{{ tools.length }}</span></button
            ><button
              :class="{ selected: filter === 'published' }"
              @click="filter = 'published'"
            >
              已发布 <span>{{ publishedCount }}</span></button
            ><button
              :class="{ selected: filter === 'draft' }"
              @click="filter = 'draft'"
            >
              草稿 <span>{{ tools.length - publishedCount }}</span>
            </button>
          </div>
          <label class="search-box"
            ><span class="sr-only">搜索接口</span
            ><span aria-hidden="true">⌕</span
            ><input v-model="search" placeholder="搜索名称、描述或地址"
          /></label>
        </div>
        <div v-if="tools.length === 0" class="empty-state panel">
          <div class="empty-illustration" aria-hidden="true">
            <span>API</span><i></i><span>MCP</span>
          </div>
          <h2>你的第一个 AI 工具，从这里开始</h2>
          <p>填写接口地址和请求方式，定义哪些参数固定、哪些交给客户端填写。</p>
          <div class="button-row">
            <button class="button primary" @click="newTool()">
              创建第一个接口</button
            ><button class="button" @click="newTool(true)">
              使用 Pod 查询示例
            </button>
          </div>
          <small>示例使用公司内网地址，实际调用需要网络可达。</small>
        </div>
        <div v-else class="panel table-wrap">
          <table class="tool-table">
            <thead>
              <tr>
                <th>接口 / 工具名称</th>
                <th>请求</th>
                <th>状态</th>
                <th v-if="user.role === 'admin'">所属用户</th>
                <th>最近更新</th>
                <th class="align-right">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="t in visibleTools" :key="t.id">
                <td>
                  <button class="tool-name" @click="openEditor(t, t.id)">
                    {{ t.name }}
                  </button>
                  <p class="row-description">{{ t.description }}</p>
                </td>
                <td>
                  <span :class="['method', t.method.toLowerCase()]">{{
                    t.method
                  }}</span
                  ><span class="row-url" :title="t.url">{{ t.url }}</span>
                </td>
                <td>
                  <span :class="['badge', t.published ? 'success' : 'neutral']"
                    ><i></i>{{ t.published ? "已发布" : "草稿" }}</span
                  >
                </td>
                <td v-if="user.role === 'admin'">
                  <span class="owner-name">{{ t.ownerName }}</span>
                </td>
                <td class="muted nowrap">{{ date(t.updatedAt) }}</td>
                <td class="align-right nowrap">
                  <button class="text-button" @click="openEditor(t, t.id)">
                    配置</button
                  ><button
                    class="text-button danger-text"
                    @click="removeTool(t)"
                  >
                    删除
                  </button>
                </td>
              </tr>
              <tr v-if="visibleTools.length === 0">
                <td colspan="6" class="no-results">
                  没有符合条件的接口，请调整搜索条件。
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="bottom-note">
          <span>固定参数由服务端填入，动态参数由客户端传入。</span
          ><button class="text-button" @click="newTool(true)">
            查看 Pod 查询示例
          </button>
        </div>
      </template>
      <template v-else-if="view === 'tools' && editor">
        <header class="page-header editor-header">
          <div>
            <button class="back-button" @click="navigate('tools')">
              ‹ 返回接口库
            </button>
            <h1>
              {{ selectedId === null ? "新建接口" : editor.name
              }}<span :class="['badge', published ? 'success' : 'neutral']">{{
                published ? "已发布" : "草稿"
              }}</span>
            </h1>
            <p>定义请求、核对参数，再把接口发布为 MCP 工具。</p>
          </div>
          <div class="button-row">
            <button
              class="button"
              :disabled="selectedId === null || dirty || publishing"
              @click="publish"
            >
              {{
                publishing ? "正在更新…" : published ? "停用工具" : "发布工具"
              }}</button
            ><button
              class="button primary"
              :disabled="saving || published"
              form="tool-form"
            >
              {{ saving ? "正在保存…" : "保存草稿" }}
            </button>
          </div>
        </header>
        <div v-if="published" class="alert info">
          此接口已发布。需要修改时，请先停用工具，再保存并重新发布。
        </div>
        <div class="editor-grid">
          <div class="editor-main">
            <form id="tool-form" class="panel" @submit.prevent="save">
              <fieldset :disabled="published || saving">
                <div class="panel-heading">
                  <div>
                    <h2>接口定义</h2>
                    <p>名称用于客户端识别，描述帮助 AI 判断何时调用。</p>
                  </div>
                  <span class="step-label">HTTP</span>
                </div>
                <div class="form-content">
                  <div class="form-grid">
                    <label
                      >工具名称<input
                        v-model="editor.name"
                        required
                        pattern="[A-Za-z_][A-Za-z0-9_-]{0,63}"
                        maxlength="64"
                        placeholder="例如 getPodInstance" /></label
                    ><label
                      >请求超时（秒）<input
                        v-model.number="editor.timeoutSeconds"
                        type="number"
                        required
                        min="1"
                        max="60"
                    /></label>
                  </div>
                  <label
                    >用途描述<textarea
                      v-model="editor.description"
                      required
                      rows="2"
                      placeholder="描述接口能完成什么操作，以及适合何时调用"
                    /></label
                  ><label
                    >接口地址
                    <div class="request-input">
                      <select v-model="editor.method" aria-label="请求方法">
                        <option
                          v-for="method in [
                            'GET',
                            'POST',
                            'PUT',
                            'PATCH',
                            'DELETE',
                            'HEAD',
                            'OPTIONS',
                          ]"
                          :key="method"
                        >
                          {{ method }}
                        </option></select
                      ><input
                        v-model="editor.url"
                        required
                        type="url"
                        placeholder="https://api.example.com/users/{id}"
                        aria-label="接口地址"
                      /></div
                  ></label>
                  <div class="field-help">
                    <span
                      >路径变量使用 <code>{id}</code>；请求体采用 JSON
                      对象。</span
                    ><button
                      type="button"
                      class="text-button"
                      @click="extractQuery"
                    >
                      提取 URL 查询参数
                    </button>
                  </div>
                </div>
                <div class="parameter-heading">
                  <div>
                    <h2>
                      参数映射
                      <span class="muted">{{ editor.parameters.length }}</span>
                    </h2>
                    <p>固定值由服务端填入，不会出现在客户端输入中。</p>
                  </div>
                  <button
                    type="button"
                    class="button small"
                    @click="addParameter"
                  >
                    ＋ 添加参数
                  </button>
                </div>
                <div v-if="!editor.parameters.length" class="parameter-empty">
                  此接口还没有参数。可以添加参数，或从 URL 提取固定查询值。
                </div>
                <div v-else class="table-wrap parameter-wrap">
                  <table class="parameter-table">
                    <thead>
                      <tr>
                        <th>参数名称 / 描述</th>
                        <th>位置</th>
                        <th>类型</th>
                        <th>映射方式 / 固定值</th>
                        <th>必填</th>
                        <th></th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(p, index) in editor.parameters" :key="index">
                        <td>
                          <input
                            v-model="p.name"
                            required
                            :aria-label="`参数 ${index + 1} 名称`"
                            placeholder="参数名称"
                          /><input
                            v-model="p.description"
                            class="sub-input"
                            :aria-label="`参数 ${index + 1} 描述`"
                            placeholder="参数用途说明"
                          />
                        </td>
                        <td>
                          <select
                            v-model="p.in"
                            :aria-label="`参数 ${index + 1} 位置`"
                          >
                            <option value="query">查询参数</option>
                            <option value="path">路径参数</option>
                            <option value="header">请求头</option>
                            <option value="body">请求体</option>
                          </select>
                        </td>
                        <td>
                          <select
                            v-model="p.type"
                            :aria-label="`参数 ${index + 1} 类型`"
                          >
                            <option
                              v-for="type in [
                                'string',
                                'boolean',
                                'integer',
                                'number',
                                'object',
                                'array',
                              ]"
                              :key="type"
                            >
                              {{ type }}
                            </option>
                          </select>
                        </td>
                        <td>
                          <select
                            v-model="p.fixed"
                            :aria-label="`参数 ${index + 1} 映射方式`"
                          >
                            <option :value="false">客户端填写</option>
                            <option :value="true">固定值</option></select
                          ><input
                            v-if="p.fixed"
                            v-model="p.value"
                            :type="p.in === 'header' ? 'password' : 'text'"
                            :aria-label="`参数 ${index + 1} 固定值`"
                            placeholder="输入固定值"
                            autocomplete="off"
                          />
                        </td>
                        <td class="checkbox-cell">
                          <input
                            v-model="p.required"
                            type="checkbox"
                            :disabled="p.fixed"
                            :aria-label="`参数 ${index + 1} 必填`"
                          />
                        </td>
                        <td>
                          <button
                            type="button"
                            class="icon-button danger-text"
                            :aria-label="`删除参数 ${index + 1}`"
                            @click="editor.parameters.splice(index, 1)"
                          >
                            ×
                          </button>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </fieldset>
            </form>
            <section class="panel call-panel">
              <div class="panel-heading">
                <div>
                  <h2>试调用</h2>
                  <p>
                    仅填写动态参数；固定值会自动合并。此操作会真实请求上游接口。
                  </p>
                </div>
                <button
                  class="button"
                  :disabled="selectedId === null || dirty || calling"
                  @click="runCall"
                >
                  {{ calling ? "请求中…" : "运行测试" }}
                </button>
              </div>
              <div class="form-content">
                <label
                  >调用参数（JSON）<textarea
                    v-model="callArgs"
                    class="code-input"
                    rows="5"
                    spellcheck="false"
                  />
                </label>
                <p v-if="selectedId === null || dirty" class="field-help">
                  请先保存当前配置，再运行测试。
                </p>
                <div v-if="callError" class="alert error" role="alert">
                  {{ callError }}
                </div>
                <div v-if="result" class="call-result">
                  <div class="result-bar">
                    <span
                      :class="[
                        'badge',
                        result.statusCode >= 200 && result.statusCode < 300
                          ? 'success'
                          : 'failure',
                      ]"
                      >HTTP {{ result.statusCode }}</span
                    ><span>{{ result.durationMs }} ms</span
                    ><span>{{ result.contentType || "无内容类型" }}</span>
                  </div>
                  <pre>{{ formattedBody(result.body) }}</pre>
                </div>
              </div>
            </section>
          </div>
          <aside class="preview-column">
            <section class="panel preview-panel">
              <div class="panel-heading">
                <div>
                  <h2>工具预览</h2>
                  <p>核对客户端可见的参数结构。</p>
                </div>
              </div>
              <div class="preview-tabs">
                <button
                  :class="{ selected: preview === 'schema' }"
                  @click="loadExport('schema')"
                >
                  输入结构</button
                ><button
                  :disabled="selectedId === null || dirty"
                  :class="{ selected: preview === 'openapi' }"
                  @click="loadExport('openapi')"
                >
                  OpenAPI</button
                ><button
                  :disabled="selectedId === null || dirty"
                  :class="{ selected: preview === 'higress' }"
                  @click="loadExport('higress')"
                >
                  Higress
                </button>
              </div>
              <pre class="schema-preview">{{
                previewText || "正在生成配置…"
              }}</pre>
              <div class="preview-footer">
                <span
                  >{{
                    editor.parameters.filter((p) => !p.fixed).length
                  }}
                  个动态参数</span
                ><button
                  class="text-button"
                  :disabled="!previewText || (dirty && preview !== 'schema')"
                  @click="downloadExport"
                >
                  下载配置
                </button>
              </div>
            </section>
            <div class="editor-tip">
              <strong>让 AI 知道怎么用</strong>
              <p>
                描述里写清操作对象与用途，参数描述里给出示例。发布后，客户端就能发现这项工具。
              </p>
              <p>导出文件包含固定参数，请妥善保管。</p>
            </div>
          </aside>
        </div>
      </template>
      <template v-else-if="view === 'tokens'">
        <header class="page-header">
          <div>
            <div class="breadcrumb">工作空间 / 客户端连接</div>
            <h1>连接与令牌</h1>
            <p>用个人令牌连接 MCP，仅能调用你自己已发布的工具。</p>
          </div>
        </header>
        <section class="panel connection-panel">
          <div class="panel-heading">
            <div>
              <h2>MCP 服务地址</h2>
              <p>使用支持 Streamable HTTP 的 MCP 客户端连接。</p>
            </div>
            <span class="badge success">HTTP</span>
          </div>
          <div class="endpoint-row">
            <code>{{ endpoint }}</code
            ><button class="button small" @click="copy(endpoint)">
              复制地址
            </button>
          </div>
          <div class="connection-instructions">
            <span>认证方式</span
            ><code>Authorization: Bearer &lt;你的访问令牌&gt;</code>
          </div>
        </section>
        <section class="panel token-panel">
          <div class="panel-heading">
            <div>
              <h2>个人访问令牌</h2>
              <p>完整令牌只展示一次。不要把它写进接口参数或分享给他人。</p>
            </div>
          </div>
          <form class="token-create" @submit.prevent="createToken">
            <label class="grow"
              >令牌名称<input
                v-model="tokenName"
                required
                maxlength="80"
                placeholder="例如：桌面 AI 客户端" /></label
            ><button class="button primary" :disabled="tokenBusy">
              {{ tokenBusy ? "正在创建…" : "创建令牌" }}
            </button>
          </form>
          <div v-if="createdToken" class="token-reveal">
            <strong>请立即复制并保存，这是唯一一次展示。</strong>
            <div>
              <code>{{ createdToken }}</code
              ><button class="button small" @click="copy(createdToken)">
                复制令牌</button
              ><button class="text-button" @click="createdToken = ''">
                我已保存
              </button>
            </div>
          </div>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>令牌前缀</th>
                  <th>创建时间</th>
                  <th class="align-right">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in tokens" :key="t.id">
                  <td>{{ t.name }}</td>
                  <td>
                    <code>{{ t.prefix }}••••••••</code>
                  </td>
                  <td>{{ date(t.createdAt) }}</td>
                  <td class="align-right">
                    <button
                      class="text-button danger-text"
                      @click="revokeToken(t)"
                    >
                      撤销
                    </button>
                  </td>
                </tr>
                <tr v-if="!tokens.length">
                  <td colspan="4" class="no-results">
                    尚未创建令牌。创建后即可连接你的 MCP 工具。
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
      <template v-else-if="view === 'users'">
        <header class="page-header">
          <div>
            <div class="breadcrumb">管理 / 账户权限</div>
            <h1>用户管理</h1>
            <p>
              成员可以自行注册。停用账户后，该账户的会话和 MCP 令牌将无法使用。
            </p>
          </div>
        </header>
        <section class="panel table-wrap">
          <table>
            <thead>
              <tr>
                <th>用户</th>
                <th>角色</th>
                <th>状态</th>
                <th class="align-right">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in users" :key="u.id">
                <td>
                  <strong>{{ u.username }}</strong>
                </td>
                <td>{{ u.role === "admin" ? "管理员" : "成员" }}</td>
                <td>
                  <span
                    :class="['badge', u.disabled ? 'neutral' : 'success']"
                    >{{ u.disabled ? "已停用" : "正常" }}</span
                  >
                </td>
                <td class="align-right">
                  <span v-if="u.role === 'admin'" class="muted">受保护账户</span
                  ><button v-else class="text-button" @click="toggleUser(u)">
                    {{ u.disabled ? "启用账户" : "停用账户" }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </section>
      </template>
    </main>
  </div>
  <div v-if="toast" class="toast" role="status" aria-live="polite">
    <span aria-hidden="true">✓</span>{{ toast }}
  </div>
</template>
