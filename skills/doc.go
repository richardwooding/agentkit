// Package skills loads Agent Skills (https://agentskills.io) and exposes them to
// an agent with progressive disclosure: the catalog of names and descriptions
// goes into the system prompt, the full SKILL.md body is returned by the "skill"
// tool when the model activates a skill, and bundled files are read on demand
// through the "skill_file" tool.
//
// Skills are read through io/fs.FS only, so they can come from a directory
// (os.DirFS), an embedded tree (embed.FS) or a test map (fstest.MapFS). Which
// directories to scan, and whether to trust them, is the caller's decision.
//
//	set, err := skills.LoadAll(os.DirFS(".agents/skills"))
//	agent, err := agentkit.New("claude-sonnet-4-5",
//		agentkit.WithInstructions("You are a helpful assistant."),
//		skills.Use(set),
//	)
//
// Running the scripts a skill ships and enforcing its allowed-tools list are
// left to the host application.
package skills
