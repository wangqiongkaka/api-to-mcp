<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import HelpHint from "./HelpHint.vue";
import type { LogProcessor, LogProcessorType } from "./types";
import {
  generateRegexFromSelections,
  suggestFieldName,
  type SelectionMark,
} from "./regexGenerator";

const props = defineProps<{
  modelValue?: LogProcessor[];
  refId: string;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "update:modelValue", value: LogProcessor[] | undefined): void;
}>();

const typeLabels: Record<LogProcessorType, string> = {
  regex: "正则解析",
  json: "JSON 解析",
  split_array: "拆分数组",
  keep_fields: "保留字段",
  drop_fields: "剔除字段",
};

const processors = computed(() => props.modelValue ?? []);
const sample = ref("");
const previewBusy = ref(false);
const previewError = ref("");
const previewResult = ref<{
  input: Record<string, unknown>;
  rowCount?: number;
  output: unknown;
  steps: Array<{
    index: number;
    type: LogProcessorType;
    matched: boolean;
    rowCount?: number;
    output: unknown;
  }>;
} | null>(null);

interface RegexHelperState {
  open: boolean;
  sample: string;
  pendingStart: number;
  pendingEnd: number;
  pendingText: string;
  fieldName: string;
  marks: SelectionMark[];
  error: string;
}

const regexHelpers = reactive<Record<number, RegexHelperState>>({});

function helperOf(index: number): RegexHelperState {
  if (!regexHelpers[index]) {
    regexHelpers[index] = {
      open: false,
      sample: sample.value,
      pendingStart: 0,
      pendingEnd: 0,
      pendingText: "",
      fieldName: "",
      marks: [],
      error: "",
    };
  }
  return regexHelpers[index];
}

function toggleRegexHelper(index: number) {
  const h = helperOf(index);
  h.open = !h.open;
  if (h.open && !h.sample && sample.value) {
    h.sample = sample.value;
  }
  h.error = "";
}

function captureSelection(index: number, el: HTMLTextAreaElement | null) {
  if (!el) return;
  const h = helperOf(index);
  const start = el.selectionStart ?? 0;
  const end = el.selectionEnd ?? 0;
  if (end > start) {
    const text = h.sample.slice(start, end);
    h.pendingStart = start;
    h.pendingEnd = end;
    h.pendingText = text;
    h.fieldName = suggestFieldName(
      text,
      h.marks.map((m) => m.name),
    );
    h.error = "";
  }
}

function addSelectionMark(index: number) {
  const h = helperOf(index);
  h.error = "";
  if (!h.pendingText || h.pendingEnd <= h.pendingStart) {
    h.error = "请先在上方日志样例中用鼠标划选一段文本";
    return;
  }
  const name = h.fieldName.trim();
  if (!name) {
    h.error = "请输入该划选片段对应的字段名";
    return;
  }
  if (h.marks.some((m) => h.pendingStart < m.end && h.pendingEnd > m.start)) {
    h.error = "该选区与已标记片段重叠，请重新划选";
    return;
  }
  h.marks = [...h.marks, { start: h.pendingStart, end: h.pendingEnd, name }].sort(
    (a, b) => a.start - b.start,
  );
  h.pendingStart = 0;
  h.pendingEnd = 0;
  h.pendingText = "";
  h.fieldName = "";
}

function removeSelectionMark(index: number, markIndex: number) {
  const h = helperOf(index);
  h.marks = h.marks.filter((_, i) => i !== markIndex);
  h.error = "";
}

function applyGeneratedRegex(index: number) {
  const h = helperOf(index);
  h.error = "";
  try {
    const generated = generateRegexFromSelections(h.sample, h.marks);
    patchProcessor(index, { regex: generated.regex, keys: generated.keys });
    if (!sample.value.trim()) {
      sample.value = h.sample;
    }
    void runPreviewWith(
      processors.value.map((item, i) =>
        i === index ? { ...item, regex: generated.regex, keys: generated.keys } : item,
      ),
      h.sample,
    );
  } catch (e) {
    h.error = e instanceof Error ? e.message : "生成正则失败";
  }
}

