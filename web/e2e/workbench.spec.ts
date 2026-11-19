import { test, expect } from "@playwright/test";
import { resolve } from "node:path";

const screenshot = (name: string) =>
  resolve(import.meta.dirname, "../../docs/screenshots", name);

test("注册、配置、试调用、发布、导出和令牌管理", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => consoleErrors.push(error.message));
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
  await page.screenshot({
    path: screenshot("login-desktop.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "创建账户", exact: true }).click();
  await page
    .getByLabel("用户名", { exact: true })
    .fill(`browser_${Date.now()}`);
  await page
    .getByLabel("密码", { exact: true })
    .fill("browser-test-password-123");
  await page.getByRole("button", { name: "注册并进入工作台" }).click();
  await expect(page.getByRole("heading", { name: "接口库" })).toBeVisible();
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
