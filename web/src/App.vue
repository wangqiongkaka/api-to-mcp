<script setup lang="ts">
import HelpHint from "./HelpHint.vue";
import GrafanaConnections from "./GrafanaConnections.vue";
import GrafanaLogProcessors from "./GrafanaLogProcessors.vue";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import type {
  AccessToken,
  Approval,
  ApprovalRule,
  CallResult,
  Definition,
  GrafanaConnection,
  GrafanaDatasource,
  GrafanaQuery,
  Parameter,
  Tool,
  User,
} from "./types";
import {
  defaultGrafanaDataType,
  definitionOf,
  emptyDefinition,
  emptyGrafanaDefinition,
  emptyGrafanaQuery,
  grafanaDatasourceFamily,
  podExample,
} from "./types";
import { extractPointerSample, responsePaths } from "./outputPaths";

const user = ref<User | null>(null);
const loading = ref(true);
const registering = ref(false);
const username = ref("");
const password = ref("");
const authBusy = ref(false);
const error = ref("");
const toast = ref("");
let toastTimer: ReturnType<typeof setTimeout>;
const view = ref<"tools" | "approvals" | "tokens" | "grafana" | "users">("tools");
const tools = ref<Tool[]>([]);
const grafanaConnections = ref<GrafanaConnection[]>([]);
const grafanaDatasources = ref<GrafanaDatasource[]>([]);
const loadingDatasources = ref(false);
const datasourceError = ref("");
const grafanaExtractionError = ref("");
const tokens = ref<AccessToken[]>([]);
const users = ref<User[]>([]);
const approvals = ref<Approval[]>([]);
const openApproval = ref<Approval | null>(null);
const deciding = ref(false);
const pendingApprovals = computed(() => approvals.value.filter((item) => item.canDecide).length);
const approvalStatus: Record<Approval["status"], [string, string]> = {
  pending: ["待审批", "warning"],
  expired: ["已过期", "neutral"],
  rejected: ["已拒绝", "neutral"],
  executing: ["执行中，结果未知", "warning"],
  executed: ["已执行", "success"],
  upstream_error: ["已执行，上游返回错误", "failure"],
  unknown: ["结果未知", "warning"],
  failed: ["未执行", "failure"],
};
const allowedOrigins = ref<string[]>([]);
const search = ref("");
const filter = ref("all");
const editor = ref<Definition | null>(null);
const selectedId = ref<number | null>(null);
const baseline = ref("");
const bodyExtractionError = ref("");
const saving = ref(false);
const publishing = ref(false);
const sharing = ref(false);
const calling = ref(false);
const trialPanel = ref<HTMLElement | null>(null);
const result = ref<CallResult | null>(null);
const callError = ref("");
const selectedOutputPaths = ref<string[]>([]);
const previewingOutput = ref(false);
const outputResult = ref<{ fields: string; body?: string; error?: string } | null>(null);
const tokenName = ref("");
const createdToken = ref("");
const tokenBusy = ref(false);
const preview = ref<"schema" | "openapi">("schema");
const exported = ref("");
const parameterTypes: Record<Parameter["type"], string> = {
  string: "字符串",
  boolean: "布尔值",
  integer: "整数",
  number: "数值",
  object: "对象",
  array: "数组",
};
const grafanaParameterTypes: Record<"string" | "boolean" | "integer" | "number", string> = {
  string: "字符串",
  boolean: "布尔值",
  integer: "整数",
  number: "数值",
};
const isGrafanaEditor = computed(() => editor.value?.kind === "grafana");
const grafanaPlaceholderNames = computed(() => {
  if (!isGrafanaEditor.value || !editor.value?.grafana) return [];
  const found: string[] = [];
  const seen = new Set<string>();
  for (const q of editor.value.grafana.queries) {
    for (const match of q.expr.matchAll(/\{\{([A-Za-z_][A-Za-z0-9_.-]{0,127})\}\}/g)) {
      const name = match[1];
      if (!seen.has(name)) {
        seen.add(name);
        found.push(name);
      }
    }
  }
  return found;
});
const bodyParameters = computed(() =>
  editor.value?.parameters.filter((p) => p.in === "body") ?? [],
);
const bodyTemplateParameters = computed(() =>
  bodyParameters.value.filter((p) => p.name && (p.fixed || p.required || p.nonEmpty || p.type === "array")),
);
const canSendBody = computed(
  () => editor.value?.method !== "GET" && editor.value?.method !== "HEAD",
);
const parameterGroups = computed(() => {
  const definition = editor.value;
  if (!definition) return [];
  const rows = definition.parameters.map((p, index) => ({ p, index }));
  return [
    { key: "other", label: "参数映射", rows: rows.filter(({ p }) => p.in !== "body") },
    { key: "body", label: "JSON 请求体", rows: rows.filter(({ p }) => p.in === "body") },
  ].filter(group => group.key === "other" || group.rows.length ||
    (canSendBody.value && !published.value) || definition.bodyTemplate);
});
const successfulResponse = computed(
  () => result.value && result.value.statusCode >= 200 && result.value.statusCode < 300,
);
const availableOutputPaths = computed(
  () => successfulResponse.value && result.value ? responsePaths(result.value.body) : [],
);
const outputFieldSlots = computed(() => Math.max(0, 100 - (editor.value?.outputFields?.length ?? 0)));
const serializedOutputFields = computed(() => JSON.stringify(editor.value?.outputFields ?? []));
const currentOutput = computed(
  () => outputResult.value?.fields === serializedOutputFields.value ? outputResult.value : null,
);
const selectedTool = computed(() => tools.value.find((t) => t.id === selectedId.value));
const published = computed(() => selectedTool.value?.published ?? false);
const sharedTool = computed(() => selectedTool.value !== undefined && isShared(selectedTool.value));
const dirty = computed(
  () =>
    editor.value !== null && JSON.stringify(editor.value) !== baseline.value,
);
const visibleTools = computed(() =>
  tools.value.filter(
    (t) =>
      (filter.value === "all" ||
        (filter.value === "published" ? t.published : !t.published)) &&
      `${t.name} ${t.description} ${t.url} ${t.ownerName} ${t.kind === "grafana" ? "grafana " + grafanaSummary(t) + " " + grafanaDataTypes(t).join(" ") : ""}`
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
  const filters: string[] = [];
  if (editor.value?.kind === "grafana" && editor.value.grafana) {
    const g = editor.value.grafana;
    const defFrom = (g.defaultFrom ?? "").trim();
    const defTo = (g.defaultTo ?? "").trim();
    properties.from = {
      type: "string",
      description:
        "查询开始时间，支持相对时间（如 now-1h）、ISO8601 或 Unix 毫秒时间戳" +
        (!g.fromRequired && defFrom ? `。不传时默认：${defFrom}` : ""),
      ...(g.fromRequired ? { pattern: "\\S" } : defFrom ? { default: defFrom } : {}),
    };
    if (g.fromRequired) required.push("from");
    properties.to = {
      type: "string",
      description:
        "查询结束时间，支持相对时间（如 now）、ISO8601 或 Unix 毫秒时间戳" +
        (!g.toRequired && defTo ? `。不传时默认：${defTo}` : ""),
      ...(g.toRequired ? { pattern: "\\S" } : defTo ? { default: defTo } : {}),
    };
    if (g.toRequired) required.push("to");
  }
  for (const p of editor.value?.parameters ?? []) {
    if (p.fixed || !p.name) continue;
    properties[p.name] = {
      type: p.type,
      description: p.description,
      ...(p.type === "array"
        ? { items: {}, ...(p.nonEmpty ? { minItems: 1 } : {}) }
        : {}),
      ...(p.nonEmpty && p.type === "string" ? { pattern: "\\S" } : {}),
      ...(p.nonEmpty && p.type === "object" ? { minProperties: 1 } : {}),
      ...(p.type === "integer"
        ? { minimum: -9007199254740991, maximum: 9007199254740991 }
        : {}),
    };
    if (p.required || p.nonEmpty) required.push(p.name);
    if (p.queryFilter) filters.push(p.name);
  }
  return {
    type: "object", properties, required, additionalProperties: false,
    ...(filters.length ? { description: `请填写 ${filters.join(" 或 ")}` } : {}),
  };
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

function isShared(tool: Tool) {
  return tool.public && tool.ownerId !== user.value?.id && user.value?.role !== "admin";
}

async function refreshTools() {
  tools.value = await api<Tool[]>("/tools");
}
async function refreshGrafanaConnections() {
  grafanaConnections.value = await api<GrafanaConnection[]>("/grafana/connections");
}
async function loadDatasources(connectionId: number, autoSelectDefault = false) {
  grafanaDatasources.value = [];
  datasourceError.value = "";
  if (!connectionId || connectionId < 1) return;
  loadingDatasources.value = true;
  try {
    const list = await api<GrafanaDatasource[]>(`/grafana/connections/${connectionId}/datasources`);
    if (editor.value?.grafana?.connectionId !== connectionId) return;
    grafanaDatasources.value = list;
    if (autoSelectDefault && list.length && editor.value?.grafana) {
      const def = list.find((d) => d.isDefault) ?? list[0];
      for (const q of editor.value.grafana.queries) {
        if (!q.datasourceUid) {
          q.datasourceUid = def.uid;
          q.datasourceType = def.type;
          q.datasourceName = def.name;
          q.dataType = defaultGrafanaDataType(def.type);
        }
      }
    }
  } catch (e) {
    if (editor.value?.grafana?.connectionId === connectionId) {
      datasourceError.value = message(e);
    }
  } finally {
    loadingDatasources.value = false;
  }
}
function grafanaDataTypes(t: Tool): Array<"metrics" | "logs"> {
  if (t.kind !== "grafana" || !t.grafana?.queries?.length) return [];
  const seen = new Set<"metrics" | "logs">();
  for (const q of t.grafana.queries) {
    const dt = q.dataType ?? (q.datasourceType ? defaultGrafanaDataType(q.datasourceType) : undefined);
    if (dt === "metrics" || dt === "logs") seen.add(dt);
  }
  return (["metrics", "logs"] as const).filter((dt) => seen.has(dt));
}
function grafanaSummary(t: Tool) {
  if (t.kind !== "grafana") return t.url;
  const g = t.grafana;
  if (!g || !g.connectionId) return "Grafana 查询";
  const conn = grafanaConnections.value.find((c) => c.id === g.connectionId);
  const dsNames = Array.from(new Set((g.queries ?? []).map((q) => q.datasourceName || q.datasourceType).filter(Boolean)));
  const count = g.queries?.length ?? 0;
  const parts: string[] = [];
  if (conn) parts.push(conn.name);
  if (count) parts.push(`${count} 条查询`);
  if (dsNames.length) parts.push(dsNames.join("、"));
  return parts.join(" · ") || "Grafana 查询";
}
async function loadApprovals() {
  approvals.value = await api<Approval[]>("/approvals");
  if (openApproval.value && !approvals.value.some((item) => item.id === openApproval.value?.id))
    openApproval.value = null;
}
async function afterLogin() {
  if (window.location.hash === "#approvals") view.value = "approvals";
  await Promise.all([
    refreshTools(),
    refreshGrafanaConnections(),
    loadApprovals(),
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
    if (next === "tools") await Promise.all([refreshTools(), refreshGrafanaConnections()]);
    if (next === "grafana") await refreshGrafanaConnections();
    if (next === "approvals") await loadApprovals();
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
  bodyExtractionError.value = "";
  grafanaExtractionError.value = "";
  grafanaDatasources.value = [];
  datasourceError.value = "";
  preview.value = "schema";
  exported.value = "";
  error.value = "";
  result.value = null;
  callError.value = "";
  selectedOutputPaths.value = [];
  outputResult.value = null;
  trialRows.value = [];
  syncTrialRows();
  if (editor.value.kind === "grafana" && editor.value.grafana?.connectionId && !sharedTool.value) {
    void loadDatasources(editor.value.grafana.connectionId, false);
  }
}

function newTool(example = false) {
  openEditor(example ? podExample() : emptyDefinition());
}

function newGrafanaTool() {
  const defaultConn = grafanaConnections.value.length === 1 ? grafanaConnections.value[0].id : 0;
  openEditor(emptyGrafanaDefinition(defaultConn));
}

function switchEditorKind(kind: "http" | "grafana") {
  if (!editor.value || selectedId.value !== null) return;
  const currentlyGrafana = editor.value.kind === "grafana";
  if ((kind === "grafana") === currentlyGrafana) return;
  const name = editor.value.name;
  const description = editor.value.description;
  const timeoutSeconds = editor.value.timeoutSeconds;
  const next =
    kind === "grafana"
      ? emptyGrafanaDefinition(grafanaConnections.value.length === 1 ? grafanaConnections.value[0].id : 0)
      : emptyDefinition();
  next.name = name;
  next.description = description;
  next.timeoutSeconds = timeoutSeconds;
  editor.value = next;
  preview.value = "schema";
  grafanaDatasources.value = [];
  datasourceError.value = "";
  grafanaExtractionError.value = "";
  bodyExtractionError.value = "";
  if (kind === "grafana" && next.grafana?.connectionId) {
    void loadDatasources(next.grafana.connectionId, false);
  }
}

function onGrafanaConnectionChange() {
  const g = editor.value?.grafana;
  if (!g) return;
  for (const q of g.queries) {
    q.datasourceUid = "";
    q.datasourceType = "";
    q.datasourceName = "";
    q.queryMode = "";
  }
  void loadDatasources(g.connectionId, true);
}

function nextGrafanaRefId(queries: GrafanaQuery[]) {
  const used = new Set(queries.map((q) => q.refId.toUpperCase()));
  for (let code = 65; code <= 90; code++) {
    const candidate = String.fromCharCode(code);
    if (!used.has(candidate)) return candidate;
  }
  for (let n = 1; n <= 99; n++) {
    const candidate = `Q${n}`;
    if (!used.has(candidate)) return candidate;
  }
  return "Q";
}

function addGrafanaQuery() {
  const g = editor.value?.grafana;
  if (!g || g.queries.length >= 20) return;
  const previous = g.queries[g.queries.length - 1];
  g.queries.push(emptyGrafanaQuery(nextGrafanaRefId(g.queries), previous));
}

function removeGrafanaQuery(index: number) {
  const g = editor.value?.grafana;
  if (!g || g.queries.length <= 1) return;
  g.queries.splice(index, 1);
}

function clearQueryTypeSpecificFields(q: GrafanaQuery) {
  if (q.dataType !== "logs") q.logProcessors = undefined;
  if (q.dataType !== "metrics") {
    q.maxPoints = undefined;
    q.changePointRatio = undefined;
    q.maxChangePoints = undefined;
  }
}

function setOptionalQueryNumber(
  q: GrafanaQuery,
  field: "maxPoints" | "changePointRatio" | "maxChangePoints",
  raw: string,
) {
  const trimmed = raw.trim();
  if (!trimmed) {
    q[field] = undefined;
    return;
  }
  const n = Number(trimmed);
  q[field] = Number.isFinite(n) ? n : undefined;
}

function metricsAnalysisSummary(q: GrafanaQuery) {
  const pts = q.maxPoints ?? 240;
  const ratio = q.changePointRatio ?? 0.3;
  const maxCP = q.maxChangePoints ?? 5;
  const custom = q.maxPoints !== undefined || q.changePointRatio !== undefined || q.maxChangePoints !== undefined;
  return `${custom ? "已自定义" : "默认"}：LTTB ${pts} 点 · 突变阈值 ${ratio} · 最多 ${maxCP} 个`;
}

function selectQueryDatasource(q: GrafanaQuery, uid: string) {
  const ds = grafanaDatasources.value.find((item) => item.uid === uid);
  q.datasourceUid = uid;
  if (ds) {
    q.datasourceType = ds.type;
    q.datasourceName = ds.name;
    q.dataType = defaultGrafanaDataType(ds.type);
    clearQueryTypeSpecificFields(q);
    q.queryMode = "";
  } else {
    q.datasourceType = "";
    q.datasourceName = "";
  }
}

function queryExprPlaceholder(dsType: string) {
  switch (grafanaDatasourceFamily(dsType)) {
    case "prometheus":
      return '例如：up{instance=~"{{host_ip}}.*"}';
    case "loki":
      return '例如：{host="{{host_ip}}"} |= "error"';
    case "victorialogs":
      return '例如：host:"{{host_ip}}" AND _msg:error';
    case "clickhouse":
    case "sql":
      return "例如：SELECT * FROM events WHERE host_ip = '{{host_ip}}' LIMIT 100";
    case "elasticsearch":
      return '例如：host.ip:"{{host_ip}}" AND level:error';
    default:
      return '输入查询语句，使用 {{host_ip}} 声明变量';
  }
}

function queryModeOptions(dsType: string): Array<{ value: string; label: string }> {
  switch (grafanaDatasourceFamily(dsType)) {
    case "prometheus":
      return [
        { value: "", label: "区间查询（range，默认）" },
        { value: "instant", label: "即时查询（instant）" },
      ];
    case "loki":
      return [
        { value: "", label: "区间查询（range，默认）" },
        { value: "instant", label: "即时查询（instant）" },
      ];
    case "victorialogs":
      return [
        { value: "", label: "日志列表（instant，默认）" },
        { value: "stats", label: "聚合统计（stats）" },
        { value: "statsRange", label: "区间统计（statsRange）" },
        { value: "hits", label: "命中分布（hits）" },
      ];
    case "clickhouse":
    case "sql":
      return [
        { value: "", label: "表格（table，默认）" },
        { value: "time_series", label: "时序（time_series）" },
      ];
    case "elasticsearch":
      return [
        { value: "", label: "原始文档（raw_data，默认）" },
        { value: "logs", label: "日志（logs）" },
      ];
    default:
      return [];
  }
}

function extractGrafanaParameters() {
  const definition = editor.value;
  if (!definition?.grafana || published.value) return;
  grafanaExtractionError.value = "";
  try {
    const existing = new Map(definition.parameters.map((p) => [p.name.toLowerCase(), p]));
    const added: Parameter[] = [];
    for (const q of definition.grafana.queries) {
      if (/\{\{\s*\}\}/.test(q.expr)) {
        throw new Error(`查询 ${q.refId || ""} 包含空占位符 {{}}`);
      }
      for (const match of q.expr.matchAll(/\{\{([A-Za-z_][A-Za-z0-9_.-]{0,127})\}\}/g)) {
        const name = match[1];
        const lower = name.toLowerCase();
        if (lower === "from" || lower === "to") continue;
        const found = existing.get(lower);
        if (found) {
          if (found.name !== name) {
            throw new Error(`占位符与已有参数大小写不一致：${name}`);
          }
          continue;
        }
        const mapping: Parameter = {
          name,
          description: "",
          in: "query",
          type: "string",
          required: true,
          fixed: false,
          value: "",
        };
        added.push(mapping);
        existing.set(lower, mapping);
      }
    }
    if (definition.parameters.length + added.length > 100) {
      throw new Error("最多配置 100 个参数，请减少占位符或移除不需要的参数");
    }
    if (!added.length) {
      notify("查询语句中没有可新增的占位符（from / to 为内置时间参数）");
      return;
    }
    definition.parameters.push(...added);
    preview.value = "schema";
    notify(`已生成 ${added.length} 个查询变量参数，请在参数表确认说明与类型`);
  } catch (e) {
    grafanaExtractionError.value = message(e);
  }
}

function setApproval(enabled: boolean, approver: ApprovalRule["approver"] = "self") {
  if (!editor.value) return;
  editor.value.approval = !enabled ? undefined
    : approver === "user" ? { approver, approverName: "" } : { approver };
}

async function toggleApproval(item: Approval) {
  if (openApproval.value?.id === item.id) {
    openApproval.value = null;
    return;
  }
  try {
    openApproval.value = await api<Approval>(`/approvals/${item.id}`);
  } catch (e) {
    error.value = message(e);
  }
}

async function decideApproval(item: Approval, approve: boolean) {
  if (approve && !window.confirm(`批准后将立即按提交的参数执行 ${item.toolName}，确定批准吗？`)) return;
  deciding.value = true;
  error.value = "";
  try {
    const detail = await api<Approval>(`/approvals/${item.id}/decision`, {
      method: "POST",
      body: JSON.stringify({ approve }),
    });
    openApproval.value = detail;
    await loadApprovals();
    notify({
      executed: "已批准，接口已执行，结果已保存",
      upstream_error: "已批准并执行，上游返回错误，请查看结果",
      unknown: "请求已发出但结果未知，请到上游核实",
      failed: "已批准，但接口未执行，请查看原因",
    }[detail.status as string] ?? "已拒绝该审批单");
  } catch (e) {
    error.value = message(e);
    await loadApprovals().catch(() => undefined);
  } finally {
    deciding.value = false;
  }
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

function addOutputField() {
  if (!editor.value) return;
  editor.value.outputFields ??= [];
  editor.value.outputFields.push({ name: "", source: "response", value: "" });
}

function addResponseFields() {
  const paths = selectedOutputPaths.value;
  if (!editor.value || !paths.length || paths.length > outputFieldSlots.value) return;
  const fields = editor.value.outputFields ??= [];
  for (const path of paths) {
    const last = path.slice(path.lastIndexOf("/") + 1).replaceAll("~1", "/").replaceAll("~0", "~").trim();
    const base = last && last !== "*" && last.length <= 40 ? last : "结果";
    let name = base;
    for (let index = 2; fields.some((field) => field.name === name); index++) {
      name = `${base}_${index}`;
    }
    fields.push({ name, source: "response", value: path });
  }
  selectedOutputPaths.value = [];
}

function removeOutputField(index: number) {
  const fields = editor.value?.outputFields;
  if (editor.value && fields) {
    fields.splice(index, 1);
    if (!fields.length) editor.value.outputFields = undefined;
  }
}

function sampleForOutputField(field: { source: "response" | "fixed"; value: string }) {
  if (field.source !== "response" || !successfulResponse.value || !result.value) return "";
  return extractPointerSample(result.value.body, field.value);
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

function extractBodyParameters() {
  const definition = editor.value;
  if (!definition?.bodyTemplate || !canSendBody.value || published.value) return;
  bodyExtractionError.value = "";
  try {
    let body: unknown;
    try {
      body = JSON.parse(definition.bodyTemplate);
    } catch {
      throw new Error("请求体模板必须是有效的 JSON 对象");
    }
    if (!body || typeof body !== "object" || Array.isArray(body))
      throw new Error("请求体模板必须是有效的 JSON 对象");

    const existing = new Map(definition.parameters.map((p) => [p.name.toLowerCase(), p]));
    const added: Parameter[] = [];
    const pending: unknown[] = [body];
    while (pending.length) {
      const value = pending.pop();
      if (value && typeof value === "object") {
        pending.push(...Object.values(value).reverse());
        continue;
      }
      if (typeof value !== "string" || !value.startsWith("{{") || !value.endsWith("}}"))
        continue;
      const name = value.slice(2, -2);
      if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,127}$/.test(name))
        throw new Error(`占位符名称无效：${JSON.stringify(value)}`);
      const parameter = existing.get(name.toLowerCase());
      if (parameter) {
        if (parameter.in !== "body" || parameter.name !== name)
          throw new Error(`占位符与参数名称冲突：${name}`);
        continue;
      }
      const mapping: Parameter = {
        name, description: "", in: "body", type: "string", required: true, fixed: false, value: "",
      };
      added.push(mapping);
      existing.set(name.toLowerCase(), mapping);
    }
    if (definition.parameters.length + added.length > 100)
      throw new Error("最多配置 100 个参数，请减少模板占位符或移除不需要的参数");
    if (!added.length) {
      notify("模板中没有可新增的占位符");
      return;
    }
    definition.parameters.push(...added);
    preview.value = "schema";
    notify(`已生成 ${added.length} 个请求体参数，请在参数表确认类型`);
  } catch (e) {
    bodyExtractionError.value = message(e);
  }
}

const trialFields = computed(() => {
  const fields: Array<{ parameter: Parameter; name: string; type: Parameter["type"]; defaultHint?: string }> = [];
  if (editor.value?.kind === "grafana" && editor.value.grafana) {
    const g = editor.value.grafana;
    fields.push(
      {
        parameter: {
          name: "from",
          description: "查询开始时间",
          in: "query",
          type: "string",
          required: g.fromRequired,
          nonEmpty: g.fromRequired,
          fixed: false,
          value: "",
        },
        name: "from",
        type: "string",
        defaultHint: !g.fromRequired && g.defaultFrom ? g.defaultFrom : undefined,
      },
      {
        parameter: {
          name: "to",
          description: "查询结束时间",
          in: "query",
          type: "string",
          required: g.toRequired,
          nonEmpty: g.toRequired,
          fixed: false,
          value: "",
        },
        name: "to",
        type: "string",
        defaultHint: !g.toRequired && g.defaultTo ? g.defaultTo : undefined,
      },
    );
  }
  for (const parameter of editor.value?.parameters ?? []) {
    if (!parameter.fixed && parameter.name.trim()) {
      fields.push({ parameter, name: parameter.name, type: parameter.type });
    }
  }
  return fields;
});
const trialRows = ref<
  Array<{ parameter: Parameter; name: string; type: Parameter["type"]; value: string; defaultHint?: string }>
>([]);

function syncTrialRows() {
  const previous = trialRows.value;
  trialRows.value = trialFields.value.map(field => {
    const old = previous.find(row => row.parameter === field.parameter)
      ?? previous.find(row => row.name === field.name);
    const sample = field.name === "podIp" && field.type === "string" ? "10.60.144.183" : "";
    return { ...field, value: old?.type === field.type ? old.value : sample };
  });
}
watch(trialFields, syncTrialRows);

const requirements = ["选填", "必须传入", "必须有值"] as const;
function requirementOf(p: Parameter) {
  if (p.nonEmpty) return "必须有值";
  return p.required ? "必须传入" : "选填";
}
function setRequirement(p: Parameter, choice: string) {
  p.required = choice !== "选填";
  p.nonEmpty = choice === "必须有值";
}

function trialArguments() {
  const entries: string[] = [];
  for (const {name, type, value, parameter} of trialRows.value) {
    const trimmed = value.trim();
    if (!trimmed) {
      if (parameter.nonEmpty) throw new Error(`参数 ${name} 不能为空`);
      if (parameter.required) {
        const empty = type === "string" ? JSON.stringify(value) : type === "array" ? "[]" : type === "object" ? "{}" : undefined;
        if (empty === undefined) throw new Error(`请填写必填参数：${name}`);
        entries.push(`${JSON.stringify(name)}:${empty}`);
      }
      continue;
    }
    let json = trimmed;
    if (type === "string") json = JSON.stringify(value);
    else if (type === "integer" || type === "number") {
      const number = Number(trimmed);
      if (!Number.isFinite(number) || (type === "integer" && !Number.isSafeInteger(number)))
        throw new Error(`参数 ${name} 应为${parameterTypes[type]}`);
      json = JSON.stringify(number);
    } else if (type === "array" && !trimmed.startsWith("[")) {
      json = JSON.stringify(trimmed.split(/\r?\n/).map(line => line.trim()).filter(Boolean));
    } else {
      let parsed;
      try { parsed = JSON.parse(json); }
      catch { throw new Error(`参数 ${name} 的${parameterTypes[type]}格式无效`); }
      const valid = type === "array" ? Array.isArray(parsed)
        : type === "object" ? parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)
        : typeof parsed === "boolean";
      if (!valid) throw new Error(`参数 ${name} 应为${parameterTypes[type]}`);
    }
    entries.push(`${JSON.stringify(name)}:${json}`);
  }
  return `{${entries.join(",")}}`;
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

async function toggleSharing(tool: Tool) {
  sharing.value = true;
  error.value = "";
  try {
    await api(`/tools/${tool.id}/visibility`, {
      method: "PATCH",
      body: JSON.stringify({ public: !tool.public }),
    });
    await refreshTools();
    notify(tool.public ? "已取消共享" : "已设为公共接口，发布后所有登录用户可调用");
  } catch (e) {
    error.value = message(e);
  } finally {
    sharing.value = false;
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

function focusTrial() {
  trialPanel.value?.scrollIntoView({ block: "start" });
  trialPanel.value?.focus({ preventScroll: true });
}

async function runCall() {
  const invalid = trialPanel.value?.querySelector<HTMLInputElement>('input[type="number"]:invalid');
  if (invalid) {
    invalid.reportValidity();
    return;
  }
  calling.value = true;
  result.value = null;
  callError.value = "";
  selectedOutputPaths.value = [];
  outputResult.value = null;
  const id = selectedId.value;
  try {
    const response = await api<CallResult>(`/tools/${id}/call`, {
      method: "POST",
      body: `{"arguments":${trialArguments()},"raw":${!sharedTool.value}}`,
    });
    if (selectedId.value !== id) return;
    result.value = response;
    if (successfulResponse.value && editor.value?.outputFields?.length) await previewOutput();
  } catch (e) {
    if (selectedId.value === id) callError.value = message(e);
  } finally {
    calling.value = false;
  }
}

async function previewOutput() {
  const response = result.value;
  const fields = serializedOutputFields.value;
  if (!response) return;
  previewingOutput.value = true;
  try {
    const preview = await api<{ body: string }>("/output-preview", {
      method: "POST",
      body: `{"body":${JSON.stringify(response.body)},"fields":${fields}}`,
    });
    if (result.value === response) outputResult.value = { fields, body: preview.body };
  } catch (e) {
    if (result.value === response) outputResult.value = { fields, error: message(e) };
  } finally {
    previewingOutput.value = false;
  }
}

async function loadExport(format: "schema" | "openapi") {
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
  a.download = `${editor.value?.name || "tool"}-${preview.value}.json`;
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
          :class="{ active: view === 'approvals' }"
          @click="navigate('approvals')"
        >
          <span class="nav-icon" aria-hidden="true">✓</span>审批<span
            v-if="pendingApprovals"
            class="nav-count"
            :aria-label="`${pendingApprovals} 个待你审批`"
            >{{ pendingApprovals }}</span
          ></button
        ><button
          :class="{ active: view === 'tokens' }"
          @click="navigate('tokens')"
        >
          <span class="nav-icon" aria-hidden="true">⌁</span>连接与令牌</button
        ><button
          v-if="user.role === 'admin'"
          :class="{ active: view === 'grafana' }"
          @click="navigate('grafana')"
        >
          <span class="nav-icon" aria-hidden="true">◈</span>Grafana 连接<span
            v-if="grafanaConnections.length"
            class="nav-count"
            aria-hidden="true"
            >{{ grafanaConnections.length }}</span
          ></button
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
          <div class="button-row">
            <button class="button" @click="newGrafanaTool()">
              <span aria-hidden="true">＋</span>新建 Grafana 查询
            </button>
            <button class="button primary" @click="newTool()">
              <span aria-hidden="true">＋</span>新建接口
            </button>
          </div>
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
                <th>操作</th>
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
                  <template v-if="t.kind === 'grafana'">
                    <div class="grafana-method-tags">
                      <span class="method grafana">GRAFANA</span>
                      <span
                        v-for="dt in grafanaDataTypes(t)"
                        :key="dt"
                        :class="['badge', 'data-type', dt]"
                      >{{ dt === "metrics" ? "METRICS" : "LOGS" }}</span>
                    </div>
                    <span class="row-url" :title="grafanaSummary(t)">{{ grafanaSummary(t) }}</span>
                  </template>
                  <template v-else>
                    <span :class="['method', t.method.toLowerCase()]">{{
                      t.method
                    }}</span
                    ><span v-if="t.url" class="row-url" :title="t.url">{{ t.url }}</span>
                  </template>
                </td>
                <td>
                  <span :class="['badge', t.published ? 'success' : 'neutral']"
                    ><i></i>{{ t.published ? "已发布" : "草稿" }}</span
                  >
                  <span v-if="t.public" class="badge neutral">公共</span>
                  <span v-if="t.approval" class="badge warning">需审批</span>
                </td>
                <td v-if="user.role === 'admin'">
                  <span class="owner-name">{{ t.ownerName }}</span>
                </td>
                <td class="muted nowrap">{{ date(t.updatedAt) }}</td>
                <td class="nowrap">
                  <button v-if="isShared(t)" class="text-button" @click="openEditor(t, t.id)">调用</button>
                  <template v-else>
                    <button class="text-button" @click="openEditor(t, t.id)">配置</button>
                    <button
                      v-if="user.role === 'admin' && t.ownerId === user.id"
                      class="text-button"
                      :disabled="sharing"
                      @click="toggleSharing(t)"
                    >{{ t.public ? "取消共享" : "设为公共" }}</button>
                    <button class="text-button danger-text" @click="removeTool(t)">删除</button>
                  </template>
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
        <button class="back-button" @click="navigate('tools')">
          ‹ 返回接口库
        </button>
        <header class="page-header editor-header" :class="{ 'editor-toolbar': !sharedTool }">
          <div>
            <h1>
              <span class="editor-title" :title="selectedId === null ? undefined : editor.name">
                {{ selectedId === null ? "新建接口" : editor.name }}
              </span>
              <span :class="['badge', published ? 'success' : 'neutral']">{{
                published ? "已发布" : "草稿"
              }}</span>
              <span v-if="selectedTool?.public" class="badge neutral">公共</span>
            </h1>
            <p v-if="sharedTool">{{ editor.description }}</p>
            <p v-else-if="published">停用后可编辑。</p>
          </div>
          <div v-if="!sharedTool" class="button-row">
            <button
              class="button"
              :class="{ primary: !published && (selectedId === null || dirty) }"
              :disabled="saving || published"
              form="tool-form"
            >
              {{ saving ? "正在保存…" : "保存草稿" }}
            </button>
            <button
              class="button"
              :class="{ primary: selectedId !== null && !dirty }"
              @click="focusTrial"
            >调试</button>
            <button
              class="button"
              :disabled="selectedId === null || dirty || saving || publishing"
              @click="publish"
            >{{ publishing ? "正在更新…" : published ? "停用工具" : "发布工具" }}</button>
          </div>
        </header>
        <div class="editor-grid">
          <div class="editor-main">
            <form v-if="!sharedTool" id="tool-form" class="panel" @submit.prevent="save">
              <fieldset :disabled="published || saving">
                <div class="panel-heading">
                  <h2>{{ isGrafanaEditor ? "Grafana 查询定义" : "接口定义" }}</h2>
                  <div v-if="selectedId === null" class="kind-switch" role="group" aria-label="工具类型">
                    <button
                      type="button"
                      :class="{ selected: !isGrafanaEditor }"
                      @click="switchEditorKind('http')"
                    >HTTP 接口</button>
                    <button
                      type="button"
                      :class="{ selected: isGrafanaEditor }"
                      @click="switchEditorKind('grafana')"
                    >Grafana 查询</button>
                  </div>
                  <span v-else-if="isGrafanaEditor" class="method grafana">GRAFANA</span>
                </div>
                <div class="form-content">
                  <div class="form-grid">
                    <label
                      >工具名称<input
                        v-model="editor.name"
                        required
                        pattern="[A-Za-z_][A-Za-z0-9_-]{0,63}"
                        maxlength="64"
                        :placeholder="isGrafanaEditor ? '例如 queryHostMetrics' : '例如 getPodInstance'" /></label
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
                      :placeholder="isGrafanaEditor ? '如按主机 IP 查询 CPU 与错误日志' : '如按主机 IP 查询资产'"
                    /></label>
                  <template v-if="!isGrafanaEditor">
                    <div class="url-field">
                      <div class="url-heading">
                        <div class="group-title">
                          <label for="endpoint-url">接口地址</label>
                          <HelpHint label="路径变量说明" text="接口地址中的路径变量使用 {id} 格式。" />
                        </div>
                        <button type="button" class="text-button" @click="extractQuery">提取 URL 查询参数</button>
                      </div>
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
                          id="endpoint-url" v-model="editor.url"
                          required
                          type="url"
                          placeholder="https://api.example.com/users/{id}"
                          aria-label="接口地址"
                        /></div></div>
                  </template>
                  <template v-else-if="editor.grafana">
                    <div class="grafana-connection-row">
                      <label class="grow">
                        Grafana 连接
                        <select
                          v-model.number="editor.grafana.connectionId"
                          required
                          aria-label="Grafana 连接"
                          @change="onGrafanaConnectionChange"
                        >
                          <option :value="0" disabled>请选择 Grafana 连接</option>
                          <option v-for="c in grafanaConnections" :key="c.id" :value="c.id">
                            {{ c.name }}（{{ c.baseUrl }}）
                          </option>
                        </select>
                      </label>
                      <button
                        type="button"
                        class="button small"
                        :disabled="!editor.grafana.connectionId || loadingDatasources"
                        @click="loadDatasources(editor.grafana.connectionId, false)"
                      >
                        {{ loadingDatasources ? "拉取中…" : "刷新数据源" }}
                      </button>
                    </div>
                    <p v-if="!grafanaConnections.length" class="field-help">
                      尚未配置 Grafana 连接。
                      <button
                        v-if="user.role === 'admin'"
                        type="button"
                        class="text-button"
                        @click="navigate('grafana')"
                      >前往「Grafana 连接」添加</button>
                      <span v-else>请联系管理员在工作台添加 Grafana 连接。</span>
                    </p>
                    <div v-if="datasourceError" class="alert error" role="alert">{{ datasourceError }}</div>
                    <div class="grafana-time-grid">
                      <div class="grafana-time-item">
                        <div class="url-heading">
                          <div class="group-title">
                            <label for="grafana-default-from">开始时间（from）</label>
                            <HelpHint label="时间范围说明" text="内置时间参数，支持相对时间（如 now-1h、now）、ISO8601 或 Unix 毫秒时间戳。查询语句中也可直接写 {{from}}、{{to}}。" />
                          </div>
                          <label class="check-label compact-check">
                            <input v-model="editor.grafana.fromRequired" type="checkbox" />
                            必填
                          </label>
                        </div>
                        <input
                          id="grafana-default-from"
                          v-model.trim="editor.grafana.defaultFrom"
                          :disabled="editor.grafana.fromRequired"
                          :required="!editor.grafana.fromRequired"
                          maxlength="128"
                          :placeholder="editor.grafana.fromRequired ? '调用时由客户端必填' : '默认开始时间，如 now-1h'"
                        />
                      </div>
                      <div class="grafana-time-item">
                        <div class="url-heading">
                          <label for="grafana-default-to">结束时间（to）</label>
                          <label class="check-label compact-check">
                            <input v-model="editor.grafana.toRequired" type="checkbox" />
                            必填
                          </label>
                        </div>
                        <input
                          id="grafana-default-to"
                          v-model.trim="editor.grafana.defaultTo"
                          :disabled="editor.grafana.toRequired"
                          :required="!editor.grafana.toRequired"
                          maxlength="128"
                          :placeholder="editor.grafana.toRequired ? '调用时由客户端必填' : '默认结束时间，如 now'"
                        />
                      </div>
                    </div>
                  </template>
                </div>
                <template v-if="isGrafanaEditor && editor.grafana">
                  <section class="parameter-group" aria-label="Grafana 查询语句">
                    <div class="parameter-heading">
                      <div class="group-title">
                        <h2>查询语句</h2>
                        <HelpHint label="查询语句说明" text="支持添加多条查询（A、B、C...），每条可选不同数据源。在语句中使用 {{host_ip}} 等占位符声明变量，点击右侧按钮自动提取参数。" />
                      </div>
                      <div class="button-row">
                        <button
                          type="button"
                          class="button small"
                          @click="extractGrafanaParameters"
                        >从占位符生成参数</button>
                        <button
                          type="button"
                          class="button small"
                          :disabled="editor.grafana.queries.length >= 20"
                          @click="addGrafanaQuery"
                        >＋ 添加查询</button>
                      </div>
                    </div>
                    <div class="form-content">
                      <div v-if="grafanaExtractionError" class="alert error" role="alert">{{ grafanaExtractionError }}</div>
                      <div
                        v-for="(q, qIndex) in editor.grafana.queries"
                        :key="qIndex"
                        class="grafana-query-card"
                      >
                        <div class="grafana-query-head">
                          <label class="ref-id-field">
                            查询标识
                            <input
                              v-model.trim="q.refId"
                              required
                              maxlength="32"
                              pattern="[A-Za-z][A-Za-z0-9_-]{0,31}"
                              :aria-label="`查询 ${qIndex + 1} 标识`"
                              placeholder="A"
                            />
                          </label>
                          <label class="grow">
                            数据源
                            <select
                              :value="q.datasourceUid"
                              required
                              :aria-label="`查询 ${qIndex + 1} 数据源`"
                              @change="selectQueryDatasource(q, ($event.target as HTMLSelectElement).value)"
                            >
                              <option value="" disabled>
                                {{ loadingDatasources ? "正在拉取数据源…" : "请选择数据源" }}
                              </option>
                              <option
                                v-if="q.datasourceUid && !grafanaDatasources.some((d) => d.uid === q.datasourceUid)"
                                :value="q.datasourceUid"
                              >
                                {{ q.datasourceName || q.datasourceUid }}（{{ q.datasourceType }}）
                              </option>
                              <option v-for="ds in grafanaDatasources" :key="ds.uid" :value="ds.uid">
                                {{ ds.name }}（{{ ds.type }}）{{ ds.isDefault ? " · 默认" : "" }}
                              </option>
                            </select>
                          </label>
                          <label class="data-type-field">
                            数据类型
                            <select
                              v-model="q.dataType"
                              required
                              :aria-label="`查询 ${q.refId || qIndex + 1} 数据类型`"
                              @change="clearQueryTypeSpecificFields(q)"
                            >
                              <option value="metrics">Metrics（指标）</option>
                              <option value="logs">Logs（日志）</option>
                            </select>
                          </label>
                          <button
                            v-if="editor.grafana.queries.length > 1"
                            type="button"
                            class="icon-button danger-text"
                            :aria-label="`删除查询 ${q.refId || qIndex + 1}`"
                            @click="removeGrafanaQuery(qIndex)"
                          >×</button>
                        </div>

                        <div v-if="q.datasourceType" class="grafana-query-options">
                          <label v-if="queryModeOptions(q.datasourceType).length">
                            查询模式
                            <select v-model="q.queryMode" :aria-label="`查询 ${q.refId || qIndex + 1} 模式`">
                              <option
                                v-for="opt in queryModeOptions(q.datasourceType)"
                                :key="opt.value"
                                :value="opt.value"
                              >{{ opt.label }}</option>
                            </select>
                          </label>
                          <label
                            v-if="['prometheus', 'loki', 'victorialogs'].includes(grafanaDatasourceFamily(q.datasourceType))"
                          >
                            步长 / Interval（选填）
                            <input
                              v-model.trim="q.interval"
                              maxlength="64"
                              placeholder="如 1m、30s"
                            />
                          </label>
                          <label v-if="grafanaDatasourceFamily(q.datasourceType) === 'prometheus'">
                            最大点数（选填）
                            <input
                              v-model.number="q.maxDataPoints"
                              type="number"
                              min="0"
                              max="100000"
                              placeholder="默认由数据源决定"
                            />
                          </label>
                          <label
                            v-if="['loki', 'victorialogs', 'elasticsearch'].includes(grafanaDatasourceFamily(q.datasourceType))"
                          >
                            最大返回条数（选填）
                            <input
                              v-model.number="q.maxLines"
                              type="number"
                              min="0"
                              max="10000"
                              placeholder="默认 100"
                            />
                          </label>
                          <label v-if="grafanaDatasourceFamily(q.datasourceType) === 'elasticsearch'">
                            时间字段（选填）
                            <input
                              v-model.trim="q.timeField"
                              maxlength="128"
                              placeholder="如 @timestamp"
                            />
                          </label>
                        </div>

                        <label>
                          查询语句
                          <textarea
                            v-model="q.expr"
                            required
                            rows="3"
                            maxlength="32768"
                            spellcheck="false"
                            class="code-input"
                            :aria-label="`查询 ${q.refId || qIndex + 1} 语句`"
                            :placeholder="queryExprPlaceholder(q.datasourceType)"
                            @input="grafanaExtractionError = ''"
                          />
                        </label>

                        <details
                          v-if="q.dataType === 'metrics'"
                          class="metrics-analysis-box"
                          :open="q.maxPoints !== undefined || q.changePointRatio !== undefined || q.maxChangePoints !== undefined"
                        >
                          <summary>
                            <span>时序降采样与突变点参数</span>
                            <span class="metrics-analysis-meta">{{ metricsAnalysisSummary(q) }}</span>
                          </summary>
                          <div class="metrics-analysis-body">
                            <div class="grafana-query-options">
                              <label>
                                LTTB 目标点数（maxPoints）
                                <input
                                  :value="q.maxPoints ?? ''"
                                  type="number"
                                  min="2"
                                  max="10000"
                                  step="1"
                                  :aria-label="`查询 ${q.refId || qIndex + 1} LTTB 目标点数`"
                                  placeholder="默认 240"
                                  @input="setOptionalQueryNumber(q, 'maxPoints', ($event.target as HTMLInputElement).value)"
                                />
                              </label>
                              <label>
                                突变阈值比例（changePointRatio）
                                <input
                                  :value="q.changePointRatio ?? ''"
                                  type="number"
                                  min="0.01"
                                  max="1"
                                  step="any"
                                  :aria-label="`查询 ${q.refId || qIndex + 1} 突变阈值比例`"
                                  placeholder="默认 0.3"
                                  @input="setOptionalQueryNumber(q, 'changePointRatio', ($event.target as HTMLInputElement).value)"
                                />
                              </label>
                              <label>
                                最多突变点数（maxChangePoints）
                                <input
                                  :value="q.maxChangePoints ?? ''"
                                  type="number"
                                  min="0"
                                  max="100"
                                  step="1"
                                  :aria-label="`查询 ${q.refId || qIndex + 1} 最多突变点数`"
                                  placeholder="默认 5"
                                  @input="setOptionalQueryNumber(q, 'maxChangePoints', ($event.target as HTMLInputElement).value)"
                                />
                              </label>
                            </div>
                            <p class="field-help">
                              全量原始数据上自动计算 stats 与突变点（changePoints），LTTB 降采样仅用于紧凑输出 series（东八区时间）。
                            </p>
                          </div>
                        </details>
                      </div>
                      <p v-if="grafanaPlaceholderNames.length" class="body-template-tokens">
                        <span>已识别占位符</span>
                        <code v-for="name in grafanaPlaceholderNames" :key="name">{{ `\{\{${name}\}\}` }}</code>
                      </p>
                    </div>
                  </section>

                  <section class="parameter-group" aria-label="查询变量参数">
                    <div class="parameter-heading">
                      <div class="group-title">
                        <h2>变量参数映射</h2>
                        <HelpHint label="变量参数说明" text="对应查询语句中的 {{变量}} 占位符（from / to 已内置）。可配置由客户端填写或固定值，勾选筛选参数表示至少填写一项。" />
                      </div>
                      <button type="button" class="button small" @click="addParameter()">
                        ＋ 添加参数
                      </button>
                    </div>
                    <div v-if="editor.parameters.length" class="table-wrap parameter-wrap">
                      <table class="parameter-table">
                        <thead>
                          <tr>
                            <th scope="col">参数名称 / 描述</th>
                            <th scope="col">类型</th>
                            <th scope="col">映射方式 / 固定值</th>
                            <th scope="col" class="checkbox-cell">筛选参数</th>
                            <th scope="col">填写要求</th>
                            <th scope="col" aria-label="操作"></th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr v-for="(p, index) in editor.parameters" :key="index">
                            <td>
                              <input
                                v-model="p.name"
                                required
                                :aria-label="`参数 ${index + 1} 名称`"
                                placeholder="与 {{占位符}} 同名"
                              /><input
                                v-model="p.description"
                                class="parameter-secondary-input"
                                :aria-label="`参数 ${index + 1} 描述`"
                                placeholder="参数描述（选填）"
                              />
                            </td>
                            <td>
                              <select
                                v-model="p.type"
                                class="parameter-type"
                                :aria-label="`参数 ${index + 1} 类型`"
                              >
                                <option
                                  v-for="(label, type) in grafanaParameterTypes"
                                  :key="type"
                                  :value="type"
                                >
                                  {{ label }} · {{ type }}
                                </option>
                              </select>
                            </td>
                            <td>
                              <select
                                v-model="p.fixed"
                                :aria-label="`参数 ${index + 1} 映射方式`"
                                @change="if (p.fixed) { p.queryFilter = false; p.nonEmpty = false; }"
                              >
                                <option :value="false">客户端填写</option>
                                <option :value="true">固定值</option>
                              </select><input
                                v-if="p.fixed"
                                v-model="p.value"
                                class="parameter-secondary-input"
                                type="text"
                                :aria-label="`参数 ${index + 1} 固定值`"
                                placeholder="输入固定值"
                                autocomplete="off"
                              />
                            </td>
                            <td class="checkbox-cell">
                              <input
                                v-model="p.queryFilter"
                                type="checkbox"
                                :disabled="p.fixed"
                                :aria-label="`参数 ${index + 1} 筛选参数`"
                              />
                            </td>
                            <td>
                              <select
                                v-if="!p.fixed"
                                :value="requirementOf(p)"
                                :aria-label="`参数 ${index + 1} 填写要求`"
                                @change="setRequirement(p, ($event.target as HTMLSelectElement).value)"
                              >
                                <option v-for="choice in requirements" :key="choice">{{ choice }}</option>
                              </select>
                              <span v-else class="muted">无需填写</span>
                            </td>
                            <td>
                              <button
                                type="button"
                                class="icon-button danger-text"
                                :aria-label="`删除参数 ${index + 1}`"
                                @click="editor.parameters.splice(index, 1)"
                              >×</button>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </section>
                </template>
                <template v-else>
                <section v-for="group in parameterGroups" :key="group.key"
                  class="parameter-group" :aria-label="group.label">
                  <div class="parameter-heading">
                    <div class="group-title">
                      <h2>{{ group.label }}</h2>
                      <HelpHint label="参数配置说明" :text="group.key === 'body'
                        ? '请求体字段可由客户端填写或使用固定值。勾选的筛选参数中，调用时至少填写一项。'
                        : '可添加查询参数、路径参数和请求头。勾选的筛选参数中，调用时至少填写一项。'" />
                    </div>
                    <button v-if="group.key !== 'body'"
                      type="button" class="button small" @click="addParameter()">
                      ＋ 添加参数
                    </button>
                  </div>
                    <div v-if="group.key === 'body'" class="mapped-body">
                      <p v-if="!canSendBody" class="body-method-warning">
                        {{ editor.method }} 不支持请求体。请调整请求方法或移除请求体配置。
                      </p>
                      <details v-if="!published || editor.bodyTemplate" class="body-template">
                        <summary>输入请求体</summary>
                        <label for="body-template" class="sr-only">JSON 请求体模板</label>
                        <textarea
                          id="body-template"
                          v-model.trim="editor.bodyTemplate"
                          rows="4"
                          maxlength="32768"
                          spellcheck="false"
                          class="code-input"
                          placeholder='{"items":["{{value}}"]}'
                          @input="bodyExtractionError = ''"
                        />
                        <div class="group-title"><button
                          v-if="canSendBody && !published"
                          type="button"
                          class="button small"
                          :disabled="!editor.bodyTemplate"
                          @click="extractBodyParameters"
                        >从占位符生成参数</button>
                          <HelpHint v-if="canSendBody && !published" label="模板占位符说明" text="占位符须加双引号。未填写的选填数组会省略对应条件或字段，其他参数须必填或固定。" /></div>
                        <div v-if="bodyExtractionError" class="alert error" role="alert">{{ bodyExtractionError }}</div>

                        <p v-if="bodyTemplateParameters.length" class="body-template-tokens">
                          <span>参数占位符</span>
                          <code
                            v-for="(p, index) in bodyTemplateParameters"
                            :key="index"
                            v-text="JSON.stringify(`{{${p.name}}}`)"
                          />
                        </p>
                      </details>
                    </div>
                    <div v-if="group.rows.length" class="table-wrap parameter-wrap">
                      <table class="parameter-table">
                        <thead>
                          <tr>
                            <th scope="col">参数名称 / 描述</th>
                            <th v-if="group.key !== 'body'" scope="col">位置</th>
                            <th scope="col">类型</th>
                            <th scope="col">映射方式 / 固定值</th>
                            <th scope="col" class="checkbox-cell">筛选参数</th>
                            <th scope="col">填写要求</th>
                            <th scope="col" aria-label="操作"></th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr v-for="{ p, index } in group.rows" :key="index">
                            <td>
                              <input
                                v-model="p.name"
                                required
                                :aria-label="`参数 ${index + 1} 名称`"
                                placeholder="参数名称"
                              /><input
                                v-model="p.description"
                                class="parameter-secondary-input"
                                :aria-label="`参数 ${index + 1} 描述`"
                                placeholder="参数描述（选填）"
                              />
                            </td>
                            <td v-if="group.key !== 'body'">
                              <select
                                v-model="p.in"
                                :aria-label="`参数 ${index + 1} 位置`"
                              >
                                <option value="query">查询参数</option>
                                <option value="path">路径参数</option>
                                <option value="header">请求头</option>
                              </select>
                            </td>
                            <td>
                              <select
                                v-model="p.type"
                                class="parameter-type"
                                :aria-label="`参数 ${index + 1} 类型`"

                              >
                                <option
                                  v-for="(label, type) in parameterTypes"
                                  :key="type"
                                  :value="type"
                                >
                                  {{ label }} · {{ type }}
                                </option>
                              </select>
                            </td>
                            <td>
                              <select
                                v-model="p.fixed"
                                :aria-label="`参数 ${index + 1} 映射方式`"
                                @change="if (p.fixed) { p.queryFilter = false; p.nonEmpty = false; }"
                              >
                                <option :value="false">客户端填写</option>
                                <option :value="true">固定值</option></select
                              ><input
                                v-if="p.fixed"
                                v-model="p.value"
                                class="parameter-secondary-input"
                                :type="p.in === 'header' ? 'password' : 'text'"
                                :aria-label="`参数 ${index + 1} 固定值`"
                                placeholder="输入固定值"
                                autocomplete="off"
                              />
                            </td>
                            <td class="checkbox-cell">
                              <input
                                v-model="p.queryFilter"
                                type="checkbox"
                                :disabled="p.fixed"
                                :aria-label="`参数 ${index + 1} 筛选参数`"
                              />
                            </td>
                            <td>
                              <select v-if="!p.fixed" :value="requirementOf(p)"
                                :aria-label="`参数 ${index + 1} 填写要求`"
                                @change="setRequirement(p, ($event.target as HTMLSelectElement).value)">
                                <option v-for="choice in requirements" :key="choice">{{ choice }}</option>
                              </select>
                              <span v-else class="muted">无需填写</span>
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
                </section>
                </template>
                <section aria-labelledby="output-fields-title">
                  <div class="parameter-heading">
                    <div class="group-title">
                      <h2 id="output-fields-title">输出字段</h2>
                      <HelpHint v-if="!availableOutputPaths.length && !published" label="输出字段说明" text="调试成功后可从响应中选取字段。" />
                    </div>
                    <button type="button" class="button small" @click="addOutputField">
                      ＋ 添加输出字段
                    </button>
                  </div>
                  <div v-if="availableOutputPaths.length" class="form-content">
                    <div class="output-picker">
                      <details>
                        <summary>
                          <span>从调试结果选字段</span>
                          <span v-if="selectedOutputPaths.length"> · 已选 {{ selectedOutputPaths.length }} 项</span>
                        </summary>
                        <div class="output-path-list" role="group" aria-label="可选字段">
                          <label
                            v-for="{ path, all, label } in availableOutputPaths"
                            :key="path"
                            :title="path + (all ? '（全部记录）' : '')"
                          >
                            <input
                              type="checkbox"
                              v-model="selectedOutputPaths"
                              :value="path"
                              :aria-label="path + (all ? '（全部记录）' : '')"
                            />
                            <span>
                              {{ label }}
                              <small v-if="selectedOutputPaths.includes(path)">{{ path + (all ? '（全部记录）' : '') }}</small>
                            </span>
                          </label>
                        </div>
                      </details>
                      <button
                        type="button"
                        class="button small"
                        :disabled="!selectedOutputPaths.length || selectedOutputPaths.length > outputFieldSlots"
                        @click="addResponseFields"
                      >
                        添加所选字段
                      </button>
                    </div>
                    <p v-if="selectedOutputPaths.length > outputFieldSlots" class="field-help" role="status">
                      最多配置 100 个输出字段，还可添加 {{ outputFieldSlots }} 项。请减少所选字段。
                    </p>
                  </div>
                  <div v-if="editor.outputFields?.length" class="form-content">
                    <div v-for="(field, index) in editor.outputFields" :key="index" class="output-field-card">
                      <div class="output-field">
                        <label>
                          输出名称
                          <input v-model.trim="field.name" required maxlength="128" placeholder="如 userId" />
                        </label>
                        <label>
                          值来源
                          <select
                            v-model="field.source"
                            @change="field.value = ''; field.processors = undefined"
                          >
                            <option value="response">上游响应</option>
                            <option value="fixed">固定值</option>
                          </select>
                        </label>
                        <label class="output-value">
                          {{ field.source === 'response' ? '响应路径' : '固定 JSON 值' }}
                          <input
                            v-model="field.value"
                            required
                            spellcheck="false"
                            maxlength="32768"
                            :placeholder="field.source === 'response' ? '如 /data/users/*/id' : '如 &quot;crm&quot;、true 或 1'"
                          />
                        </label>
                        <button
                          type="button"
                          class="icon-button danger-text"
                          :aria-label="`删除输出字段 ${index + 1}`"
                          @click="removeOutputField(index)"
                        >
                          ×
                        </button>
                      </div>
                      <GrafanaLogProcessors
                        v-if="field.source === 'response'"
                        v-model="field.processors"
                        :scope-label="field.name || `输出字段 ${index + 1}`"
                        :sample-hint="sampleForOutputField(field)"
                        :disabled="published || saving"
                      />
                    </div>
                  </div>
                </section>
                <section v-if="!isGrafanaEditor" aria-labelledby="approval-title">
                  <div class="parameter-heading">
                    <div class="group-title">
                      <h2 id="approval-title">调用审批</h2>
                      <HelpHint label="调用审批说明" text="开启后，MCP 调用先生成审批单，不会立即执行。审批通过后由平台按提交时的参数执行一次并保存结果。" />
                    </div>
                  </div>
                  <div class="form-content">
                    <label class="check-label"><input
                      type="checkbox"
                      :checked="!!editor.approval"
                      @change="setApproval(($event.target as HTMLInputElement).checked)"
                    />MCP 调用需审批</label>
                    <div v-if="editor.approval" class="approval-settings">
                      <label>审批人<select
                        :value="editor.approval.approver"
                        @change="setApproval(true, ($event.target as HTMLSelectElement).value as ApprovalRule['approver'])"
                      >
                        <option value="self">调用者本人</option>
                        <option value="admin">任一管理员</option>
                        <option value="user">指定用户</option>
                      </select></label>
                      <label v-if="editor.approval.approver === 'user'">审批人用户名<input
                        v-model.trim="editor.approval.approverName"
                        required
                        maxlength="32"
                        autocomplete="off"
                        placeholder="例如 zhangsan"
                      /></label>
                    </div>
                  </div>
                </section>
              </fieldset>
            </form>
            <section ref="trialPanel" class="panel call-panel" tabindex="-1" aria-labelledby="trial-title">
              <div class="panel-heading">
                <div class="group-title">
                  <h2 id="trial-title">调试</h2>
                  <HelpHint label="调用参数说明" text="填写参数值后发送请求。留空的选填项会省略，固定值由系统填入。" />
                </div>
                <button
                  class="button primary"
                  :disabled="selectedId === null || dirty || calling || (sharedTool && !!editor.approval)"
                  @click="runCall"
                >
                  {{ calling ? "请求中…" : "发送请求" }}
                </button>
              </div>
              <div class="form-content">
                <table v-if="trialRows.length" class="trial-table">
                  <thead><tr><th scope="col">参数</th><th scope="col">调试值</th></tr></thead>
                  <tbody>
                    <tr v-for="row in trialRows" :key="row.name">
                      <th scope="row">
                        {{ row.name }}
                        <small>
                          {{ parameterTypes[row.type] }} ·
                          {{ row.defaultHint ? `选填（默认 ${row.defaultHint}）` : requirementOf(row.parameter) }}
                        </small>
                      </th>
                      <td>
                        <select v-if="row.type === 'boolean'" v-model="row.value" :aria-label="`调试值 ${row.name}`">
                          <option value="">请选择</option><option value="true">true</option><option value="false">false</option>
                        </select>
                        <textarea v-else-if="row.type === 'array' || row.type === 'object'" v-model="row.value"
                          :aria-label="`调试值 ${row.name}`" rows="2" spellcheck="false"
                          :placeholder="row.type === 'array' ? '每行一个值，或粘贴 JSON 数组' : '输入 JSON 对象'" />
                        <input v-else-if="row.type === 'integer' || row.type === 'number'"
                          type="number" :step="row.type === 'integer' ? 1 : 'any'"
                          :value="row.value" @input="row.value = ($event.target as HTMLInputElement).value"
                          :aria-label="`调试值 ${row.name}`" :placeholder="`输入${parameterTypes[row.type]}`" />
                        <input v-else v-model="row.value" :aria-label="`调试值 ${row.name}`"
                          :placeholder="row.defaultHint ? `留空使用默认：${row.defaultHint}` : `输入${parameterTypes[row.type]}`" />
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p v-else>此接口无需填写调试参数。</p>
                <p v-if="selectedId === null || dirty" class="field-help">
                  保存草稿后可发送请求。
                </p>
                <p v-else-if="editor.approval" class="field-help">
                  {{ sharedTool ? "该接口需审批，请通过 MCP 调用提交，审批通过后由平台执行。" : "调试请求由维护者直接发送，不经过审批。" }}
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
                  <details open>
                    <summary>{{ sharedTool ? "调用结果" : isGrafanaEditor && result.rawBody ? "查询结果（已整理）" : "原始响应" }}</summary>
                    <pre>{{ formattedBody(result.body) }}</pre>
                  </details>
                  <details v-if="isGrafanaEditor && result.rawBody" class="grafana-raw-details">
                    <summary>Grafana 原始响应</summary>
                    <pre>{{ formattedBody(result.rawBody) }}</pre>
                  </details>
                  <section v-if="successfulResponse && editor.outputFields?.length" class="custom-output" aria-labelledby="custom-output-title">
                    <div class="output-result-heading">
                      <h3 id="custom-output-title">自定义结果</h3>
                      <button type="button" class="button small" :disabled="previewingOutput" @click="previewOutput">
                        {{ previewingOutput ? '生成中…' : '预览自定义结果' }}
                      </button>
                    </div>
                    <div v-if="currentOutput?.error" class="alert error" role="alert">{{ currentOutput.error }}</div>
                    <pre v-else-if="currentOutput?.body" aria-label="自定义结果 JSON">{{ currentOutput.body }}</pre>
                  </section>
                </div>
              </div>
            </section>
          </div>
          <aside class="preview-column">
            <section class="panel preview-panel">
              <div class="panel-heading">
                <h2>工具预览</h2>
              </div>
              <div v-if="!sharedTool" class="preview-tabs">
                <button
                  :class="{ selected: preview === 'schema' }"
                  @click="loadExport('schema')"
                >
                  输入结构</button
                ><button
                  v-if="!isGrafanaEditor"
                  :disabled="selectedId === null || dirty"
                  :class="{ selected: preview === 'openapi' }"
                  @click="loadExport('openapi')"
                >
                  OpenAPI</button>
              </div>
              <pre class="schema-preview">{{
                previewText || "正在生成配置…"
              }}</pre>
              <div v-if="!sharedTool" class="preview-footer">
                <span v-if="preview !== 'schema'">导出文件可能包含固定值。</span>
                <button
                  class="text-button"
                  :disabled="!previewText || (dirty && preview !== 'schema')"
                  @click="downloadExport"
                >
                  下载配置
                </button>
              </div>
            </section>
          </aside>
        </div>
      </template>
      <template v-else-if="view === 'approvals'">
        <header class="page-header">
          <div>
            <div class="breadcrumb">工作空间 / 调用审批</div>
            <h1>审批</h1>
            <p>MCP 调用需审批的接口时生成审批单，批准后由平台按提交时的参数执行一次并保存结果。</p>
          </div>
          <button class="button" @click="navigate('approvals')">刷新</button>
        </header>
        <section class="panel table-wrap">
          <table>
            <thead>
              <tr>
                <th>单号</th>
                <th>工具</th>
                <th>申请人</th>
                <th>审批人</th>
                <th>状态</th>
                <th>提交时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="item in approvals" :key="item.id">
                <tr>
                  <td class="nowrap">#{{ item.id }}</td>
                  <td><strong>{{ item.toolName }}</strong></td>
                  <td>{{ item.requester }}</td>
                  <td>{{ item.approver }}</td>
                  <td>
                    <span :class="['badge', approvalStatus[item.status][1]]">{{ approvalStatus[item.status][0] }}</span>
                  </td>
                  <td class="muted nowrap">{{ date(item.createdAt) }}</td>
                  <td class="nowrap">
                    <button class="text-button" :aria-expanded="openApproval?.id === item.id" @click="toggleApproval(item)">
                      {{ openApproval?.id === item.id ? "收起" : "详情" }}
                    </button>
                    <template v-if="item.canDecide">
                      <button class="text-button" :disabled="deciding" @click="decideApproval(item, true)">批准并执行</button>
                      <button class="text-button danger-text" :disabled="deciding" @click="decideApproval(item, false)">拒绝</button>
                    </template>
                  </td>
                </tr>
                <tr v-if="openApproval?.id === item.id" class="approval-detail">
                  <td colspan="7">
                    <p v-if="openApproval.method || openApproval.description" class="muted">
                      {{ openApproval.method }} · {{ openApproval.description }}
                    </p>
                    <h3>调用参数</h3>
                    <pre>{{ JSON.stringify(openApproval.arguments, null, 2) }}</pre>
                    <template v-if="openApproval.request">
                      <h3>最终请求</h3>
                      <pre>{{ openApproval.request.method }} {{ openApproval.request.url }}{{ openApproval.request.body ? "\n\n" + formattedBody(openApproval.request.body) : "" }}</pre>
                    </template>
                    <template v-if="openApproval.result">
                      <h3>执行结果 · HTTP {{ openApproval.result.statusCode }} · {{ openApproval.result.durationMs }} ms</h3>
                      <pre>{{ formattedBody(openApproval.result.body) }}</pre>
                    </template>
                    <div v-if="openApproval.error" class="alert error" role="alert">{{ openApproval.error }}</div>
                    <p class="muted">
                      有效期至 {{ date(openApproval.expiresAt) }}<template v-if="openApproval.decidedBy">，处理人 {{ openApproval.decidedBy }}<template v-if="openApproval.decidedAt">（{{ date(openApproval.decidedAt) }}）</template></template>
                    </p>
                  </td>
                </tr>
              </template>
              <tr v-if="!approvals.length">
                <td colspan="7" class="no-results">
                  暂无审批单。为接口开启“调用审批”后，MCP 调用会在这里等待审批。
                </td>
              </tr>
            </tbody>
          </table>
        </section>
      </template>
      <template v-else-if="view === 'tokens'">
        <header class="page-header">
          <div>
            <div class="breadcrumb">工作空间 / 客户端连接</div>
            <h1>连接与令牌</h1>
            <p>用个人令牌连接 MCP，调用已发布的自有工具和公共工具。</p>
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
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in tokens" :key="t.id">
                  <td>{{ t.name }}</td>
                  <td>
                    <code>{{ t.prefix }}••••••••</code>
                  </td>
                  <td>{{ date(t.createdAt) }}</td>
                  <td>
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
      <template v-else-if="view === 'grafana'">
        <GrafanaConnections
          :connections="grafanaConnections"
          :allowed-origins="allowedOrigins"
          @refresh="refreshGrafanaConnections"
          @notify="notify"
          @error="(msg) => (error = msg)"
        />
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
                <th>操作</th>
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
                <td>
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
