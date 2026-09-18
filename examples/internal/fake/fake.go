// Package fake registers an in-process OpenAI-compatible provider so the
// examples run offline. Replace "fake/model" with a real model name to talk to
// a vendor.
package fake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/richardwooding/llmkit"
	"github.com/richardwooding/llmkit/openaicompat"
)

type request struct {
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

// Register starts a fake server whose replies follow a tiny script: when a
// tool named by toolName is available and has not been called yet, call it
// with args; otherwise answer with finalText. It returns a stop function.
func Register(toolName, args, finalText string) func() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		_ = json.NewDecoder(r.Body).Decode(&req)
		hasTool, called := false, false
		for _, t := range req.Tools {
			hasTool = hasTool || t.Function.Name == toolName
		}
		for _, m := range req.Messages {
			called = called || m.Role == "tool"
		}
		if hasTool && !called {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"`+toolName+`","arguments":`+strings.TrimSpace(mustQuote(args))+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":`+mustQuote(finalText)+`},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":10,"total_tokens":40}}`)
	}))
	llmkit.Register(openaicompat.NewProvider(openaicompat.Config{ID: "fake", BaseURL: srv.URL, KeyOptional: true}))
	return srv.Close
}

func mustQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
