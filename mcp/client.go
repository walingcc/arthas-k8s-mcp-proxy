package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/arthas-k8s-mcp-proxy/session"
)

// ArthasClient 封装了与 Pod 内部 Arthas 原生 MCP 服务通信的 HTTP JSON-RPC 客户端
type ArthasClient struct {
	httpClient *http.Client
}

// NewArthasClient 创建 Arthas MCP 客户端实例
func NewArthasClient() *ArthasClient {
	return &ArthasClient{
		httpClient: &http.Client{
			Timeout: 60 * time.Second, // 诊断工具单次执行的最大超时时间
		},
	}
}

// JSONRPCRequest 定义标准的 JSON-RPC 2.0 请求体格式
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// ToolCallParams 定义 tools/call 方法的参数结构
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// JSONRPCResponse 定义标准的 JSON-RPC 2.0 响应体格式
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError 定义 JSON-RPC 错误对象
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolCallResult 定义 MCP 工具返回的内容结构
type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// ToolContent 定义 MCP 工具返回的文本段落
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ensureInitialized 检查并执行与目标 Arthas MCP 服务的初始化握手，获取 mcp-session-id
func (c *ArthasClient) ensureInitialized(ctx context.Context, s *session.Session) error {
	if s.MCPSessionID != "" {
		return nil
	}

	endpoint := fmt.Sprintf("%s/mcp", s.BaseURL())
	log.Printf("[MCP] Initiating Streamable MCP handshake with Pod %s/%s at %s...", s.Namespace, s.PodName, endpoint)

	initPayload := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "arthas-k8s-mcp-gateway",
				"version": "1.0.0",
			},
		},
	}

	bodyBytes, _ := json.Marshal(initPayload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create initialize request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", "2024-11-05")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("initialize handshake failed: %w", err)
	}
	defer resp.Body.Close()

	sessionID := resp.Header.Get("mcp-session-id")
	if sessionID == "" {
		sessionID = resp.Header.Get("Mcp-Session-Id")
	}

	if sessionID == "" {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("arthas mcp did not return mcp-session-id header (status: %d, body: %s)", resp.StatusCode, string(b))
	}

	s.MCPSessionID = sessionID
	log.Printf("[MCP] Handshake succeeded! Acquired mcp-session-id: %s for Pod %s/%s", sessionID, s.Namespace, s.PodName)

	// 发送 initialized 通知，确认握手完成
	notifyPayload := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	}
	nBytes, _ := json.Marshal(notifyPayload)
	nReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(nBytes))
	if err == nil {
		nReq.Header.Set("Content-Type", "application/json")
		nReq.Header.Set("Accept", "application/json")
		nReq.Header.Set("mcp-session-id", s.MCPSessionID)
		nResp, nErr := c.httpClient.Do(nReq)
		if nErr == nil {
			nResp.Body.Close()
		}
	}

	return nil
}

// CallTool 通过本地转发端口向目标 Pod 上的 Arthas MCP 服务发起工具调用
func (c *ArthasClient) CallTool(
	ctx context.Context,
	s *session.Session,
	toolName string,
	arguments map[string]interface{},
) (string, error) {
	s.Touch()

	// 1. 确保已建立 MCP 会话握手
	if err := c.ensureInitialized(ctx, s); err != nil {
		return "", fmt.Errorf("failed to initialize MCP session with Arthas: %w", err)
	}

	endpoint := fmt.Sprintf("%s/mcp", s.BaseURL())

	reqPayload := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      time.Now().UnixNano(),
		Method:  "tools/call",
		Params: ToolCallParams{
			Name:      toolName,
			Arguments: arguments,
		},
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to encode json-rpc payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", "2024-11-05")
	if s.MCPSessionID != "" {
		httpReq.Header.Set("mcp-session-id", s.MCPSessionID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http call to arthas mcp failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// 若会话在服务端失效，清空会话 ID 便于下次重新握手
		s.MCPSessionID = ""
		return "", fmt.Errorf("arthas mcp returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		// 若返回内容不是标准 JSON-RPC 格式，则作为原始字符串返回
		return string(respBytes), nil
	}

	if rpcResp.Error != nil {
		return "", fmt.Errorf("arthas mcp error [%d]: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var toolResult ToolCallResult
	if err := json.Unmarshal(rpcResp.Result, &toolResult); err == nil && len(toolResult.Content) > 0 {
		var output stringsBuilder
		for _, item := range toolResult.Content {
			if item.Text != "" {
				output.WriteString(item.Text)
				output.WriteString("\n")
			}
		}
		return output.String(), nil
	}

	return string(rpcResp.Result), nil
}

type stringsBuilder struct {
	buf bytes.Buffer
}

func (sb *stringsBuilder) WriteString(s string) {
	sb.buf.WriteString(s)
}

func (sb *stringsBuilder) String() string {
	return sb.buf.String()
}
