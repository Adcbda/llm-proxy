package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	address := os.Getenv("FAKE_UPSTREAM_LISTEN")
	if address == "" {
		address = "127.0.0.1:18081"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "fake-agent-model", "object": "model", "owned_by": "e2e"},
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if payload.Stream {
			writer.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("data: {\"id\":\"chat_e2e\",\"object\":\"chat.completion.chunk\",\"model\":\"" + payload.Model + "\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello \"}}]}\n\n"))
			flusher.Flush()
			time.Sleep(40 * time.Millisecond)
			_, _ = writer.Write([]byte("data: {\"id\":\"chat_e2e\",\"object\":\"chat.completion.chunk\",\"model\":\"" + payload.Model + "\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"from upstream\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":9}}\n\n"))
			_, _ = writer.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		writeJSON(writer, map[string]any{
			"id": "chat_e2e", "object": "chat.completion", "model": payload.Model,
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "hello from upstream"}}},
			"usage":   map[string]any{"total_tokens": 9},
		})
	})
	log.Printf("fake upstream listening on %s", address)
	log.Fatal(http.ListenAndServe(address, mux))
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
