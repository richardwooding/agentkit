// Package mcp exposes the tools of a Model Context Protocol server as
// agentkit tools.
//
// The official SDK package is also named mcp, so import this package under an
// alias:
//
//	agentmcp "github.com/richardwooding/agentkit/mcp"
//
//	server, err := agentmcp.Connect(ctx, agentmcp.Command("npx", "-y", "@modelcontextprotocol/server-filesystem", "."),
//		agentmcp.WithPrefix("fs_"))
//	defer server.Close()
//	tools, err := server.Tools(ctx)
//	agent, err := agentkit.New("claude-sonnet-4-5", agentkit.WithTools(tools...))
package mcp
