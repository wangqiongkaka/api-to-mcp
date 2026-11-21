import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { resolve } from "node:path";

const screenshot = (name: string) =>
  resolve(import.meta.dirname, "../../docs/screenshots", name);

async function registerMember(page: Page) {
  await page.getByRole("button", { name: "创建账户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(`browser_${Date.now()}`);
  await page.getByLabel("密码", { exact: true }).fill("browser-test-password-123");
  await page.getByRole("button", { name: "注册并进入工作台" }).click();
  await expect(page.getByRole("heading", { name: "接口库" })).toBeVisible();
}

test("注册、配置、试调用、发布、导出和令牌管理", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => consoleErrors.push(error.message));
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
  await page.screenshot({
    path: screenshot("login-desktop.png"),
    fullPage: true,
  });
  await registerMember(page);
  await expect(
    page.getByRole("button", { name: "用户管理", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "使用 Pod 查询示例" }).click();
  await page
    .getByLabel("接口地址", { exact: true })
    .fill("http://127.0.0.1:8099/ws/aquaman/kubernetes/pods/getPodInstance");
  await expect(page.getByRole("button", { name: "发送请求" })).toBeDisabled();
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "发送请求" })).toBeEnabled();
  await page.getByRole("button", { name: "试调用", exact: true }).click();
  await expect(page.getByRole("region", { name: "试调用" })).toBeFocused();
  await page.getByRole("button", { name: "发送请求" }).click();
  await expect(page.getByText("HTTP 200", { exact: true })).toBeVisible();
  await expect(page.locator(".call-result pre")).toContainText(
    '"showAllPods": "false"',
  );
  await expect(page.locator(".schema-preview")).toContainText("podIp");
  await expect(page.locator(".schema-preview")).not.toContainText(
    "showAllPods",
  );
  await expect(page.locator(".toast")).toHaveCount(0);
  await page.screenshot({
    path: screenshot("editor-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 393, height: 852 });
  await page.screenshot({
    path: screenshot("editor-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.setViewportSize({ width: 1440, height: 1060 });
  await page.getByRole("button", { name: "OpenAPI", exact: true }).click();
  await expect(page.locator(".schema-preview")).toContainText(
    '"openapi": "3.0.3"',
  );
  await page.getByRole("button", { name: "Higress", exact: true }).click();
  await expect(page.locator(".schema-preview")).toContainText(
    "showAllPods=false",
  );
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "下载配置" }).click();
  expect((await download).suggestedFilename()).toBe(
    "getPodInstance-higress.yaml",
  );
  await page.getByRole("button", { name: "发布工具", exact: true }).click();
  await expect(page.getByRole("button", { name: "停用工具" })).toBeVisible();
  await expect(page.getByRole("button", { name: "保存草稿" })).toBeDisabled();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await expect(page.locator(".tool-table")).toContainText("getPodInstance");
  await expect(page.locator(".tool-table")).toContainText("已发布");
  await page.screenshot({
    path: screenshot("library-desktop.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "连接与令牌", exact: true }).click();
  await page.getByLabel("令牌名称").fill("浏览器验收客户端");
  await page.getByRole("button", { name: "创建令牌", exact: true }).click();
  await expect(page.locator(".token-reveal")).toBeVisible();
  await expect(page.locator(".token-reveal code")).toContainText("mcp_");
  await page.getByRole("button", { name: "我已保存" }).click();
  await expect(page.locator(".token-reveal")).toHaveCount(0);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "撤销", exact: true }).click();
  await expect(
    page.getByText("尚未创建令牌。创建后即可连接你的 MCP 工具。"),
  ).toBeVisible();
  await page.getByRole("button", { name: "接口库", exact: true }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await page.getByRole("button", { name: "停用工具" }).click();
  await expect(page.getByRole("button", { name: "保存草稿" })).toBeEnabled();
  const callArgs = page.getByLabel("调用参数（JSON）");
  await page.getByLabel("参数 1 映射方式").selectOption({ label: "客户端填写" });
  await callArgs.fill('{"podIp":"10.60.144.183","showAllPods":true}');
  await page.getByLabel("参数 2 映射方式").selectOption({ label: "固定值" });
  await page.getByLabel("参数 2 固定值").fill("10.60.144.184");
  await page.getByRole("button", { name: "保存草稿" }).click();
  const call = page.waitForResponse((response) => response.url().endsWith("/call"));
  await page.getByRole("button", { name: "发送请求" }).click();
  const response = await call;
  expect(response.ok(), await response.text()).toBe(true);
  expect(JSON.parse(await callArgs.inputValue())).toEqual({ showAllPods: true });
  await expect(page.locator(".call-result pre")).toContainText(
    '"podIp": "10.60.144.184"',
  );
  await expect(page.locator(".call-result pre")).toContainText(
    '"showAllPods": "true"',
  );
  await page.getByRole("button", { name: "返回接口库" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "你的第一个 AI 工具，从这里开始" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
  expect(consoleErrors).toEqual([]);
});

test("从原始试调用响应多选字段并预览自定义输出", async ({ page }, testInfo) => {
  await page.goto("/");
  await registerMember(page);
  await page.getByRole("button", { name: "使用 Pod 查询示例" }).click();
  await page.getByLabel("接口地址", { exact: true }).fill("http://127.0.0.1:8099/pods");
  const output = page.getByRole("region", { name: "输出字段" });
  const add = output.getByRole("button", { name: "添加输出字段" });
  const trial = page.getByRole("button", { name: "发送请求" });
  let calls = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.endsWith("/call")) calls++;
  });
  await page.getByRole("button", { name: "保存草稿" }).click();
  await trial.click();
  const picker = output.locator("summary").filter({ hasText: "从试调用结果选字段" });
  const paths = output.getByRole("group", { name: "可选字段" });
  const addSelected = output.getByRole("button", { name: "添加所选字段" });
  await expect(addSelected).toBeDisabled();
  await picker.press("Enter");
  await expect(paths).toBeVisible();
  await expect(paths.locator("label")).toHaveText([
    "podIp",
    "showAllPods",
    "instance",
    "↳ name",
    "↳ namespace",
    "↳ status",
    "data",
    "↳ list",
    "↳ 全部记录",
    "↳ totalIp",
    "↳ 0",
    "↳ totalIp",
    "↳ 1",
    "↳ totalIp",
  ]);
  const all = paths.getByRole("checkbox", { name: "/data/list/*/totalIp（全部记录）", exact: true });
  await all.press("Space");
  await expect(all).toBeChecked();
  await paths.getByRole("checkbox", { name: "/data/list/0/totalIp", exact: true }).check();
  const status = paths.getByRole("checkbox", { name: "/instance/status", exact: true });
  await status.check();
  await status.uncheck();
  await expect(output.getByText("· 已选 2 项")).toBeVisible();
  await addSelected.click();
  await expect(paths.getByRole("checkbox", { checked: true })).toHaveCount(0);
  await expect(addSelected).toBeDisabled();
  await expect(output.getByLabel("输出名称").nth(1)).toHaveValue("totalIp_2");
  await expect(output.getByLabel("响应路径").nth(1)).toHaveValue("/data/list/0/totalIp");
  await output.getByLabel("输出名称").first().fill("ipList");
  await add.click();
  await output.getByLabel("输出名称").nth(2).fill("source");
  await output.getByLabel("值来源").nth(2).selectOption("fixed");
  await output.getByLabel("固定 JSON 值").fill('"inventory"');
  const custom = page.getByLabel("自定义结果 JSON");
  const want = '{"ipList":["10.0.0.1","10.0.0.2"],"source":"inventory","totalIp_2":"10.0.0.1"}';
  await page.getByRole("button", { name: "预览自定义结果" }).click();
  await expect(custom).toHaveText(want);
  expect(calls).toBe(1);
  await expect(page.getByText("原始响应", { exact: true })).toBeVisible();
  await page.getByText("原始响应", { exact: true }).click();
  await expect(page.locator(".toast")).toHaveCount(0);
  await output.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("output-fields-desktop.png") });
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByText("接口配置已保存")).toBeVisible();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await expect(output.getByLabel("响应路径").first()).toHaveValue("/data/list/*/totalIp");
  await expect(output.getByLabel("响应路径").nth(1)).toHaveValue("/data/list/0/totalIp");
  await expect(output.getByLabel("固定 JSON 值")).toHaveValue('"inventory"');
  await trial.click();
  await expect(custom).toHaveText(want);
  await page.setViewportSize({ width: 393, height: 852 });
  await picker.click();
  await expect(paths).toBeVisible();
  await expect(output.getByLabel("固定 JSON 值")).toBeVisible();
  await expect(page.locator(".toast")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await output.screenshot({ path: testInfo.outputPath("output-fields-mobile.png") });
  await output.getByRole("button", { name: "删除输出字段 3" }).click();
  await output.getByRole("button", { name: "删除输出字段 2" }).click();
  await output.getByRole("button", { name: "删除输出字段 1" }).click();
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "发送请求" })).toBeEnabled();
  await page.getByRole("button", { name: "发送请求" }).click();
  await expect(page.locator(".call-result pre")).toContainText('"showAllPods": "false"');
  await page.getByRole("button", { name: "返回接口库" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除", exact: true }).click();
});

