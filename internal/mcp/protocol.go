package mcp

import "encoding/json"

// JSON-RPC 2.0 types

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string   `json:"jsonrpc"`
	ID      any      `json:"id"`
	Result  any      `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	RPCParseError     = -32700
	RPCInvalidRequest = -32600
	RPCMethodNotFound = -32601
	RPCInvalidParams  = -32602
	RPCInternalError  = -32603
)

func errResponse(id any, code int, msg string) Response {
	return Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func okResponse(id any, result any) Response {
	return Response{JSONRPC: "2.0", ID: id, Result: result}
}

// MCP protocol types

const MCPProtocolVersion = "2024-11-05"

type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      map[string]any `json:"clientInfo"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      map[string]any     `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
