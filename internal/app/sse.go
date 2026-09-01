package app

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type ToolCallAggregate struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
	Custom struct {
		Name  string `json:"name,omitempty"`
		Input string `json:"input,omitempty"`
	} `json:"custom,omitempty"`
}

type ChoiceAggregate struct {
	Index        int
	Role         string
	Content      string
	Refusal      string
	FinishReason any
	FunctionName string
	FunctionArgs string
	Tools        map[int]*ToolCallAggregate
}

type SSEAggregator struct {
	buffer  []byte
	id      string
	object  string
	created any
	model   string
	usage   any
	choices map[int]*ChoiceAggregate
}

func NewSSEAggregator() *SSEAggregator {
	return &SSEAggregator{choices: make(map[int]*ChoiceAggregate)}
}

func (aggregator *SSEAggregator) Feed(data []byte) {
	aggregator.buffer = append(aggregator.buffer, data...)
	for {
		index, size := nextSSEBoundary(aggregator.buffer)
		if index < 0 {
			break
		}
		event := bytes.Clone(aggregator.buffer[:index])
		aggregator.buffer = aggregator.buffer[index+size:]
		aggregator.consumeEvent(event)
	}
	if len(aggregator.buffer) > 4<<20 {
		aggregator.buffer = aggregator.buffer[len(aggregator.buffer)-(1<<20):]
	}
}

func (aggregator *SSEAggregator) Finish() {
	if len(bytes.TrimSpace(aggregator.buffer)) > 0 {
		aggregator.consumeEvent(aggregator.buffer)
	}
	aggregator.buffer = nil
}

func nextSSEBoundary(data []byte) (int, int) {
	lf := bytes.Index(data, []byte("\n\n"))
	crlf := bytes.Index(data, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		if crlf < 0 {
			return -1, 0
		}
		return crlf, 4
	case crlf < 0 || lf < crlf:
		return lf, 2
	default:
		return crlf, 4
	}
}

func (aggregator *SSEAggregator) consumeEvent(event []byte) {
	lines := strings.Split(strings.ReplaceAll(string(event), "\r\n", "\n"), "\n")
	dataLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(dataLines) == 0 {
		return
	}
	payload := strings.Join(dataLines, "\n")
	if payload == "[DONE]" {
		return
	}
	var chunk map[string]any
	if json.Unmarshal([]byte(payload), &chunk) != nil {
		return
	}
	if value, ok := chunk["id"].(string); ok {
		aggregator.id = value
	}
	if value, ok := chunk["object"].(string); ok {
		aggregator.object = value
	}
	if value, ok := chunk["model"].(string); ok {
		aggregator.model = value
	}
	if value, ok := chunk["created"]; ok {
		aggregator.created = value
	}
	if value, ok := chunk["usage"]; ok && value != nil {
		aggregator.usage = value
	}
	choices, _ := chunk["choices"].([]any)
	for _, rawChoice := range choices {
		choiceMap, _ := rawChoice.(map[string]any)
		index := intNumber(choiceMap["index"])
		choice := aggregator.choices[index]
		if choice == nil {
			choice = &ChoiceAggregate{Index: index, Tools: make(map[int]*ToolCallAggregate)}
			aggregator.choices[index] = choice
		}
		if finish, ok := choiceMap["finish_reason"]; ok && finish != nil {
			choice.FinishReason = finish
		}
		delta, _ := choiceMap["delta"].(map[string]any)
		if role, ok := delta["role"].(string); ok {
			choice.Role = role
		}
		if content, ok := delta["content"].(string); ok {
			choice.Content += content
		}
		if refusal, ok := delta["refusal"].(string); ok {
			choice.Refusal += refusal
		}
		if functionCall, ok := delta["function_call"].(map[string]any); ok {
			choice.FunctionName += stringValue(functionCall["name"])
			choice.FunctionArgs += stringValue(functionCall["arguments"])
		}
		toolCalls, _ := delta["tool_calls"].([]any)
		for _, rawTool := range toolCalls {
			toolMap, _ := rawTool.(map[string]any)
			toolIndex := intNumber(toolMap["index"])
			tool := choice.Tools[toolIndex]
			if tool == nil {
				tool = &ToolCallAggregate{Index: toolIndex}
				choice.Tools[toolIndex] = tool
			}
			tool.ID += stringValue(toolMap["id"])
			if value := stringValue(toolMap["type"]); value != "" {
				tool.Type = value
			}
			if function, ok := toolMap["function"].(map[string]any); ok {
				tool.Function.Name += stringValue(function["name"])
				tool.Function.Arguments += stringValue(function["arguments"])
			}
			if custom, ok := toolMap["custom"].(map[string]any); ok {
				tool.Custom.Name += stringValue(custom["name"])
				tool.Custom.Input += stringValue(custom["input"])
			}
		}
	}
}

func (aggregator *SSEAggregator) JSON() string {
	indexes := make([]int, 0, len(aggregator.choices))
	for index := range aggregator.choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	choices := make([]any, 0, len(indexes))
	for _, index := range indexes {
		choice := aggregator.choices[index]
		message := map[string]any{"role": defaultString(choice.Role, "assistant"), "content": choice.Content}
		if choice.Refusal != "" {
			message["refusal"] = choice.Refusal
		}
		if choice.FunctionName != "" || choice.FunctionArgs != "" {
			message["function_call"] = map[string]any{"name": choice.FunctionName, "arguments": choice.FunctionArgs}
		}
		if len(choice.Tools) > 0 {
			toolIndexes := make([]int, 0, len(choice.Tools))
			for toolIndex := range choice.Tools {
				toolIndexes = append(toolIndexes, toolIndex)
			}
			sort.Ints(toolIndexes)
			tools := make([]any, 0, len(toolIndexes))
			for _, toolIndex := range toolIndexes {
				tool := choice.Tools[toolIndex]
				item := map[string]any{"index": tool.Index, "id": tool.ID, "type": tool.Type}
				if tool.Function.Name != "" || tool.Function.Arguments != "" {
					item["function"] = tool.Function
				}
				if tool.Custom.Name != "" || tool.Custom.Input != "" {
					item["custom"] = tool.Custom
				}
				tools = append(tools, item)
			}
			message["tool_calls"] = tools
		}
		choices = append(choices, map[string]any{
			"index": choice.Index, "finish_reason": choice.FinishReason, "message": message,
		})
	}
	result := map[string]any{
		"id": aggregator.id, "object": "chat.completion.aggregated", "model": aggregator.model,
		"created": aggregator.created, "choices": choices,
	}
	if aggregator.usage != nil {
		result["usage"] = aggregator.usage
	}
	encoded, _ := json.Marshal(result)
	return string(encoded)
}

func intNumber(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		value, _ := strconv.Atoi(typed.String())
		return value
	default:
		return 0
	}
}

func stringValue(value any) string {
	valueString, _ := value.(string)
	return valueString
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
