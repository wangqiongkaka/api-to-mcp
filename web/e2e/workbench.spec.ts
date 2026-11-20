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
  await expect(page.getByRole("button", { name: "运行测试" })).toBeDisabled();
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "运行测试" })).toBeEnabled();
  await page.getByRole("button", { name: "运行测试" }).click();
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
  await page.getByRole("button", { name: "运行测试" }).click();
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

test("从原始试调用响应选择字段并预览自定义输出", async ({ page }, testInfo) => {
  await page.goto("/");
  await registerMember(page);
  await page.getByRole("button", { name: "使用 Pod 查询示例" }).click();
  await page.getByLabel("接口地址", { exact: true }).fill("http://127.0.0.1:8099/pods");
  const output = page.getByRole("region", { name: "输出字段" });
  const add = output.getByRole("button", { name: "添加输出字段" });
  const trial = page.getByRole("button", { name: "运行测试" });
  let calls = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.endsWith("/call")) calls++;
  });
  await page.getByRole("button", { name: "保存草稿" }).click();
  await trial.click();
  const paths = output.getByLabel("从试调用结果选字段");
  await paths.selectOption("/data/list/*/totalIp");
  await output.getByRole("button", { name: "添加所选字段" }).click();
  await output.getByLabel("输出名称").first().fill("ipList");
  await add.click();
  await output.getByLabel("输出名称").nth(1).fill("source");
  await output.getByLabel("值来源").nth(1).selectOption("fixed");
  await output.getByLabel("固定 JSON 值").fill('"inventory"');
  const custom = page.getByLabel("自定义结果 JSON");
  const want = '{"ipList":["10.0.0.1","10.0.0.2"],"source":"inventory"}';
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
  await expect(output.getByLabel("响应路径")).toHaveValue("/data/list/*/totalIp");
  await expect(output.getByLabel("固定 JSON 值")).toHaveValue('"inventory"');
  await trial.click();
  await expect(custom).toHaveText(want);
  await page.setViewportSize({ width: 393, height: 852 });
  await expect(output.getByLabel("固定 JSON 值")).toBeVisible();
  await expect(page.locator(".toast")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await output.screenshot({ path: testInfo.outputPath("output-fields-mobile.png") });
  await output.getByRole("button", { name: "删除输出字段 2" }).click();
  await output.getByRole("button", { name: "删除输出字段 1" }).click();
  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByRole("button", { name: "运行测试" })).toBeEnabled();
  await page.getByRole("button", { name: "运行测试" }).click();
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
  await expect(body).toHaveCount(0);
  await wholeBody.check();
  await expect(location.getByRole("option", { name: "请求体", includeHidden: true })).toBeDisabled();
  await wholeBody.uncheck();
  await location.selectOption("body");
  await expect(wholeBody).toBeDisabled();
  await expect(body).toContainText("默认按参数名称生成 JSON 字段");
  await expect(template).toBeHidden();
  await method.selectOption("GET");
  await expect(body).toContainText("GET 不支持请求体");
  await method.selectOption("POST");
  await expect(body).not.toContainText("不支持请求体");

  await type.selectOption({ label: "整数 · integer" });
  await expect(page.locator(".schema-preview")).toContainText('"type": "integer"');
  await body.getByText("配置嵌套 JSON 模板", { exact: true }).press("Enter");
  await expect(template).toBeVisible();
  await expect(body.locator("code")).toHaveText('"{{itemId}}"');
  const json = '{"items":["{{itemId}}"]}';
  await template.fill(json);
  await body.getByText("嵌套 JSON 模板（已填写）", { exact: true }).click();
  await expect(template).toBeHidden();

  await page.getByRole("button", { name: "保存草稿" }).click();
  await expect(page.getByText("接口配置已保存")).toBeVisible();
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
