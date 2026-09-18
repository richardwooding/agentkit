// Command multiagent shows a sub-agent exposed as a tool and a parallel Map.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/richardwooding/agentkit"
	"github.com/richardwooding/agentkit/examples/internal/fake"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	stop := fake.Register("researcher", `{"input":"Go iterators"}`, "Summary: iter.Seq2 yields pairs; break stops the producer.")
	defer stop()

	researcher, err := agentkit.New("fake/model", agentkit.WithName("researcher"),
		agentkit.WithInstructions("Research the topic and report the key facts."))
	if err != nil {
		return err
	}
	writer, err := agentkit.New("fake/model", agentkit.WithName("writer"),
		agentkit.WithInstructions("Delegate research, then write a short summary."),
		agentkit.WithTools(agentkit.AsTool(researcher, "researcher", "Research a topic in depth")))
	if err != nil {
		return err
	}
	res, err := writer.Run(context.Background(), "Write about Go iterators.")
	if err != nil {
		return err
	}
	fmt.Println(res.Output)
	fmt.Printf("usage incl. sub-agent: %d tokens\n", res.Usage.TotalTokens)

	topics := []string{"channels", "generics", "iterators"}
	outs, err := agentkit.Map(context.Background(), 3, topics, func(ctx context.Context, topic string) (string, error) {
		r, err := researcher.Run(ctx, "Explain "+topic)
		if err != nil {
			return "", err
		}
		return r.Output, nil
	})
	if err != nil {
		return err
	}
	for i, o := range outs {
		fmt.Printf("%s: %s\n", topics[i], o)
	}
	return nil
}
