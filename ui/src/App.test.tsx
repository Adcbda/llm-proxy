import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("shows the first-project workflow and opens the creation dialog", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) }));
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>);
    const action = await screen.findByRole("button", { name: "创建项目" });
    fireEvent.click(action);
    expect(await screen.findByRole("dialog")).toHaveTextContent("创建调试项目");
    expect(screen.getByLabelText("项目名称")).toBeInTheDocument();
  });

  it("pauses capture and saves the round as a group", async () => {
    let captureState: "capturing" | "paused" | "idle" = "capturing";
    const project = () => ({
      id: "prj_test", name: "Capture test", baseUrl: "http://upstream.test/v1",
      upstreamApiKeyMasked: "", apiKeyPrefix: "llmp_test", createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(), requestCount: 2, captureState,
      captureStartedAt: new Date().toISOString(), capturePausedAt: captureState === "paused" ? new Date().toISOString() : null,
      captureRequestCount: captureState === "idle" ? 0 : 2,
    });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/capture/pause") && init?.method === "POST") {
        captureState = "paused";
        return { ok: true, json: async () => project() };
      }
      if (url.endsWith("/capture-groups") && init?.method === "POST") {
        captureState = "idle";
        return { ok: true, json: async () => ({ id: "grp_test", projectId: "prj_test", name: "复现分组", startedAt: new Date().toISOString(), endedAt: new Date().toISOString(), createdAt: new Date().toISOString(), requestCount: 2 }) };
      }
      if (url === "/api/projects") return { ok: true, json: async () => ({ items: [project()] }) };
      if (url.includes("/capture-groups")) return { ok: true, json: async () => ({ items: [] }) };
      if (url.includes("/requests")) return { ok: true, json: async () => ({ items: [] }) };
      return { ok: false, status: 404, json: async () => ({}) };
    });
    class EventSourceStub {
      addEventListener() { /* no-op */ }
      close() { /* no-op */ }
    }
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("EventSource", EventSourceStub);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>);

    fireEvent.click(await screen.findByRole("button", { name: "暂停抓包" }));
    expect(await screen.findByRole("button", { name: "继续抓包" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "保存为分组" }));
    fireEvent.change(await screen.findByLabelText("分组名称"), { target: { value: "复现分组" } });
    fireEvent.click(screen.getByRole("button", { name: "保存分组" }));
    expect(await screen.findByText("已保存分组“复现分组”")).toBeInTheDocument();
  });

  it("uses stable group colors in the all-captures view", async () => {
    const now = new Date().toISOString();
    const project = {
      id: "prj_test", name: "Capture test", baseUrl: "http://upstream.test/v1",
      upstreamApiKeyMasked: "", apiKeyPrefix: "llmp_test", createdAt: now, updatedAt: now,
      requestCount: 2, captureState: "capturing", captureStartedAt: now, capturePausedAt: null,
      captureRequestCount: 0,
    };
    const groups = [
      { id: "grp_alpha", projectId: project.id, name: "第一组", startedAt: now, endedAt: now, createdAt: now, requestCount: 1 },
      { id: "grp_beta", projectId: project.id, name: "第二组", startedAt: now, endedAt: now, createdAt: now, requestCount: 1 },
    ];
    const request = (id: string, groupId: string, model: string) => ({
      id, projectId: project.id, groupId, method: "POST", path: "/v1/chat/completions", model,
      streaming: false, status: "completed", httpStatus: 200, startedAt: now, finishedAt: now,
      durationMs: 10, requestTruncated: false, responseTruncated: false, requestBytes: 10, responseBytes: 20,
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/projects") return { ok: true, json: async () => ({ items: [project] }) };
      if (url.includes("/capture-groups")) return { ok: true, json: async () => ({ items: groups }) };
      if (url.includes("/requests")) return { ok: true, json: async () => ({ items: [request("req_a", groups[0].id, "model-a"), request("req_b", groups[1].id, "model-b")] }) };
      return { ok: false, status: 404, json: async () => ({}) };
    }));
    class EventSourceStub {
      addEventListener() { /* no-op */ }
      close() { /* no-op */ }
    }
    vi.stubGlobal("EventSource", EventSourceStub);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>);

    const firstRow = await screen.findByTitle("抓包分组：第一组");
    const secondRow = await screen.findByTitle("抓包分组：第二组");
    expect(firstRow).toHaveClass("capture-group-row");
    expect(secondRow).toHaveClass("capture-group-row");
    expect(firstRow.getAttribute("style")).not.toBe(secondRow.getAttribute("style"));
  });

  it("shows the model duration and inferred tool batch duration", async () => {
    const now = new Date().toISOString();
    const project = {
      id: "prj_test", name: "Timing test", baseUrl: "http://upstream.test/v1",
      upstreamApiKeyMasked: "", apiKeyPrefix: "llmp_test", createdAt: now, updatedAt: now,
      requestCount: 1, captureState: "capturing", captureStartedAt: now, capturePausedAt: null,
      captureRequestCount: 1,
    };
    const summary = {
      id: "req_timing", projectId: project.id, method: "POST", path: "/v1/chat/completions", model: "tool-model",
      streaming: false, status: "completed", httpStatus: 200, startedAt: now, finishedAt: now,
      durationMs: 1250, requestTruncated: false, responseTruncated: false, requestBytes: 100, responseBytes: 50,
    };
    const detail = {
      ...summary,
      upstreamUrl: "http://upstream.test/v1/chat/completions", error: "",
      requestHeaders: {}, responseHeaders: {}, live: false,
      requestBody: JSON.stringify({ messages: [
        { role: "tool", tool_call_id: "call_a", content: "one" },
        { role: "tool", tool_call_id: "call_b", content: "two" },
      ] }),
      responseBody: JSON.stringify({ choices: [{ message: { role: "assistant", content: "done" } }] }),
      aggregatedResponse: "",
      toolTimingEstimate: { durationMs: 875, previousRequestId: "req_previous", toolCallIds: ["call_a", "call_b"] },
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/projects") return { ok: true, json: async () => ({ items: [project] }) };
      if (url.includes("/capture-groups")) return { ok: true, json: async () => ({ items: [] }) };
      if (url === "/api/requests/req_timing") return { ok: true, json: async () => detail };
      if (url.includes("/requests")) return { ok: true, json: async () => ({ items: [summary] }) };
      return { ok: false, status: 404, json: async () => ({}) };
    }));
    class EventSourceStub {
      addEventListener() { /* no-op */ }
      close() { /* no-op */ }
    }
    vi.stubGlobal("EventSource", EventSourceStub);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>);

    const modelCell = await screen.findByText("tool-model");
    fireEvent.click(modelCell.closest("tr")!);
    expect(await screen.findByText("UPSTREAM RESPONSE · 本轮模型 1.25 s")).toBeInTheDocument();
    expect(await screen.findAllByText("推算 ≈ 875 ms · 批次")).toHaveLength(2);
  });

  it("selects completed requests and deletes them in one batch", async () => {
    const now = new Date().toISOString();
    const project = {
      id: "prj_test", name: "Delete test", baseUrl: "http://upstream.test/v1",
      upstreamApiKeyMasked: "", apiKeyPrefix: "llmp_test", createdAt: now, updatedAt: now,
      requestCount: 3, captureState: "capturing" as const, captureStartedAt: now, capturePausedAt: null,
      captureRequestCount: 3,
    };
    const request = (id: string, status: "completed" | "running") => ({
      id, projectId: project.id, method: "POST", path: "/v1/chat/completions", model: id,
      streaming: false, status, httpStatus: status === "completed" ? 200 : null, startedAt: now,
      finishedAt: status === "completed" ? now : null, durationMs: 10, requestTruncated: false,
      responseTruncated: false, requestBytes: 10, responseBytes: 20,
    });
    let requests = [request("req_delete_a", "completed"), request("req_delete_b", "completed"), request("req_running", "running")];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/requests/batch-delete") && init?.method === "POST") {
        const inputBody = JSON.parse(String(init.body)) as { ids: string[] };
        requests = requests.filter((item) => !inputBody.ids.includes(item.id));
        return { ok: true, json: async () => ({ deleted: inputBody.ids.length }) };
      }
      if (url === "/api/projects") return { ok: true, json: async () => ({ items: [project] }) };
      if (url.includes("/capture-groups")) return { ok: true, json: async () => ({ items: [] }) };
      if (url.includes("/requests")) return { ok: true, json: async () => ({ items: requests }) };
      return { ok: false, status: 404, json: async () => ({}) };
    });
    class EventSourceStub {
      addEventListener() { /* no-op */ }
      close() { /* no-op */ }
    }
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("EventSource", EventSourceStub);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>);

    const currentApp = within(view.container);
    expect(await currentApp.findByRole("checkbox", { name: "选择请求 req_running" })).toBeDisabled();
    const selectPage = currentApp.getByRole("checkbox", { name: "选择当前页请求" });
    fireEvent.click(selectPage);
    expect(currentApp.getByRole("checkbox", { name: "选择请求 req_delete_a" })).toBeChecked();
    expect(currentApp.getByRole("checkbox", { name: "选择请求 req_delete_b" })).toBeChecked();
    fireEvent.click(currentApp.getByRole("button", { name: "删除 (2)" }));

    expect(await currentApp.findByText("已删除 2 条请求")).toBeInTheDocument();
    expect(window.confirm).toHaveBeenCalledWith("确定删除选中的 2 条请求吗？此操作不可恢复。");
    const deleteCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/requests/batch-delete"));
    expect(JSON.parse(String(deleteCall?.[1]?.body))).toEqual({ ids: ["req_delete_a", "req_delete_b"] });
  });
});
