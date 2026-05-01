// Package mcp implements the Model Context Protocol server for JARV.
//
// JARV can operate as an MCP tool server, exposing its capabilities
// as JSON-RPC 2.0 tools over stdio transport. This allows any MCP-compatible
// client (Claude Desktop, Cursor, etc.) to use JARV as a backend.
//
// Supported tools:
//   - jarv_oracle: Run predictive simulations
//   - jarv_chat: Send chat messages
//   - jarv_stats: Get system statistics
//   - jarv_squad_run: Execute squad workflows
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/mkvinicius/jarv/internal/core/agent"
	"github.com/mkvinicius/jarv/internal/core/oracle"
	"github.com/mkvinicius/jarv/internal/core/shield"
)

// Server is the MCP protocol server that handles JSON-RPC requests.
type Server struct {
	engine *agent.Engine
	oracle *oracle.Oracle
	shield *shield.Shield
	mu     sync.Mutex
}

// NewServer creates a new MCP server with the given components.
func NewServer(e *agent.Engine, o *oracle.Oracle, s *shield.Shield) *Server {
	return &Server{
		engine: e,
		oracle: o,
		shield: s,
	}
}

// Serve starts the MCP stdio server, processing JSON-RPC requests.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error")
			continue
		}

		resp := s.handle(ctx, req)
		if resp != nil {
			b, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			fmt.Println(string(b))
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// jsonRPCRequest represents an incoming JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse represents a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

// jsonRPCError represents a JSON-RPC 2.0 error.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) sendError(id any, code int, msg string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: msg,
		},
	}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}

func (s *Server) handle(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(ctx, req)
	case "tools/list":
		return s.handleToolsList(ctx, req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "ping":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]string{"status": "pong"},
		}
	default:
		s.sendError(req.ID, -32601, "Method not found")
		return nil
	}
}

// handleInitialize handles the MCP initialize request.
func (s *Server) handleInitialize(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]string{
				"name":    "jarv",
				"version": "1.0.0",
			},
		},
	}
}

// handleToolsList returns the list of available MCP tools.
func (s *Server) handleToolsList(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	tools := []map[string]interface{}{
		{
			"name":        "jarv_oracle",
			"description": "Run JARV's Oracle predictive simulation engine",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scenario": map[string]interface{}{
						"type":        "string",
						"description": "The scenario to simulate",
					},
					"mode": map[string]interface{}{
						"type":        "string",
						"description": "Simulation mode: economy|balanced|maximum",
						"enum":        []string{"economy", "balanced", "maximum"},
					},
					"context": map[string]interface{}{
						"type":        "string",
						"description": "Additional context for the simulation",
					},
					"domain": map[string]interface{}{
						"type":        "string",
						"description": "Domain hint: business|product|security|social",
					},
				},
				"required": []string{"scenario"},
			},
		},
		{
			"name":        "jarv_chat",
			"description": "Send a chat message to JARV and get a response",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]interface{}{
						"type":        "string",
						"description": "The message to send",
					},
					"session_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional session ID for conversation continuity",
					},
				},
				"required": []string{"text"},
			},
		},
		{
			"name":        "jarv_stats",
			"description": "Get JARV system statistics",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name":        "jarv_squad_run",
			"description": "Execute a squad workflow",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"squad_id": map[string]interface{}{
						"type":        "string",
						"description": "The squad to run",
					},
					"input": map[string]interface{}{
						"type":        "string",
						"description": "Input for the squad",
					},
				},
				"required": []string{"squad_id"},
			},
		},
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	}
}

// handleToolsCall executes an MCP tool call.
func (s *Server) handleToolsCall(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "Invalid params")
		return nil
	}

	var result interface{}
	var err error

	switch params.Name {
	case "jarv_oracle":
		result, err = s.callOracle(ctx, params.Arguments)
	case "jarv_chat":
		result, err = s.callChat(ctx, params.Arguments)
	case "jarv_stats":
		result, err = s.callStats(ctx, params.Arguments)
	case "jarv_squad_run":
		result, err = s.callSquadRun(ctx, params.Arguments)
	default:
		s.sendError(req.ID, -32601, "Tool not found")
		return nil
	}

	if err != nil {
		s.sendError(req.ID, -32603, err.Error())
		return nil
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": result,
				},
			},
		},
	}
}

func (s *Server) callOracle(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Scenario string `json:"scenario"`
		Mode     string `json:"mode,omitempty"`
		Context  string `json:"context,omitempty"`
		Domain   string `json:"domain,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	mode := oracle.ModeBalanced
	if params.Mode != "" {
		mode = oracle.Mode(params.Mode)
	}

	pred, err := s.oracle.Predict(ctx, oracle.OracleRequest{
		Scenario: params.Scenario,
		Context:  params.Context,
		Domain:   params.Domain,
		Mode:     mode,
	})
	if err != nil {
		return "", err
	}

	return oracle.FormatReport(pred), nil
}

func (s *Server) callChat(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Text      string `json:"text"`
		SessionID string `json:"session_id,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	// Shield inspection
	if s.shield != nil {
		threatLevel, events := s.shield.InspectRequest(ctx, params.SessionID, params.Text)
		if threatLevel >= shield.ThreatHigh {
			return fmt.Sprintf("Request blocked: %v", events), nil
		}
	}

	resp, err := s.engine.Process(ctx, agent.Request{
		SessionID: params.SessionID,
		Text:      params.Text,
	})
	if err != nil {
		return "", err
	}

	// Shield response inspection
	if s.shield != nil {
		sanitized, _ := s.shield.InspectResponse(ctx, params.SessionID, resp.Text)
		return sanitized, nil
	}

	return resp.Text, nil
}

func (s *Server) callStats(ctx context.Context, args json.RawMessage) (string, error) {
	if s.engine == nil {
		return `{"error": "engine not initialized"}`, nil
	}

	stats := map[string]interface{}{
		"active_requests": s.engine.ActiveRequests(),
		"mode":            "balanced",
	}

	b, err := json.Marshal(stats)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Server) callSquadRun(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		SquadID string `json:"squad_id"`
		Input   string `json:"input,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	return "Squad mode not yet implemented. Please use jarv_chat for interactions.", nil
}
