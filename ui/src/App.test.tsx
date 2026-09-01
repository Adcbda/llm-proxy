import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";

afterEach(() => vi.restoreAllMocks());

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
});

