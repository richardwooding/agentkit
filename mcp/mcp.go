package mcp

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/richardwooding/agentkit"
)

const (
	defaultImplName    = "agentkit"
	defaultImplVersion = "0.1.0"
)

type config struct {
	prefix     string
	filter     func(name string) bool
	impl       sdk.Implementation
	sequential bool
}

// Option configures Connect.
type Option func(*config)

// WithPrefix exposes every tool under prefix+name.
func WithPrefix(p string) Option {
	return func(c *config) { c.prefix = p }
}

// WithFilter keeps only the tools whose server-side name satisfies keep.
func WithFilter(keep func(name string) bool) Option {
	return func(c *config) { c.filter = keep }
}

// WithImplementation sets the client name and version sent during the handshake.
func WithImplementation(name, version string) Option {
	return func(c *config) { c.impl = sdk.Implementation{Name: name, Version: version} }
}

// WithSequential marks every tool as Sequential so the loop never calls the server concurrently.
func WithSequential() Option {
	return func(c *config) { c.sequential = true }
}

// Server is an initialized MCP client session whose tools can be exposed to an agent.
type Server struct {
	session *sdk.ClientSession
	cfg     config
}

// Connect performs the MCP handshake over t. Close the Server when done.
func Connect(ctx context.Context, t sdk.Transport, opts ...Option) (*Server, error) {
	cfg := config{impl: sdk.Implementation{Name: defaultImplName, Version: defaultImplVersion}}
	for _, o := range opts {
		o(&cfg)
	}
	session, err := sdk.NewClient(&cfg.impl, nil).Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect: %w", err)
	}
	return &Server{session: session, cfg: cfg}, nil
}

// Tools lists the server's tools as an agentkit.Toolset, following pagination.
func (s *Server) Tools(ctx context.Context) (agentkit.Toolset, error) {
	var ts agentkit.Toolset
	for t, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools: %w", err)
		}
		if s.cfg.filter != nil && !s.cfg.filter(t.Name) {
			continue
		}
		ts = append(ts, newTool(s.session, t, s.cfg))
	}
	return ts, nil
}

// Session returns the underlying SDK session for resources, prompts and notifications.
func (s *Server) Session() *sdk.ClientSession { return s.session }

// Close ends the session; calling it more than once is safe.
func (s *Server) Close() error { return s.session.Close() }

// Command returns a stdio transport that runs the given program.
func Command(name string, args ...string) sdk.Transport {
	return &sdk.CommandTransport{Command: exec.Command(name, args...)} //nolint:gosec // the caller picks the server binary on purpose
}

type httpConfig struct {
	client *http.Client
}

// HTTPOption configures HTTP.
type HTTPOption func(*httpConfig)

// WithHTTPClient sets the http.Client used by the Streamable HTTP transport.
func WithHTTPClient(c *http.Client) HTTPOption {
	return func(h *httpConfig) { h.client = c }
}

// HTTP returns a Streamable HTTP transport for the MCP endpoint at url.
func HTTP(url string, opts ...HTTPOption) sdk.Transport {
	var cfg httpConfig
	for _, o := range opts {
		o(&cfg)
	}
	return &sdk.StreamableClientTransport{Endpoint: url, HTTPClient: cfg.client}
}
