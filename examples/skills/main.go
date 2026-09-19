// Command skills shows Agent Skills loaded from an embedded filesystem.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"

	"github.com/richardwooding/agentkit"
	"github.com/richardwooding/agentkit/examples/internal/fake"
	"github.com/richardwooding/agentkit/skills"
)

//go:embed skills
var skillFS embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	stop := fake.Register(skills.ToolName, `{"name":"pdf-processing"}`, "Loaded the PDF skill; I will report page numbers with every passage.")
	defer stop()

	// Any fs.FS works: os.DirFS(".agents/skills") for a directory on disk.
	root, err := fs.Sub(skillFS, "skills")
	if err != nil {
		return err
	}
	set, err := skills.LoadAll(root)
	if err != nil {
		return err
	}
	for _, p := range set.Problems {
		log.Println("skill problem:", p)
	}
	fmt.Println(set.Prompt())
	fmt.Println()

	agent, err := agentkit.New("fake/model",
		agentkit.WithInstructions("You are a concise assistant."),
		skills.Use(set),
		agentkit.WithBudget(agentkit.Budget{MaxSteps: 5}),
		agentkit.WithHooks(agentkit.Hooks{OnToolResult: func(i agentkit.ToolResultInfo) {
			fmt.Printf("--- %s returned:\n%s\n\n", i.Call.Call.Name, i.Output.Text())
		}}),
	)
	if err != nil {
		return err
	}
	res, err := agent.Run(context.Background(), "Pull the tables out of report.pdf")
	if err != nil {
		return err
	}
	fmt.Println(res.Output)
	return nil
}
