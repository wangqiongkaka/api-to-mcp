export function responsePaths(body: string) {
  let response: unknown;
  try {
    response = JSON.parse(body);
  } catch {
    return [];
  }

  const paths = new Map<string, boolean>();
  const wildcards = new Set<string>();
  function visit(value: unknown, path: string, all: string) {
    // shortcut: 候选列表最多 500 条，大响应的其他路径可手动填写。
    if (paths.size >= 500) return;
    if (path) {
      if (!paths.has(path)) paths.set(path, false);
      if (all !== path && paths.size < 500) paths.set(all, true);
    }
    if (Array.isArray(value)) {
      const wildcard = `${all}/*`;
      if (value.length) wildcards.add(wildcard);
      for (const [index, item] of value.entries()) {
        if (paths.size >= 500) break;
        visit(item, `${path}/${index}`, wildcard);
      }
    } else if (value !== null && typeof value === "object") {
      for (const [key, item] of Object.entries(value)) {
        if (paths.size >= 500) break;
        const token = key.replaceAll("~", "~0").replaceAll("/", "~1");
        visit(item, `${path}/${token}`, `${all}/${token}`);
      }
    }
  }
  visit(response, "", "");

  const byParent = new Map<string, string[]>();
  for (const path of paths.keys()) {
    const parent = path.slice(0, path.lastIndexOf("/"));
    const siblings = byParent.get(parent) ?? [];
    if (wildcards.has(path)) siblings.unshift(path);
    else siblings.push(path);
    byParent.set(parent, siblings);
  }

  const options: { path: string; all: boolean; label: string }[] = [];
  function appendOptions(parent: string, depth: number) {
    for (const path of byParent.get(parent) ?? []) {
      const key = path.slice(parent.length + 1).replaceAll("~1", "/").replaceAll("~0", "~");
      const name = key && key === key.trim() ? key : JSON.stringify(key);
      // shortcut: 缩进最多八级以保留窄屏空间，完整路径可在选中后查看。
      const indent = depth ? "\u00a0\u00a0".repeat(Math.min(depth, 8)) + "↳ " : "";
      const label = indent + (wildcards.has(path) ? "全部记录" : name);
      options.push({ path, all: paths.get(path) === true, label });
      appendOptions(path, depth + 1);
    }
  }
  appendOptions("", 0);
  return options;
}
