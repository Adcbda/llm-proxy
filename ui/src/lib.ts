import type { RequestDetail } from "./api";

export type InspectorMessage = {
  role: string;
  content?: unknown;
  name?: string;
  tool_call_id?: string;
  tool_calls?: unknown[];
  function_call?: unknown;
  refusal?: unknown;
  [key: string]: unknown;
};

export function parseJSON(value: string): unknown | null {
  if (!value.trim()) return null;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

export function parseEmbeddedJSON(value: unknown): { value: unknown; isJSON: boolean } {
  if (typeof value !== "string") {
    return { value, isJSON: value !== null && typeof value === "object" };
  }
  if (!value.trim()) return { value, isJSON: false };
  try {
    return { value: JSON.parse(value), isJSON: true };
  } catch {
    return { value, isJSON: false };
  }
}

export function textPreview(value: string, maxLength = 180): string {
  const compact = value
    .replace(/<[^>]*>/g, " ")
    .replace(/&(?:nbsp|#160);/gi, " ")
    .replace(/&(?:quot|#34);/gi, '"')
    .replace(/&(?:apos|#39|#x27);/gi, "'")
    .replace(/&(?:amp|#38);/gi, "&")
    .replace(/^\s*[-#]+\s*/gm, "")
    .replace(/\s+/g, " ")
    .trim();
  return compact.length > maxLength ? `${compact.slice(0, maxLength).trimEnd()}…` : compact;
}

export function prettyBody(value: string): string {
  const parsed = parseJSON(value);
  return parsed === null ? value : JSON.stringify(parsed, null, 2);
}

export function extractMessages(detail: Pick<RequestDetail, "requestBody" | "responseBody" | "aggregatedResponse">) {
  const request = parseJSON(detail.requestBody) as { messages?: unknown[] } | null;
  const response = parseJSON(detail.aggregatedResponse || detail.responseBody) as { choices?: Array<{ message?: unknown }> } | null;
  const requestMessages = Array.isArray(request?.messages)
    ? request.messages.filter(isMessage)
    : [];
  const responseMessages = Array.isArray(response?.choices)
    ? response.choices.map((choice) => choice?.message).filter(isMessage)
    : [];
  return { request: requestMessages, response: responseMessages };
}

export type SSEEvent = { index: number; event: string; data: string; parsed: unknown | null };

export function parseSSEEvents(value: string): SSEEvent[] {
  return value
    .replace(/\r\n/g, "\n")
    .split(/\n\n+/)
    .map((block, index) => {
      let event = "message";
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) data.push(line.slice(5).trim());
      }
      const joined = data.join("\n");
      return { index, event, data: joined, parsed: joined === "[DONE]" ? null : parseJSON(joined) };
    })
    .filter((item) => item.data.length > 0);
}

export function modelList(value: string): Array<{ id: string; owned_by?: string; created?: number }> {
  const parsed = parseJSON(value) as { data?: unknown[] } | null;
  if (!Array.isArray(parsed?.data)) return [];
  return parsed.data.filter((item): item is { id: string; owned_by?: string; created?: number } =>
    Boolean(item && typeof item === "object" && typeof (item as { id?: unknown }).id === "string"),
  );
}

function isMessage(value: unknown): value is InspectorMessage {
  return Boolean(value && typeof value === "object" && typeof (value as { role?: unknown }).role === "string");
}

export function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(1)} MiB`;
}

export function formatDuration(value: number): string {
  if (value < 1000) return `${value} ms`;
  if (value < 60_000) return `${(value / 1000).toFixed(2)} s`;
  return `${(value / 60_000).toFixed(1)} min`;
}

export function formatTime(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit",
    hour12: false,
  }).format(new Date(value));
}
