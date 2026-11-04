package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewManagerWithDial is NewManager with an injectable transport factory.
//
// It exists so embedders/tests can run a real Manager (real discovery, real
// tool wrapping) against an in-memory MCP server instead of spawning npx. A nil
// dial keeps the production stdio/HTTP transport.
func NewManagerWithDial(cfg Config, dial func(context.Context, ServerConfig) (mcpsdk.Transport, error)) (*Manager, error) {
	m, err := NewManager(cfg)
	if err != nil {
		return nil, err
	}
	if dial != nil {
		m.dial = dial
	}
	return m, nil
}
