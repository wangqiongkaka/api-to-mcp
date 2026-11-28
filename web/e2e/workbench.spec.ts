import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const envConfig = Object.fromEntries(
  readFileSync(resolve(import.meta.dirname, "../../.env"), "utf8")
    .split("\n")
    .filter((line) => line && !line.startsWith("#"))
    .map((line) => {
      const at = line.indexOf("=");
      return [line.slice(0, at), line.slice(at + 1)];
    }),
);

const screenshot = (name: string) =>
  resolve(import.meta.dirname, "../../docs/screenshots", name);

async function registerMember(page: Page) {
  await page.getByRole("button", { name: "创建账户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(`browser_${Date.now()}`);
  await page.getByLabel("密码", { exact: true }).fill("browser-test-password-123");
  await page.getByRole("button", { name: "注册并进入工作台" }).click();
  await expect(page.getByRole("heading", { name: "接口库" })).toBeVisible();
}

test("注册、配置、调试、发布、导出和令牌管理", async ({ page }) => {
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
  await page.getByRole("button", { name: "调试", exact: true }).click();
  await expect(page.getByRole("region", { name: "调试" })).toBeFocused();
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
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "下载配置" }).click();
  expect((await download).suggestedFilename()).toBe(
    "getPodInstance-openapi.json",
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
  await page.getByLabel("参数 1 映射方式").selectOption({ label: "客户端填写" });
  await page.getByLabel("调试值 showAllPods").selectOption("true");
  await page.getByLabel("参数 2 映射方式").selectOption({ label: "固定值" });
  await page.getByLabel("参数 2 固定值").fill("10.60.144.184");
  await page.getByRole("button", { name: "保存草稿" }).click();
  const call = page.waitForResponse((response) => response.url().endsWith("/call"));
  await page.getByRole("button", { name: "发送请求" }).click();
  const response = await call;
  expect(response.ok(), await response.text()).toBe(true);
  await expect(page.getByLabel("调试值 podIp")).toHaveCount(0);
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

test("从原始调试响应多选字段并预览自定义输出", async ({ page }, testInfo) => {
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
  const picker = output.locator("summary").filter({ hasText: "从调试结果选字段" });
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
  await expect(wholeBody).toHaveCount(0);
  await expect(body.getByRole("button", { name: "添加请求体参数" })).toHaveCount(0);
  await expect(location.getByRole("option", { name: "请求体", includeHidden: true })).toHaveCount(0);
  await page.getByLabel("删除参数 1").click();
  await body.locator("summary").click();
  await template.fill('{"id":"{{itemId}}"}');
  await body.getByRole("button", {name:"从占位符生成参数"}).click();
  await template.fill("");
  await body.locator("summary").click();
  await expect(template).toBeHidden();
  await method.selectOption("GET");
  await expect(body).toContainText("GET 不支持请求体");
  await method.selectOption("POST");
  await expect(body).not.toContainText("不支持请求体");

  await expect(page.getByRole("textbox", {name: "调试值 itemId"})).toBeVisible();
  await type.selectOption("number");
  const numeric = page.getByRole("spinbutton", {name: "调试值 itemId"});
  await expect(numeric).toHaveAttribute("step", "any");
  await type.selectOption({ label: "整数 · integer" });
  await expect(page.locator(".schema-preview")).toContainText('"type": "integer"');
  const save = page.getByRole("button", { name: "保存草稿" });
  const send = page.getByRole("button", { name: "发送请求" });
  await save.click();
  await expect(send).toBeEnabled();
  await expect(numeric).toHaveAttribute("step", "1");
  await numeric.fill("0");
  await send.click();
  await expect(page.locator(".call-result pre")).toContainText('"itemId": 0');

  await body.getByText("输入请求体", { exact: true }).press("Enter");
  await expect(template).toBeVisible();
  await expect(body.locator("code")).toHaveText('"{{itemId}}"');
  const json = '{"items":["{{itemId}}"]}';
  await template.fill(json);
  await body.getByText("输入请求体", { exact: true }).click();
  await expect(template).toBeHidden();

  await save.click();
  await expect(send).toBeEnabled();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await expect(template).toBeHidden();
  await body.getByText("输入请求体", { exact: true }).click();
  await expect(template).toHaveValue(json);
  await expect(type).toHaveValue("integer");

  await page.getByRole("button", { name: "返回接口库" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await expect(page.getByRole("heading", { name: "你的第一个 AI 工具，从这里开始" })).toBeVisible();
});

test("从嵌套请求体模板生成参数并调试", async ({ page }) => {
  await page.goto("/");
  await registerMember(page);
  await page.getByRole("button", { name: "新建接口" }).click();
  await page.getByLabel("工具名称").fill("searchInventory");
  await page.getByLabel("用途描述").fill("按 IP 和机房搜索条目");
  await page.getByLabel("接口地址", { exact: true }).fill("http://127.0.0.1:8099/items");
  await page.getByLabel("请求方法").selectOption("POST");
  const body = page.getByRole("region", { name: "JSON 请求体", exact: true });
  await body.getByText("输入请求体", { exact: true }).click();
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
  await page.getByLabel("调试值 room").fill("机房一");
  await expect(names.first()).toHaveValue("hostIps");
  await expect(names.last()).toHaveValue("room");
  await expect(body.getByRole("columnheader", {name:"位置"})).toHaveCount(0);
  await expect(page.getByLabel("参数 1 填写要求")).toHaveValue("必须传入");
  await page.getByLabel("参数 1 类型").selectOption("array");
  await extract.click();
  await expect(names).toHaveCount(2);
  await expect(page.getByLabel("调试值 room")).toHaveValue("机房一");
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
  await body.getByText("输入请求体", { exact: true }).click();
  await expect(template).toHaveValue(json);

  await page.getByLabel("调试值 hostIps").fill("192.0.2.1");
  await page.getByLabel("调试值 room").fill("test-room");
  await page.getByRole("button", { name: "发送请求" }).click();
  await expect(page.getByText("HTTP 200", { exact: true })).toBeVisible();
  await expect(page.locator(".call-result pre")).toContainText('"ipAddresses": [');
  await expect(page.locator(".call-result pre")).toContainText('"192.0.2.1"');
  await expect(page.locator(".call-result pre")).toContainText('"roomName": "test-room"');
});

test("至少填写一个指定筛选参数才能调试", async ({ page }, testInfo) => {
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
  const addressTitle = page.locator("label[for='endpoint-url']");
  const hint = page.getByRole("button", {name: "路径变量说明"});
  await addressTitle.evaluate(el => el.scrollIntoView({block:"start"}));
  await page.evaluate(() => window.scrollBy(0, -100));
  const titleBox = (await addressTitle.boundingBox())!;
  const hintBox = (await hint.boundingBox())!;
  expect(hintBox.x).toBeGreaterThanOrEqual(titleBox.x + titleBox.width);
  expect(Math.abs(hintBox.y + hintBox.height / 2 - titleBox.y - titleBox.height / 2)).toBeLessThan(2);
  await page.screenshot({path: testInfo.outputPath("editor-inline-help.png")});
  const ip = page.getByLabel("参数 1 筛选参数");
  const admin = page.getByLabel("参数 2 筛选参数");
  const bodyGroup = page.getByRole("region", { name: "JSON 请求体", exact: true });
  await expect(bodyGroup.getByRole("columnheader", { name: "筛选参数" })).toBeVisible();
  const help = bodyGroup.getByRole("button", { name: "参数配置说明" });
  await help.hover();
  await expect(bodyGroup.getByRole("tooltip").filter({ hasText: "筛选参数" })).toBeVisible();
  await expect(ip).toBeVisible();
  const requirement = page.getByLabel("参数 1 填写要求");
  await requirement.selectOption("必须传入");
  await page.getByRole("button", {name: "保存草稿"}).click();
  const emptyCall = page.waitForRequest(request => request.url().endsWith("/call"));
  await page.getByRole("button", {name: "发送请求"}).click();
  expect((await emptyCall).postDataJSON().arguments).toEqual({host_ip: []});
  await expect(page.getByText("HTTP 200", {exact: true})).toBeVisible();
  await requirement.selectOption("必须有值");
  await expect(page.locator(".schema-preview")).toContainText("minItems");
  await page.getByRole("button", {name: "保存草稿"}).click();
  await page.getByLabel("调试值 admin").fill("owner");
  await page.getByRole("button", {name: "发送请求"}).click();
  await expect(page.getByRole("alert")).toHaveText("参数 host_ip 不能为空");
  await page.getByRole("alert").screenshot({path: testInfo.outputPath("nonempty-error.png")});
  await requirement.selectOption("选填");
  await expect(ip).not.toBeChecked();
  await ip.check();
  await admin.check();
  await page.getByLabel("参数 2 映射方式").selectOption({ label: "固定值" });
  await expect(admin).toBeDisabled();
  const noInput = admin.locator("xpath=ancestor::tr").getByText("无需填写", { exact: true });
  await admin.locator("xpath=ancestor::tr").screenshot({ path: testInfo.outputPath("fixed-requirement-row.png") });
  const adminBox = (await admin.boundingBox())!;
  const noInputBox = (await noInput.boundingBox())!;
  expect(Math.abs(noInputBox.y + noInputBox.height / 2 - adminBox.y - adminBox.height / 2)).toBeLessThan(2);
  await page.getByLabel("参数 2 映射方式").selectOption({ label: "客户端填写" });
  await expect(admin).not.toBeChecked();
  await admin.check();
  const message = "请填写 host_ip 或 admin";
  await expect(page.locator(".schema-preview")).toContainText(message);
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByText("接口配置已保存")).toBeVisible();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await expect(ip).toBeChecked();
  await expect(admin).toBeChecked();
  const limit = page.getByLabel("调试值 limit");
  const run = page.getByRole("button", { name: "发送请求" });
  await expect(limit).toHaveValue("");
  await limit.scrollIntoViewIfNeeded();
  await page.locator(".call-panel").screenshot({path: testInfo.outputPath("trial-arguments.png")});
  await limit.fill("10");
  await run.click();
  await expect(page.getByRole("alert")).toHaveText(message);
  await expect(limit).toHaveValue("10");
  await expect(page.locator(".toast")).toHaveCount(0);
  await page.getByRole("heading", { name: /^参数映射/ }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("editor-flow-desktop.png") });

  await page.getByLabel("调试值 admin").fill("owner");
  await run.click();
  await expect(page.getByText("HTTP 200", { exact: true })).toBeVisible();
  await expect(page.locator(".call-result pre")).toContainText('"owner"');
  await expect(page.locator(".call-result pre")).not.toContainText('"ip"');
  await page.setViewportSize({ width: 393, height: 852 });
  const mappings = page.getByRole("region", { name: "JSON 请求体", exact: true }).locator(".parameter-wrap");
  await mappings.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("parameter-group-mobile.png") });
  await mappings.evaluate((element) => { element.scrollLeft = element.scrollWidth; });
  await expect(ip).toBeInViewport();
  await expect(admin).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole("button", { name: "调试", exact: true }).click();
  const trial = page.getByRole("region", { name: "调试" });
  await expect(trial).toBeFocused();
  await expect(trial.getByRole("heading", { name: "调试" })).toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath("editor-flow-mobile.png") });
});

