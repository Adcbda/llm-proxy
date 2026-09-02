import { describe, expect, it } from "vitest";
import { extractMessages, modelList, parseEmbeddedJSON, parseSSEEvents, prettyBody, textPreview } from "./lib";

describe("inspector parsing", () => {
  it("extracts request and aggregated response messages without losing structured content", () => {
    const result = extractMessages({
      requestBody: JSON.stringify({ messages: [
        { role: "developer", content: "follow policy" },
        { role: "user", content: [{ type: "text", text: "hello" }, { type: "image_url", image_url: { url: "data:..." } }] },
        { role: "tool", tool_call_id: "call_1", content: "result" },
      ] }),
      responseBody: "",
      aggregatedResponse: JSON.stringify({ choices: [{ message: { role: "assistant", content: "done", tool_calls: [{ id: "call_2" }] } }] }),
    });
    expect(result.request).toHaveLength(3);
    expect(result.request[1].content).toBeInstanceOf(Array);
    expect(result.response[0].tool_calls).toHaveLength(1);
  });

  it("parses arbitrary SSE boundaries and DONE", () => {
    const events = parseSSEEvents("data: {\"choices\":[]}\r\n\r\nevent: message\r\ndata: [DONE]\r\n\r\n");
    expect(events).toHaveLength(2);
    expect(events[0].parsed).toEqual({ choices: [] });
    expect(events[1].data).toBe("[DONE]");
  });

  it("formats JSON and extracts model lists", () => {
    const raw = JSON.stringify({ object: "list", data: [{ id: "model-a", owned_by: "vendor" }, { invalid: true }] });
    expect(prettyBody(raw)).toContain("\n  \"object\"");
    expect(modelList(raw)).toEqual([{ id: "model-a", owned_by: "vendor" }]);
  });

  it("parses JSON embedded in tool arguments and keeps invalid text intact", () => {
    expect(parseEmbeddedJSON('{"database":"Intel-RPL.db","limit":10}')).toEqual({
      value: { database: "Intel-RPL.db", limit: 10 },
      isJSON: true,
    });
    expect(parseEmbeddedJSON("plain tool output")).toEqual({ value: "plain tool output", isJSON: false });
  });

  it("creates a readable preview from HTML-heavy tool results", () => {
    expect(textPreview("<table><tr><td>Raptor Lake</td></tr></table>\n\n## Design Guide"))
      .toBe("Raptor Lake Design Guide");
  });
});
