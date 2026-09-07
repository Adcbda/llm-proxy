import { expect, test } from "@playwright/test";

test("creates a project, proxies a chat request, inspects it, rotates the key, and deletes the project", async ({ page, request }) => {
  const projectName = `E2E Agent ${Date.now()}`;
  await page.goto("/");
  await page.getByRole("button", { name: "创建项目", exact: true }).click();
  await page.getByLabel("项目名称").fill(projectName);
  await page.getByLabel("上游 BaseURL").fill("http://127.0.0.1:18081/v1");
  await page.getByRole("button", { name: "创建项目", exact: true }).last().click();
  await expect(page.getByText("项目已创建")).toBeVisible();
  const projectKey = await page.locator(".secret-reveal code").textContent();
  expect(projectKey).toMatch(/^llmp_/);
  await page.getByRole("button", { name: "完成" }).click();

  const proxyResponse = await request.post("http://127.0.0.1:18080/v1/chat/completions", {
    headers: { Authorization: `Bearer ${projectKey}`, "Content-Type": "application/json" },
    data: { model: "e2e-agent-model", messages: [{ role: "user", content: "inspect me" }] },
  });
  expect(proxyResponse.status()).toBe(200);
  expect(await proxyResponse.text()).toContain("hello from upstream");

  await expect(page.getByText("e2e-agent-model")).toBeVisible();
  const endpointFilter = page.getByLabel("接口筛选");
  await endpointFilter.focus();
  await endpointFilter.press("ArrowDown");
  await expect(endpointFilter).toHaveValue("/v1/chat/completions");
  await expect(page.getByText("e2e-agent-model")).toBeVisible();

  await page.getByText("e2e-agent-model").click();
  await expect(page.locator(".inspector")).toContainText("inspect me");
  await expect(page.locator(".inspector")).toContainText("hello from upstream");
  await page.getByRole("tab", { name: "原始包" }).click();
  await expect(page.locator(".code-block")).toContainText("e2e-agent-model");
  await page.getByRole("button", { name: "关闭详情" }).click();

  await page.getByRole("checkbox", { name: /选择请求 req_/ }).check();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除 (1)" }).click();
  await expect(page.getByText("已删除 1 条请求")).toBeVisible();
  await expect(page.getByText("e2e-agent-model")).not.toBeVisible();

  await page.getByRole("button", { name: "项目设置" }).click();
  await page.getByRole("button", { name: "测试 /models" }).click();
  await expect(page.getByText(/连接成功/)).toBeVisible();
  await page.getByRole("tab", { name: "凭据" }).click();
  await page.getByRole("button", { name: "查看完整 Key" }).click();
  await expect(page.locator(".secret-reveal code")).toContainText("llmp_");
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "轮换 API Key" }).click();
  await expect(page.getByText("旧 API Key 已立即失效")).toBeVisible();

  await page.getByRole("tab", { name: "数据与删除" }).click();
  await page.locator(".danger-section.delete input").fill(projectName);
  await page.getByRole("button", { name: "永久删除" }).click();
  await expect(page.getByText("看清 Agent 的每一次模型交互")).toBeVisible();
});