test("开启调用审批后由页面批准并执行 MCP 调用", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => consoleErrors.push(error.message));
  await page.goto("/");
  await registerMember(page);
  await page.getByRole("button", { name: "使用 Pod 查询示例" }).click();
  await page.getByLabel("接口地址", { exact: true }).fill("http://127.0.0.1:8099/pods");
  await page.getByLabel("MCP 调用需审批").check();
  await page.getByRole("combobox", { name: /^审批人/ }).selectOption({ label: "指定用户" });
  await page.getByLabel("审批人用户名").fill("nobody_here");
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("alert")).toContainText("审批人 nobody_here 不存在或已停用");
  await page.getByRole("combobox", { name: /^审批人/ }).selectOption({ label: "调用者本人" });
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "发布工具" })).toBeEnabled();
  await expect(page.getByText("调试请求由维护者直接发送，不经过审批。")).toBeVisible();
  await page.getByRole("button", { name: "发布工具" }).click();
  await expect(page.getByRole("button", { name: "停用工具" })).toBeVisible();
  await page.getByRole("button", { name: "返回接口库" }).click();
  await expect(page.locator(".tool-table")).toContainText("需审批");

  const token = await page.evaluate(async () => {
    const response = await fetch("/api/tokens", { method: "POST", headers: { "Content-Type": "application/json" }, body: '{"name":"approval"}' });
    return (await response.json()).token as string;
  });
  const submitted = await page.request.post("/mcp", {
    headers: { Authorization: `Bearer ${token}`, Accept: "application/json, text/event-stream" },
    data: { jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "getPodInstance", arguments: { podIp: "10.60.144.190" } } },
  });
  expect((await submitted.json()).result.content[0].text).toContain("尚未执行");

  await page.getByRole("button", { name: /^✓?审批/ }).click();
  await expect(page.getByRole("heading", { name: "审批", exact: true })).toBeVisible();
  await expect(page.getByText("待审批", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "详情" }).click();
  await expect(page.locator(".approval-detail pre").first()).toContainText('"podIp": "10.60.144.190"');
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "批准并执行" }).click();
  await expect(page.getByText("已执行", { exact: true })).toBeVisible();
  await expect(page.locator(".approval-detail")).toContainText("执行结果 · HTTP 200");
  await expect(page.locator(".approval-detail")).toContainText('"podIp": "10.60.144.190"');
  await expect(page.getByRole("button", { name: "批准并执行" })).toHaveCount(0);
  await page.getByRole("button", { name: "接口库", exact: true }).click();
  await page.goto("/#approvals");
  await page.reload();
  await expect(page.getByRole("heading", { name: "审批", exact: true })).toBeVisible();
  await expect(page.getByText("已执行", { exact: true })).toBeVisible();
  expect(consoleErrors).toEqual([]);
});

