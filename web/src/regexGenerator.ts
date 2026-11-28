export interface SelectionMark {
  start: number;
  end: number;
  name: string;
}

function escapeRegexLiteral(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function encodeAnchor(text: string): string {
  // 将连续空白折叠为 \s+，既贴合真实日志对齐空格，又避免因空格数微调导致失配
  return text
    .split(/(\s+)/)
    .map((part) => (part && /^\s+$/.test(part) ? "\\s+" : escapeRegexLiteral(part)))
    .join("");
}

function isValidJSONOf(text: string, prefix: "{" | "["): boolean {
  const trimmed = text.trim();
  if (!trimmed.startsWith(prefix)) return false;
  try {
    const parsed = JSON.parse(trimmed);
    return prefix === "[" ? Array.isArray(parsed) : parsed !== null && typeof parsed === "object" && !Array.isArray(parsed);
  } catch {
    return false;
  }
}

export function suggestFieldName(selectedText: string, existingNames: string[]): string {
  const text = selectedText.trim();
  let base = "field";
  if (/^\d{1,3}(?:\.\d{1,3}){3}$/.test(text)) base = "ip";
  else if (/^(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)$/i.test(text)) base = "level";
  else if (/^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)$/.test(text)) base = "method";
  else if (/^[1-5]\d{2}$/.test(text)) base = "status";
  else if (/^\d{4}[-/]\d{2}[-/]\d{2}[T\s]\d{2}:\d{2}:\d{2}/.test(text)) base = "time";
  else if (isValidJSONOf(text, "{")) base = "payload";
  else if (isValidJSONOf(text, "[")) base = "items";
  else if (/^\d+(?:\.\d+)?$/.test(text)) base = "num";

  const used = new Set(existingNames.map((n) => n.toLowerCase()));
  if (!used.has(base)) return base;
  for (let i = 2; i < 100; i++) {
    const candidate = `${base}_${i}`;
    if (!used.has(candidate)) return candidate;
  }
  return `field_${existingNames.length + 1}`;
}

function inferCapturePattern(
  selected: string,
  prevChar: string,
  nextChar: string,
  atEnd: boolean,
): string {
  if (/^\d{1,3}(?:\.\d{1,3}){3}$/.test(selected)) {
    return "(\\d{1,3}(?:\\.\\d{1,3}){3})";
  }
  if (/^\d+$/.test(selected)) {
    return "(\\d+)";
  }
  if (/^\d+\.\d+$/.test(selected)) {
    return "(\\d+(?:\\.\\d+)?)";
  }
  if (isValidJSONOf(selected, "{")) {
    return atEnd ? "(\\{.*\\})" : "(\\{.*?\\})";
  }
  if (isValidJSONOf(selected, "[")) {
    return atEnd ? "(\\[.*\\])" : "(\\[.*?\\])";
  }
  if (prevChar === '"' && nextChar === '"' && !selected.includes('"')) {
    return '([^"]+)';
  }
  if (prevChar === "'" && nextChar === "'" && !selected.includes("'")) {
    return "([^']+)";
  }
  if (prevChar === "[" && nextChar === "]" && !selected.includes("]")) {
    return "([^\\]]+)";
  }
  if (prevChar === "(" && nextChar === ")" && !selected.includes(")")) {
    return "([^)]+)";
  }
  if (!/\s/.test(selected)) {
    if (nextChar && !/\s/.test(nextChar) && !selected.includes(nextChar)) {
      return `([^\\s${escapeRegexLiteral(nextChar)}]+)`;
    }
    return "(\\S+)";
  }
  if (nextChar && !/\s/.test(nextChar) && !selected.includes(nextChar)) {
    return `([^${escapeRegexLiteral(nextChar)}]+)`;
  }
  return atEnd ? "(.+)" : "(.+?)";
}

export function generateRegexFromSelections(
  sample: string,
  marks: SelectionMark[],
): { regex: string; keys: string[] } {
  if (!sample) throw new Error("请先输入日志样例");
  if (!marks.length) throw new Error("请至少在日志样例中划选一个提取字段");

  const sorted = [...marks].sort((a, b) => a.start - b.start);
  for (let i = 0; i < sorted.length; i++) {
    const m = sorted[i];
    if (m.start < 0 || m.end <= m.start || m.end > sample.length) {
      throw new Error("存在无效的划选范围，请重新划选");
    }
    if (!m.name.trim()) {
      throw new Error("请为每个划选片段填写字段名称");
    }
    if (i > 0 && m.start < sorted[i - 1].end) {
      throw new Error("划选片段不能重叠，请移除重叠项后重试");
    }
  }

  let pattern = "";
  let cursor = 0;
  const keys: string[] = [];

  for (let i = 0; i < sorted.length; i++) {
    const mark = sorted[i];
    const prefix = sample.slice(cursor, mark.start);
    if (i === 0) {
      if (mark.start === 0) {
        pattern += "^";
      } else if (prefix.length <= 48) {
        pattern += "^" + encodeAnchor(prefix);
      } else {
        // 起始锚点过长时只取紧邻左侧最多 24 个字符作为锚点，避免正则过度脆弱
        pattern += encodeAnchor(prefix.slice(-24));
      }
    } else {
      pattern += encodeAnchor(prefix);
    }

    const selected = sample.slice(mark.start, mark.end);
    const prevChar = mark.start > 0 ? sample[mark.start - 1] : "";
    const nextChar = mark.end < sample.length ? sample[mark.end] : "";
    const atEnd = mark.end === sample.length;
    pattern += inferCapturePattern(selected, prevChar, nextChar, atEnd);
    keys.push(mark.name.trim());
    cursor = mark.end;
  }

  const tail = sample.slice(cursor);
  if (tail.length === 0) {
    pattern += "$";
  } else if (tail.length <= 24) {
    pattern += encodeAnchor(tail) + "$";
  } else {
    // 尾部保留紧邻右侧的短定界锚点（如引号/括号/空格）即可精确定位最后一个字段
    const shortTail = tail.slice(0, 8);
    pattern += encodeAnchor(shortTail);
  }

  return { regex: pattern, keys };
}
