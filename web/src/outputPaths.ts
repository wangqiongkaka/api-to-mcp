export function responsePaths(body: string) {
  let response: unknown;
  try {
    response = JSON.parse(body);
  } catch {
    return [];
  }

  const paths = new Map<string, boolean>();
  function visit(value: unknown, path: string, all: string) {
    // shortcut: 候选列表最多 500 条，大响应的其他路径可手动填写。
    if (paths.size >= 500) return;
    if (path) {
      if (!paths.has(path)) paths.set(path, false);
      if (all !== path && paths.size < 500) paths.set(all, true);
    }
    if (Array.isArray(value)) {
      for (const [index, item] of value.entries()) {
        if (paths.size >= 500) break;
        visit(item, `${path}/${index}`, `${all}/*`);
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
  return Array.from(paths, ([path, all]) => ({ path, all }));
}
