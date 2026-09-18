// Command typed shows Run[T]: the agent must return a value matching a Go struct.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/richardwooding/agentkit"
	"github.com/richardwooding/agentkit/examples/internal/fake"
)

type Forecast struct {
	City       string `json:"city"`
	TempC      int    `json:"temp_c"`
	Conditions string `json:"conditions" jsonschema:"one or two words"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	stop := fake.Register(agentkit.FinalAnswerTool, `{"city":"Cape Town","temp_c":24,"conditions":"sunny"}`, "")
	defer stop()

	lookup := agentkit.Func("lookup", "Look up the weather", func(_ context.Context, in struct{ City string }) (string, error) {
		return "24C sunny", nil
	})
	agent, err := agentkit.New("fake/model", agentkit.WithTools(lookup))
	if err != nil {
		return err
	}
	forecast, res, err := agentkit.Run[Forecast](context.Background(), agent, "Forecast for Cape Town?")
	if err != nil {
		return err
	}
	fmt.Printf("%+v (stop: %s)\n", forecast, res.StopReason)
	return nil
}
