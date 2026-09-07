import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
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
});
