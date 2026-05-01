// Package mcp provides the MCP (Model Context Protocol) adapter for JARV.
//
// The adapter bridges JARV's internal components with the MCP protocol,
// allowing JARV to be used as a tool server by MCP-compatible clients.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package mcp

import (
	"context"

	"github.com/mkvinicius/jarv/internal/core/agent"
	"github.com/mkvinicius/jarv/internal/core/oracle"
	"github.com/mkvinicius/jarv/internal/core/shield"
)

// Adapter wraps JARV components for MCP protocol integration.
type Adapter struct {
	engine *agent.Engine
	oracle *oracle.Oracle
	shield *shield.Shield
	server *Server
}

// NewAdapter creates a new MCP adapter.
func NewAdapter(e *agent.Engine, o *oracle.Oracle, s *shield.Shield) *Adapter {
	return &Adapter{
		engine: e,
		oracle: o,
		shield: s,
		server: NewServer(e, o, s),
	}
}

// Serve starts the MCP server using stdio transport.
func (a *Adapter) Serve(ctx context.Context) error {
	return a.server.Serve(ctx)
}

// Engine returns the underlying agent engine.
func (a *Adapter) Engine() *agent.Engine {
	return a.engine
}

// Oracle returns the underlying Oracle.
func (a *Adapter) Oracle() *oracle.Oracle {
	return a.oracle
}

// Shield returns the underlying Shield.
func (a *Adapter) Shield() *shield.Shield {
	return a.shield
}