function updateList(next: LogProcessor[]) {
  emit("update:modelValue", next.length ? next : undefined);
}

function addProcessor(type: LogProcessorType) {
  if (processors.value.length >= 10) return;
  const item: LogProcessor =
    type === "json"
      ? {
          type: "json",
          sourceKey: "",
          expandDepth: 1,
          expandConnector: ".",
          prefix: "",
          keepSource: false,
          keepSourceIfParseError: true,
        }
      : type === "regex"
        ? {
            type: "regex",
            sourceKey: "",
            regex: "",
            keys: [],
            keepSource: false,
            keepSourceIfParseError: true,
          }
        : type === "split_array"
          ? {
              type: "split_array",
              sourceKey: "",
              targetKey: "",
              keepSource: false,
              keepSourceIfParseError: true,
            }
          : {
              type,
              fields: [],
            };
  updateList([...processors.value, item]);
}

function patchProcessor(index: number, patch: Partial<LogProcessor>) {
  const next = processors.value.map((item, i) => (i === index ? { ...item, ...patch } : item));
  updateList(next);
}

function removeProcessor(index: number) {
  const next = processors.value.filter((_, i) => i !== index);
  delete regexHelpers[index];
  updateList(next);
  if (!next.length) previewResult.value = null;
}

function moveProcessor(index: number, delta: -1 | 1) {
  const target = index + delta;
  if (target < 0 || target >= processors.value.length) return;
  const next = [...processors.value];
  [next[index], next[target]] = [next[target], next[index]];
  updateList(next);
}

function splitList(raw: string): string[] {
  return raw
    .split(/[,，\n\r]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

async function runPreviewWith(list: LogProcessor[], sampleText: string) {
  if (!sampleText.trim() || !list.length) return;
  previewError.value = "";
  previewResult.value = null;
  previewBusy.value = true;
  try {
    const response = await fetch("/api/grafana/log-processors/preview", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        sample: sampleText,
        processors: list,
      }),
    });
    const data = await response.json();
    if (!response.ok) {
      throw new Error(data.error || `验证失败（${response.status}）`);
    }
    previewResult.value = data;
  } catch (e) {
    previewError.value = e instanceof Error ? e.message : "验证失败，请检查规则配置";
  } finally {
    previewBusy.value = false;
  }
}

async function runPreview() {
  await runPreviewWith(processors.value, sample.value);
}
</script>