test("请求体配置按需展开并保留已保存的模板", async ({ page }) => {
  await page.goto("/");
  await registerMember(page);
  await page.getByRole("button", { name: "新建接口" }).click();
  await page.getByLabel("工具名称").fill("searchItems");
  await page.getByLabel("用途描述").fill("按编号搜索条目");
  await page.getByLabel("接口地址", { exact: true }).fill("http://127.0.0.1:8099/items");
  const method = page.getByLabel("请求方法");
  await method.selectOption("POST");
  await page.getByRole("button", { name: "添加参数" }).click();
  await page.getByLabel("参数 1 名称").fill("itemId");

  const body = page.getByRole("region", { name: "JSON 请求体", exact: true });
  const location = page.getByLabel("参数 1 位置");
  const type = page.getByLabel("参数 1 类型");
  const wholeBody = page.getByLabel("接收完整 JSON 请求体");
  const template = page.getByLabel("JSON 请求体模板", { exact: true });
  await expect(template).toBeHidden();
  await wholeBody.check();
  await expect(body).toHaveCount(0);
  await expect(location.getByRole("option", { name: "请求体", includeHidden: true })).toBeDisabled();
  await wholeBody.uncheck();
  await location.selectOption("body");
  await expect(wholeBody).toBeDisabled();
  await expect(template).toBeHidden();
  await method.selectOption("GET");
  await expect(body).toContainText("GET 不支持请求体");
  await method.selectOption("POST");
  await expect(body).not.toContainText("不支持请求体");

  await type.selectOption({ label: "整数 · integer" });
  await expect(page.locator(".schema-preview")).toContainText('"type": "integer"');
  const save = page.getByRole("button", { name: "保存草稿" });
  const send = page.getByRole("button", { name: "发送请求" });
  await save.click();
  await expect(send).toBeEnabled();
  await page.getByLabel("调用参数（JSON）").fill('{"itemId":42}');
  await send.click();
  await expect(page.locator(".call-result pre")).toContainText('"itemId": 42');

  await body.getByText("配置嵌套 JSON 模板", { exact: true }).press("Enter");
  await expect(template).toBeVisible();
  await expect(body.locator("code")).toHaveText('"{{itemId}}"');
  const json = '{"items":["{{itemId}}"]}';
  await template.fill(json);
  await body.getByText("嵌套 JSON 模板（已填写）", { exact: true }).click();
  await expect(template).toBeHidden();

  await save.click();
  await expect(send).toBeEnabled();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await expect(template).toBeHidden();
  await body.getByText("嵌套 JSON 模板（已填写）", { exact: true }).click();
  await expect(template).toHaveValue(json);
  await expect(type).toHaveValue("integer");

  await page.getByRole("button", { name: "返回接口库" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await expect(page.getByRole("heading", { name: "你的第一个 AI 工具，从这里开始" })).toBeVisible();
});

test("从嵌套请求体模板生成参数并试调用", async ({ page }) => {
  await page.goto("/");
  await registerMember(page);
  await page.getByRole("button", { name: "新建接口" }).click();
  await page.getByLabel("工具名称").fill("searchInventory");
  await page.getByLabel("用途描述").fill("按 IP 和机房搜索条目");
  await page.getByLabel("接口地址", { exact: true }).fill("http://127.0.0.1:8099/items");
  await page.getByLabel("请求方法").selectOption("POST");
  const body = page.getByRole("region", { name: "JSON 请求体", exact: true });
  await body.getByText("配置嵌套 JSON 模板", { exact: true }).click();
  const template = body.getByLabel("JSON 请求体模板", { exact: true });
  const extract = body.getByRole("button", { name: "从占位符生成参数" });
  await expect(extract).toBeDisabled();

  await template.fill('{"hostIps":');
  await extract.click();
  await expect(body.getByRole("alert")).toContainText("有效的 JSON 对象");

  const json = JSON.stringify({
    network: { ipAddresses: "{{hostIps}}" },
    roomName: "{{room}}",
    tags: ["{{room}}", "prefix-{{ignored}}"],
  });
  const names = page.getByRole("textbox", { name: /^参数 \d+ 名称$/ });
  await template.fill(json);
  await extract.click();
  await expect(names).toHaveCount(2);
  await expect(names.first()).toHaveValue("hostIps");
  await expect(names.last()).toHaveValue("room");
  await expect(page.getByLabel("参数 1 位置")).toHaveValue("body");
  await expect(page.getByLabel("参数 1 必填")).toBeChecked();
  await page.getByLabel("参数 1 类型").selectOption("array");
  await extract.click();
  await expect(names).toHaveCount(2);
  await expect(page.getByLabel("参数 1 类型")).toHaveValue("array");

  await template.fill('{"newField":"{{zone}}","conflict":"{{HostIps}}"}');
  await extract.click();
  await expect(body.getByRole("alert")).toContainText("名称冲突");
  await expect(names).toHaveCount(2);
  await template.fill(json);
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByText("接口配置已保存")).toBeVisible();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await expect(page.getByLabel("参数 1 类型")).toHaveValue("array");
  await body.getByText("嵌套 JSON 模板（已填写）", { exact: true }).click();
  await expect(template).toHaveValue(json);

  await page.getByLabel("调用参数（JSON）").fill('{"hostIps":["192.0.2.1"],"room":"test-room"}');
  await page.getByRole("button", { name: "发送请求" }).click();
  await expect(page.getByText("HTTP 200", { exact: true })).toBeVisible();
  await expect(page.locator(".call-result pre")).toContainText('"ipAddresses": [');
  await expect(page.locator(".call-result pre")).toContainText('"192.0.2.1"');
  await expect(page.locator(".call-result pre")).toContainText('"roomName": "test-room"');
});

test("至少填写一个指定筛选参数才能试调用", async ({ page }, testInfo) => {
  await page.goto("/");
  await registerMember(page);
  const created = await page.request.post("/api/tools", {
    data: {
      name: "searchByFilter",
      description: "按主机 IP 或负责人查询",
      url: "http://127.0.0.1:8099/items",
      method: "POST",
      timeoutSeconds: 5,
      bodyTemplate: '{"queryParams":[{"ip":"{{host_ip}}"},{"owner":"{{admin}}"}]}',
      parameters: [
        { name: "host_ip", in: "body", type: "array" },
        { name: "admin", in: "body", type: "array" },
        { name: "limit", in: "query", type: "integer" },
      ],
    },
  });
  expect(created.status()).toBe(201);
  await page.reload();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  const ip = page.getByLabel("参数 1 筛选参数");
  const admin = page.getByLabel("参数 2 筛选参数");
  const arrayOptions = page.locator(".array-options summary").first();
  const nonEmpty = page.getByLabel("参数 1 数组不为空");
  await expect(nonEmpty).toBeHidden();
  await arrayOptions.press("Enter");
  await nonEmpty.check();
  await expect(arrayOptions).toHaveText("数组约束 · 已设置");
  await nonEmpty.uncheck();
  await arrayOptions.press("Enter");
  await expect(nonEmpty).toBeHidden();
  await expect(ip).not.toBeChecked();
  await ip.check();
  await admin.check();
  await page.getByLabel("参数 2 映射方式").selectOption({ label: "固定值" });
  await expect(admin).toHaveCount(0);
  await page.getByLabel("参数 2 映射方式").selectOption({ label: "客户端填写" });
  await expect(admin).not.toBeChecked();
  await admin.check();
  const message = "请至少填写一个筛选参数：host_ip、admin";
  await expect(page.locator(".schema-preview")).toContainText(message);
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByText("接口配置已保存")).toBeVisible();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await expect(ip).toBeChecked();
  await expect(admin).toBeChecked();
  const args = page.getByLabel("调用参数（JSON）");
  const run = page.getByRole("button", { name: "发送请求" });
  const emptyFilter = '{"host_ip":[],"limit":10}';
  await args.fill(emptyFilter);
  await run.click();
  await expect(page.getByRole("alert")).toHaveText(message);
  await expect(args).toHaveValue(emptyFilter);
  await expect(page.locator(".toast")).toHaveCount(0);
  await page.getByRole("heading", { name: /^参数映射/ }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("editor-flow-desktop.png") });

  await args.fill('{"admin":["owner"]}');
  await run.click();
  await expect(page.getByText("HTTP 200", { exact: true })).toBeVisible();
  await expect(page.locator(".call-result pre")).toContainText('"owner"');
  await expect(page.locator(".call-result pre")).not.toContainText('"ip"');
  await page.setViewportSize({ width: 393, height: 852 });
  const mappings = page.locator(".parameter-wrap");
  await mappings.scrollIntoViewIfNeeded();
  await mappings.evaluate((element) => { element.scrollLeft = element.scrollWidth; });
  await expect(ip).toBeInViewport();
  await expect(admin).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole("button", { name: "试调用", exact: true }).click();
  const trial = page.getByRole("region", { name: "试调用" });
  await expect(trial).toBeFocused();
  await expect(trial.getByRole("heading", { name: "试调用" })).toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath("editor-flow-mobile.png") });
});
