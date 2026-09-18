// Package agentkit runs LLM agents on top of llmkit: typed tools generated
// from Go structs, a budgeted tool-calling loop with streaming events and
// hooks, conversation memory, and multi-agent composition.
//
//	weather := agentkit.Func("weather", "Current weather for a city",
//		func(ctx context.Context, in struct {
//			City string `json:"city" jsonschema:"city name"`
//		}) (string, error) {
//			return lookup(in.City), nil
//		})
//	agent, err := agentkit.New("claude-sonnet-4-5",
//		agentkit.WithInstructions("You are a concise assistant."),
//		agentkit.WithTools(weather))
//	res, err := agent.Run(ctx, "What's the weather in Cape Town?")
//	fmt.Println(res.Output)
package agentkit