<template>
  <details class="log-processor-box" :open="processors.length > 0">
    <summary>
      <span>日志处理（JSON / 正则 / 拆数组 / 字段过滤）</span>
      <span v-if="processors.length" class="badge neutral">已配置 {{ processors.length }} 步</span>
    </summary>
    <div class="log-processor-body">
      <div class="log-processor-toolbar">
        <div class="group-title">
          <span class="muted">按顺序处理每条 Logs 记录，精简后再返回给大模型分析。</span>
          <HelpHint
            label="SLS 日志处理说明"
            text="参考阿里云 SLS 处理器链：支持正则解析（兼容前瞻/后顾断言与划选生成）、JSON 展开、JSONArray 拆分为多行、以及字段保留/剔除。原始字段留空时自动识别 _msg / Line / content / message。"
          />
        </div>
        <div v-if="!disabled" class="button-row">
          <button
            type="button"
            class="button small"
            :disabled="processors.length >= 10"
            @click="addProcessor('regex')"
          >＋ 正则解析</button>
          <button
            type="button"
            class="button small"
            :disabled="processors.length >= 10"
            @click="addProcessor('json')"
          >＋ JSON 解析</button>
          <button
            type="button"
            class="button small"
            :disabled="processors.length >= 10"
            @click="addProcessor('split_array')"
          >＋ 拆分数组</button>
          <button
            type="button"
            class="button small"
            :disabled="processors.length >= 10"
            @click="addProcessor('keep_fields')"
          >＋ 保留字段</button>
          <button
            type="button"
            class="button small"
            :disabled="processors.length >= 10"
            @click="addProcessor('drop_fields')"
          >＋ 剔除字段</button>
        </div>
      </div>

      <div v-if="processors.length" class="log-processor-list">
        <div
          v-for="(p, idx) in processors"
          :key="idx"
          class="log-processor-step"
        >
          <div class="log-processor-step-head">
            <div class="group-title">
              <strong>#{{ idx + 1 }}</strong>
              <span :class="['badge', p.type === 'keep_fields' || p.type === 'drop_fields' ? 'neutral' : 'data-type logs']">
                {{ typeLabels[p.type] }}
              </span>
            </div>
            <div v-if="!disabled" class="button-row">
              <button
                type="button"
                class="icon-button small-icon"
                :disabled="idx === 0"
                :aria-label="`查询 ${refId} 处理器 ${idx + 1} 上移`"
                @click="moveProcessor(idx, -1)"
              >↑</button>
              <button
                type="button"
                class="icon-button small-icon"
                :disabled="idx === processors.length - 1"
                :aria-label="`查询 ${refId} 处理器 ${idx + 1} 下移`"
                @click="moveProcessor(idx, 1)"
              >↓</button>
              <button
                type="button"
                class="icon-button danger-text small-icon"
                :aria-label="`删除查询 ${refId} 处理器 ${idx + 1}`"
                @click="removeProcessor(idx)"
              >×</button>
            </div>
          </div>

          <template v-if="p.type === 'json'">
            <div class="log-processor-grid">
              <label>
                原始字段（选填）
                <input
                  :value="p.sourceKey ?? ''"
                  maxlength="128"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 原始字段`"
                  placeholder="留空自动识别 _msg / Line / content"
                  @input="patchProcessor(idx, { sourceKey: ($event.target as HTMLInputElement).value })"
                />
              </label>
              <label>
                展开深度（0 为全部展开）
                <input
                  type="number"
                  min="0"
                  max="20"
                  :value="p.expandDepth ?? 1"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 展开深度`"
                  @input="patchProcessor(idx, { expandDepth: Number(($event.target as HTMLInputElement).value) || 0 })"
                />
              </label>
              <label>
                嵌套连接符
                <input
                  :value="p.expandConnector ?? '.'"
                  maxlength="8"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 连接符`"
                  placeholder="."
                  @input="patchProcessor(idx, { expandConnector: ($event.target as HTMLInputElement).value })"
                />
              </label>
              <label>
                字段前缀（选填）
                <input
                  :value="p.prefix ?? ''"
                  maxlength="64"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 前缀`"
                  placeholder="如 payload"
                  @input="patchProcessor(idx, { prefix: ($event.target as HTMLInputElement).value })"
                />
              </label>
            </div>
            <div class="log-processor-checks">
              <label class="check-label compact-check">
                <input
                  type="checkbox"
                  :checked="!!p.keepSource"
                  @change="patchProcessor(idx, { keepSource: ($event.target as HTMLInputElement).checked })"
                />
                解析成功时保留原始字段
              </label>
              <label class="check-label compact-check">
                <input
                  type="checkbox"
                  :checked="p.keepSourceIfParseError !== false"
                  @change="patchProcessor(idx, { keepSourceIfParseError: ($event.target as HTMLInputElement).checked })"
                />
                解析失败时保留原始字段
              </label>
            </div>
          </template>

          <template v-else-if="p.type === 'split_array'">
            <div class="log-processor-grid two-col">
              <label>
                数组原始字段（选填）
                <input
                  :value="p.sourceKey ?? ''"
                  maxlength="128"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 原始字段`"
                  placeholder="如 events；留空自动识别 _msg / Line / content"
                  @input="patchProcessor(idx, { sourceKey: ($event.target as HTMLInputElement).value })"
                />
              </label>
              <label>
                拆分目标字段名（选填）
                <input
                  :value="p.targetKey ?? ''"
                  maxlength="128"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 目标字段`"
                  placeholder="留空时对象元素直接合并进当前行，标量放入 item"
                  @input="patchProcessor(idx, { targetKey: ($event.target as HTMLInputElement).value })"
                />
              </label>
            </div>
            <div class="log-processor-checks">
              <label class="check-label compact-check">
                <input
                  type="checkbox"
                  :checked="!!p.keepSource"
                  @change="patchProcessor(idx, { keepSource: ($event.target as HTMLInputElement).checked })"
                />
                拆分成功时保留原数组字段
              </label>
              <label class="check-label compact-check">
                <input
                  type="checkbox"
                  :checked="p.keepSourceIfParseError !== false"
                  @change="patchProcessor(idx, { keepSourceIfParseError: ($event.target as HTMLInputElement).checked })"
                />
                非数组或解析失败时保留原字段
              </label>
            </div>
          </template>

          <template v-else-if="p.type === 'regex'">
            <div class="log-processor-grid two-col">
              <label>
                原始字段（选填）
                <input
                  :value="p.sourceKey ?? ''"
                  maxlength="128"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 原始字段`"
                  placeholder="留空自动识别 _msg / Line / content"
                  @input="patchProcessor(idx, { sourceKey: ($event.target as HTMLInputElement).value })"
                />
              </label>
              <label>
                提取字段名（逗号分隔，命名捕获组可留空）
                <input
                  :value="(p.keys ?? []).join(', ')"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 提取字段`"
                  placeholder="例如：ip, level, payload"
                  @input="patchProcessor(idx, { keys: splitList(($event.target as HTMLInputElement).value) })"
                />
              </label>
            </div>
            <div>
              <div class="url-heading">
                <label :for="`regex-input-${refId}-${idx}`">
                  正则表达式（支持 (...)、(?P&lt;名&gt;...) 及前瞻/后顾断言）
                </label>
                <button
                  v-if="!disabled"
                  type="button"
                  class="text-button"
                  @click="toggleRegexHelper(idx)"
                >
                  {{ helperOf(idx).open ? "收起划选生成" : "划选样例自动生成正则" }}
                </button>
              </div>
              <input
                :id="`regex-input-${refId}-${idx}`"
                class="code-input"
                required
                maxlength="4096"
                spellcheck="false"
                :value="p.regex ?? ''"
                :aria-label="`查询 ${refId} 处理器 ${idx + 1} 正则表达式`"
                placeholder='例如：^(\S+)\s+\[(\w+)\]\s+(\{.*\})$'
                @input="patchProcessor(idx, { regex: ($event.target as HTMLInputElement).value })"
              />
            </div>

            <div v-if="helperOf(idx).open && !disabled" class="regex-helper">
              <label>
                1. 粘贴日志样例，用鼠标划选要提取的文本片段
                <textarea
                  v-model="helperOf(idx).sample"
                  rows="2"
                  spellcheck="false"
                  class="code-input"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 划选样例`"
                  placeholder='例如：10.20.30.40 [ERROR] {"trace_id":"tr-1","msg":"timeout"}'
                  @select="captureSelection(idx, $event.target as HTMLTextAreaElement)"
                  @mouseup="captureSelection(idx, $event.target as HTMLTextAreaElement)"
                  @keyup="captureSelection(idx, $event.target as HTMLTextAreaElement)"
                  @input="helperOf(idx).marks = []; helperOf(idx).pendingText = ''"
                />
              </label>
              <div class="regex-mark-row">
                <div class="grow">
                  <span class="muted">当前划选：</span>
                  <code v-if="helperOf(idx).pendingText">{{ helperOf(idx).pendingText }}</code>
                  <span v-else class="muted">请在上方文本框拖拽选中片段</span>
                </div>
                <input
                  v-model.trim="helperOf(idx).fieldName"
                  class="mark-name-input"
                  maxlength="64"
                  :aria-label="`查询 ${refId} 处理器 ${idx + 1} 划选字段名`"
                  placeholder="字段名，如 ip"
                />
                <button
                  type="button"
                  class="button small"
                  :disabled="!helperOf(idx).pendingText"
                  @click="addSelectionMark(idx)"
                >标记为字段</button>
              </div>
              <div v-if="helperOf(idx).marks.length" class="regex-marks">
                <span
                  v-for="(m, mIdx) in helperOf(idx).marks"
                  :key="`${m.start}-${m.end}`"
                  class="regex-mark-chip"
                >
                  <strong>{{ m.name }}</strong>
                  <code>{{ helperOf(idx).sample.slice(m.start, m.end) }}</code>
                  <button
                    type="button"
                    class="icon-button small-icon"
                    :aria-label="`移除标记 ${m.name}`"
                    @click="removeSelectionMark(idx, mIdx)"
                  >×</button>
                </span>
              </div>
              <div v-if="helperOf(idx).error" class="alert error" role="alert">{{ helperOf(idx).error }}</div>
              <div class="button-row">
                <button
                  type="button"
                  class="button small primary"
                  :disabled="!helperOf(idx).marks.length"
                  @click="applyGeneratedRegex(idx)"
                >生成正则并验证</button>
                <button
                  v-if="helperOf(idx).marks.length"
                  type="button"
                  class="text-button"
                  @click="helperOf(idx).marks = []"
                >清空标记</button>
              </div>
            </div>

            <div class="log-processor-checks">
              <label class="check-label compact-check">
                <input
                  type="checkbox"
                  :checked="!!p.keepSource"
                  @change="patchProcessor(idx, { keepSource: ($event.target as HTMLInputElement).checked })"
                />
                解析成功时保留原始字段
              </label>
              <label class="check-label compact-check">
                <input
                  type="checkbox"
                  :checked="p.keepSourceIfParseError !== false"
                  @change="patchProcessor(idx, { keepSourceIfParseError: ($event.target as HTMLInputElement).checked })"
                />
                不匹配时保留原始字段
              </label>
            </div>
          </template>

          <template v-else>
            <label>
              {{ p.type === "keep_fields" ? "仅保留以下字段（逗号分隔）" : "剔除以下字段（逗号分隔）" }}
              <input
                required
                :value="(p.fields ?? []).join(', ')"
                :aria-label="`查询 ${refId} 处理器 ${idx + 1} 字段列表`"
                placeholder="例如：Time, ip, level, trace_id, msg"
                @input="patchProcessor(idx, { fields: splitList(($event.target as HTMLInputElement).value) })"
              />
            </label>
          </template>
        </div>
      </div>

      <div v-if="processors.length" class="log-processor-preview">
        <div class="url-heading">
          <label :for="`log-sample-${refId}`">日志样例验证（在线测试处理效果）</label>
          <button
            type="button"
            class="button small"
            :disabled="previewBusy || !sample.trim()"
            @click="runPreview"
          >
            {{ previewBusy ? "验证中…" : "验证处理效果" }}
          </button>
        </div>
        <textarea
          :id="`log-sample-${refId}`"
          v-model="sample"
          rows="2"
          spellcheck="false"
          class="code-input"
          :aria-label="`查询 ${refId} 日志样例`"
          placeholder='粘贴一条原始日志或单行 JSON，例如：10.0.0.8 [ERROR] {"trace_id":"t-1","msg":"timeout","debug":"noise"}'
        />
        <div v-if="previewError" class="alert error" role="alert">{{ previewError }}</div>
        <div v-if="previewResult" class="log-preview-output">
          <div class="log-preview-steps">
            <span
              v-for="s in previewResult.steps"
              :key="s.index"
              :class="['badge', s.matched ? 'success' : 'warning']"
            >
              #{{ s.index + 1 }} {{ typeLabels[s.type] }} ·
              {{ s.type === "split_array" && s.matched ? `拆出 ${s.rowCount ?? 0} 行` : s.matched ? "已匹配" : "未匹配" }}
            </span>
          </div>
          <pre :aria-label="`查询 ${refId} 日志处理预览结果`">{{ JSON.stringify(previewResult.output, null, 2) }}</pre>
        </div>
      </div>
    </div>
  </details>
</template>