test("管理员配置 Grafana 连接并创建多条查询工具与调试发布", async ({ page }, testInfo) => {
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => consoleErrors.push(error.message));

  await page.goto("/");
  await page.getByLabel("用户名", { exact: true }).fill(envConfig.ADMIN_USERNAME || "admin");
  await page.getByLabel("密码", { exact: true }).fill(envConfig.ADMIN_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await expect(page.getByRole("heading", { name: "接口库" })).toBeVisible();

  const connName = `测试 Grafana ${Date.now()}`;
  await page.getByRole("button", { name: /Grafana 连接/ }).click();
  await expect(page.getByRole("heading", { name: /Grafana 连接/ })).toBeVisible();
  await page.getByRole("button", { name: /新建连接/ }).click();
  await page.getByLabel("连接名称").fill(connName);
  await page.getByLabel("Grafana 地址").fill("http://127.0.0.1:8099");
  await page.getByRole("textbox", { name: /Token/ }).fill("glsa_e2e_token");
  await page.getByRole("button", { name: "保存连接" }).click();
  await expect(page.locator("table")).toContainText(connName);
  const connRow = page.locator("tr", { hasText: connName });
  await connRow.getByRole("button", { name: "测试连接" }).click();
  await expect(page.locator(".toast")).toContainText("检测到 3 个数据源");
  await page.screenshot({ path: testInfo.outputPath("grafana-connections-desktop.png"), fullPage: true });

  await page.locator("nav").getByRole("button", { name: /接口库/ }).click();
  await expect(page.getByRole("button", { name: "新建 Grafana 查询", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "新建接口", exact: true }).click();
  await page.getByRole("group", { name: "工具类型" }).getByRole("button", { name: "Grafana 查询", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Grafana 查询定义" })).toBeVisible();

  const toolName = `grafana_host_${Date.now()}`;
  await page.getByLabel("工具名称").fill(toolName);
  await page.getByLabel("用途描述").fill("按主机 IP 联合查询 Prometheus 指标和 VictoriaLogs 日志");
  await page.getByLabel("Grafana 连接", { exact: true }).selectOption({ label: `${connName}（http://127.0.0.1:8099）` });
  await expect(page.getByLabel("查询 1 数据源")).toContainText("Prometheus Main");
  await expect(page.getByLabel("查询 A 数据类型")).toHaveValue("metrics");

  await page.getByLabel("查询 A 语句").fill('up{instance=~"{{host_ip}}.*"}');
  await page.getByText("时序降采样与突变点参数").click();
  await page.getByLabel("查询 A LTTB 目标点数").fill("60");
  await page.getByLabel("查询 A 突变阈值比例").fill("0.3");
  await page.getByLabel("查询 A 最多突变点数").fill("3");
  await page.getByRole("button", { name: /添加查询/ }).click();
  await page.getByLabel("查询 2 数据源").selectOption("vmlogs-main");
  await expect(page.getByLabel("查询 B 数据类型")).toHaveValue("logs");
  await page.getByLabel("查询 B 语句").fill('host:"{{host_ip}}" AND env:"{{env}}"');
  await page.getByRole("button", { name: "从占位符生成参数" }).click();

  await expect(page.getByLabel("参数 1 名称")).toHaveValue("host_ip");
  await expect(page.getByLabel("参数 2 名称")).toHaveValue("env");
  await page.getByLabel("参数 2 映射方式").selectOption("true");
  await page.getByLabel("参数 2 固定值").fill("prod");

  await expect(page.locator(".schema-preview")).toContainText('"from"');
  await expect(page.locator(".schema-preview")).toContainText('"now-1h"');
  await expect(page.locator(".schema-preview")).toContainText('"host_ip"');
  await expect(page.locator(".schema-preview")).not.toContainText('"env"');
  await expect(page.getByRole("button", { name: "OpenAPI", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "调用审批" })).toHaveCount(0);

  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "发送请求" })).toBeEnabled();
  await page.getByLabel("调试值 host_ip").fill("10.20.30.40");
  await page.getByRole("button", { name: "发送请求" }).click();
  await expect(page.getByText("HTTP 200", { exact: true })).toBeVisible();
  await expect(page.locator(".call-result pre").first()).toContainText("10.20.30.40");
  await expect(page.locator(".call-result pre").first()).toContainText("now-1h..now");
  await expect(page.locator(".call-result pre").first()).toContainText("prod");
  await expect(page.locator(".call-result pre").first()).toContainText('"dataType": "metrics"');
  await expect(page.locator(".call-result pre").first()).toContainText('"dataType": "logs"');
  await expect(page.locator(".call-result pre").first()).toContainText('"totalPoints": 300');
  await expect(page.locator(".call-result pre").first()).toContainText('"sampledPoints": 60');
  await expect(page.locator(".call-result pre").first()).toContainText('"toTime": "2026-05-21T08:00:00+08:00"');
  await expect(page.getByText("Grafana 原始响应")).toBeVisible();

  // 在「输出字段」选定日志字段，自动带入调试样例，通过划选生成正则 -> JSON 展开 -> 拆数组 -> 保留字段，预览自定义结果
  await page.getByRole("button", { name: "＋ 添加输出字段" }).click();
  await page.getByLabel("输出名称").fill("logs");
  await page.getByLabel("响应路径").fill("/results/B/frames/0/rows/*/_msg");
  await page.getByText("数据处理（JSON / 正则 / 拆数组 / 字段过滤）").click();
  await page.getByRole("button", { name: "＋ 正则解析" }).click();
  await page.getByRole("button", { name: "划选样例自动生成正则" }).click();

  const selectBox = page.getByLabel("logs 处理器 1 划选样例");
  await expect(selectBox).toHaveValue(/10\.20\.30\.40 \[ERROR\]/);
  const sampleLine = await selectBox.inputValue();
  const markSegment = async (segment: string, fieldName?: string) => {
    const start = sampleLine.indexOf(segment);
    const end = start + segment.length;
    await selectBox.evaluate(
      (el: HTMLTextAreaElement, range: [number, number]) => {
        el.focus();
        el.setSelectionRange(range[0], range[1]);
        el.dispatchEvent(new Event("select", { bubbles: true }));
      },
      [start, end] as [number, number],
    );
    if (fieldName) {
      await page.getByLabel("logs 处理器 1 划选字段名").fill(fieldName);
    }
    await page.getByRole("button", { name: "标记为字段" }).click();
  };

  await markSegment("10.20.30.40");
  await markSegment("ERROR");
  await markSegment(sampleLine.slice(sampleLine.indexOf("{")), "payload");
  await page.getByRole("button", { name: "生成正则并验证" }).click();
  await expect(page.getByLabel("logs 处理器 1 提取字段")).toHaveValue("ip, level, payload");
  await expect(page.getByLabel("logs 处理预览结果")).toContainText('"ip": "10.20.30.40"');

  await page.getByRole("button", { name: "＋ JSON 解析" }).click();
  await page.getByLabel("logs 处理器 2 原始字段").fill("payload");
  await page.getByRole("button", { name: "＋ 拆分数组" }).click();
  await page.getByLabel("logs 处理器 3 原始字段").fill("events");
  await page.getByRole("button", { name: "＋ 保留字段" }).click();
  await page.getByLabel("logs 处理器 4 字段列表").fill("ip, level, trace_id, code, msg");
  await page.getByRole("button", { name: "验证处理效果" }).click();
  await expect(page.getByText("#3 拆分数组 · 拆出 2 行")).toBeVisible();

  await page.getByRole("button", { name: "预览自定义结果" }).click();
  const customPre = page.getByLabel("自定义结果 JSON");
  await expect(customPre).toContainText('"code":"E1"');
  await expect(customPre).toContainText('"code":"E2"');
  await expect(customPre).not.toContainText("debug_noise");
  await expect(customPre).not.toContainText("redundant-pod-uid");

  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "发布工具", exact: true })).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath("grafana-editor-desktop.png"), fullPage: true });

  await page.setViewportSize({ width: 393, height: 852 });
  await page.screenshot({ path: testInfo.outputPath("grafana-editor-mobile.png"), fullPage: true });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
  ).toBe(true);
  await page.setViewportSize({ width: 1440, height: 1060 });

  await page.getByRole("button", { name: "发布工具", exact: true }).click();
  await expect(page.getByRole("button", { name: "停用工具" })).toBeVisible();
  await page.getByRole("button", { name: "返回接口库" }).click();
  const toolRow = page.locator(".tool-table tr", { hasText: toolName });
  await expect(toolRow).toContainText("GRAFANA");
  await expect(toolRow).toContainText("METRICS");
  await expect(toolRow).toContainText("LOGS");
  await page.screenshot({ path: testInfo.outputPath("grafana-library-desktop.png"), fullPage: true });
  expect(consoleErrors).toEqual([]);
});
