package skills

import "github.com/richardwooding/agentkit"

// Use registers the skill tools on an agent and appends the catalog to its
// system prompt. An empty or nil set registers nothing.
func Use(set *Set) agentkit.Option {
	return func(a *agentkit.Agent) error {
		if set.Len() == 0 {
			return nil
		}
		if err := agentkit.WithTools(Tools(set)...)(a); err != nil {
			return err
		}
		return agentkit.WithAdditionalInstructions(set.Prompt())(a)
	}
}
