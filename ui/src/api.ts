export type Project = {
  id: string;
  name: string;
  baseUrl: string;
  upstreamApiKeyMasked: string;
  apiKeyPrefix: string;
  createdAt: string;
  updatedAt: string;
  requestCount: number;
};

export type RequestSummary = {
  id: string;
  projectId: string;
  method: string;
  path: string;
  model: string;
  streaming: boolean;
  status: "running" | "completed" | "upstream_error" | "interrupted";
  httpStatus: number | null;
  startedAt: string;
  finishedAt: string | null;
  durationMs: number;
  requestTruncated: boolean;
  responseTruncated: boolean;
  requestBytes: number;
  responseBytes: number;
};

export type RequestDetail = RequestSummary & {
  upstreamUrl: string;
  error?: string;
  requestHeaders: Record<string, string[]>;
  responseHeaders: Record<string, string[]>;
  requestBody: string;
  responseBody: string;
  aggregatedResponse?: string;
  live: boolean;
};

export type RequestFilters = {
  status?: string;
  path?: string;
  model?: string;
  streaming?: string;
};

async function call<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    const message = payload?.error?.message || `请求失败 (${response.status})`;
    throw new Error(message);
  }
  return payload as T;
}

export const api = {
  listProjects: () => call<{ items: Project[] }>("/api/projects"),
  createProject: (input: { name: string; baseUrl: string; upstreamApiKey: string }) =>
    call<{ project: Project; apiKey: string }>("/api/projects", { method: "POST", body: JSON.stringify(input) }),
  updateProject: (id: string, input: { name: string; baseUrl: string; upstreamApiKey?: string }) =>
    call<Project>(`/api/projects/${id}`, { method: "PATCH", body: JSON.stringify(input) }),
  deleteProject: (id: string) => call<{ deleted: boolean }>(`/api/projects/${id}`, { method: "DELETE" }),
  revealKey: (id: string) => call<{ apiKey: string }>(`/api/projects/${id}/reveal-key`, { method: "POST" }),
  rotateKey: (id: string) => call<{ apiKey: string }>(`/api/projects/${id}/rotate-key`, { method: "POST" }),
  testUpstream: (id: string) =>
    call<{ ok: boolean; status?: number; durationMs: number; error?: string }>(`/api/projects/${id}/test-upstream`, { method: "POST" }),
  clearRequests: (id: string) => call<{ deleted: number }>(`/api/projects/${id}/requests`, { method: "DELETE" }),
  listRequests: (projectId: string, filters: RequestFilters = {}, cursor = "") => {
    const search = new URLSearchParams({ limit: "50" });
    if (cursor) search.set("cursor", cursor);
    Object.entries(filters).forEach(([key, value]) => value && search.set(key, value));
    return call<{ items: RequestSummary[]; nextCursor?: string }>(`/api/projects/${projectId}/requests?${search}`);
  },
  getRequest: (id: string) => call<RequestDetail>(`/api/requests/${id}`),
};

