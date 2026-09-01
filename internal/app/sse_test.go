package app

import (
	"encoding/json"
	"testing"
)

func TestSSEAggregatorHandlesFragmentedToolCallsAndUsage(t *testing.T) {
	t.Parallel()
	stream := "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"model\":\"demo\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你\"}}]}\n\n" +
		"data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\",\"tool_calls\":[{\"index\":0,\"id\":\"call_\",\"type\":\"function\",\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\r\n\r\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"1\",\"function\":{\"arguments\":\"\\\"rag\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"total_tokens\":12}}\n\n" +
		"data: [DONE]\n\n"
	aggregator := NewSSEAggregator()
	for index := 0; index < len(stream); index += 7 {
		end := index + 7
		if end > len(stream) {
			end = len(stream)
		}
		aggregator.Feed([]byte(stream[index:end]))
	}
	aggregator.Finish()
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal([]byte(aggregator.JSON()), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || result.Choices[0].Message.Content != "你好" {
		t.Fatalf("unexpected aggregate: %s", aggregator.JSON())
	}
	tool := result.Choices[0].Message.ToolCalls[0]
	if tool.ID != "call_1" || tool.Function.Name != "search" || tool.Function.Arguments != `{"q":"rag"}` {
		t.Fatalf("unexpected tool aggregate: %+v", tool)
	}
	if result.Choices[0].FinishReason != "tool_calls" || result.Usage["total_tokens"] != 12 {
		t.Fatalf("missing finish/usage: %+v", result)
	}
}

func TestSSEAggregatorIgnoresMalformedEvents(t *testing.T) {
	t.Parallel()
	aggregator := NewSSEAggregator()
	aggregator.Feed([]byte("event: ping\ndata: nope\n\ndata: [DONE]\n\n"))
	aggregator.Finish()
	var value map[string]any
	if err := json.Unmarshal([]byte(aggregator.JSON()), &value); err != nil {
		t.Fatal(err)
	}
}
