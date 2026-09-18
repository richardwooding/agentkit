// Command tools shows a typed tool driven by the agent loop.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/richardwooding/agentkit"
	"github.com/richardwooding/agentkit/examples/internal/fake"
	"github.com/richardwooding/agentkit/slogx"
)

type weatherArgs struct {
	City string `json:"city" jsonschema:"city name"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	stop := fake.Register("weather", `{"city":"Cape Town"}`, "It is 24°C and sunny in Cape Town.")
	defer stop()

	weather := agentkit.Func("weather", "Current weather for a city", func(_ context.Context, in weatherArgs) (string, error) {
		return "24°C, sunny in " + in.City, nil
	})
	agent, err := agentkit.New("fake/model",
		agentkit.WithName("assistant"),
		agentkit.WithInstructions("You are a concise assistant."),
		agentkit.WithTools(weather),
		agentkit.WithBudget(agentkit.Budget{MaxSteps: 5}),
		agentkit.WithHooks(slogx.Hooks(slog.New(slog.NewTextHandler(os.Stderr, nil)))),
	)
	if err != nil {
		return err
	}
	res, err := agent.Run(context.Background(), "What's the weather in Cape Town?")
	if err != nil {
		return err
	}
	fmt.Println(res.Output)
	fmt.Printf("%d steps, %d tool calls, %d tokens\n", res.Steps, res.ToolCalls, res.Usage.TotalTokens)
	return nil
}
