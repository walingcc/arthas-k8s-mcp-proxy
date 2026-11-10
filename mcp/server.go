package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/arthas-k8s-mcp-proxy/config"
	"github.com/arthas-k8s-mcp-proxy/session"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// sseSession 代表一个客户端维持的活跃 SSE 长连接
type sseSession struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	done    chan struct{}
}

// Server 统一承载 MCP 服务端：同时支持 Streamable HTTP、传统 SSE 与 Stdio 三种传输模式
type Server struct {
	cfg         *config.Config
	sessionMgr  *session.Manager
	mcpServer   *server.MCPServer
	httpServer  *http.Server
	sseSessions sync.Map // sessionID -> *sseSession
}

// NewServer 创建并注册所有诊断工具的 MCP 服务端实例
func NewServer(cfg *config.Config, sessionMgr *session.Manager) *Server {
	s := server.NewMCPServer(
		"arthas-k8s-mcp-proxy",
		"1.0.0",
		server.WithLogging(),
	)

	arthasClient := NewArthasClient()
	registry := NewToolRegistry(sessionMgr, arthasClient)
	registry.RegisterAll(s)

	return &Server{
		cfg:        cfg,
		sessionMgr: sessionMgr,
		mcpServer:  s,
	}
}

// Start 启动 MCP 服务端：支持现代 Streamable HTTP（POST / 或 POST /mcp）、老版 SSE（GET /sse + POST /message）及 Stdio 模式
func (s *Server) Start(ctx context.Context) error {
	if s.cfg.Transport == "stdio" {
		log.Println("[MCP] Starting Arthas K8s MCP Gateway in Stdio mode...")
		return server.ServeStdio(s.mcpServer)
	}

	addr := fmt.Sprintf("%s:%s", s.cfg.ServerHost, s.cfg.ServerPort)
	baseURL := strings.TrimRight(s.cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%s", s.cfg.ServerPort)
	}

	log.Printf("[MCP] Starting Arthas K8s MCP Gateway on %s", addr)
	log.Printf("[MCP] Streamable HTTP Endpoint : %s/ (or %s/mcp)", baseURL, baseURL)
	log.Printf("[MCP] SSE Endpoint             : %s/sse", baseURL)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s,
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Println("[MCP] Shutting down HTTP server...")
		return s.httpServer.Shutdown(context.Background())
	}
}

// ServeHTTP 核心分发路由：
// 1. 全局 CORS 跨域放行（OPTIONS）
// 2. 存活探针（/healthz）
// 3. 现代 Streamable HTTP（对任何路径的 POST 请求执行 JSON-RPC 交互）
// 4. 传统 SSE 模式（GET /sse 或带有 text/event-stream 头的请求）
// 5. 状态探测与信息展示（普通浏览器 GET 请求）
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. 设置通用的 CORS 响应头，允许 Web 端 AI 平台跨域调用
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, HEAD, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, mcp-session-id, Accept")
	w.Header().Set("Access-Control-Expose-Headers", "mcp-session-id")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 2. 健康检查探针
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// 3. 提取或签发会话标识（mcp-session-id）
	sessionID := r.Header.Get("mcp-session-id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// 4. Streamable HTTP：对所有 POST 请求执行 MCP JSON-RPC 协议解析
	if r.Method == http.MethodPost {
		s.handleStreamableHTTP(w, r, sessionID)
		return
	}

	// 5. GET 请求处理（区分 SSE 长连接流与普通探针）
	if r.Method == http.MethodGet {
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "text/event-stream") || strings.HasSuffix(r.URL.Path, "/sse") {
			s.handleSSEStream(w, r, sessionID)
			return
		}

		// 普通浏览器或监控探针访问：返回网关基本信息
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":       "arthas-k8s-mcp-proxy",
			"version":    "1.0.0",
			"status":     "ready",
			"transports": []string{"streamable_http", "sse"},
			"tools":      31,
		})
		return
	}

	// 6. DELETE 请求：支持客户端显式通知注销诊断会话
	if r.Method == http.MethodDelete {
		if val, ok := s.sseSessions.LoadAndDelete(sessionID); ok {
			sess := val.(*sseSession)
			close(sess.done)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleStreamableHTTP 处理基于 HTTP POST 的单轮或批处理 JSON-RPC 请求
func (s *Server) handleStreamableHTTP(w http.ResponseWriter, r *http.Request, sessionID string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		http.Error(w, "Empty body", http.StatusBadRequest)
		return
	}

	ctx := s.mcpServer.WithContext(r.Context(), server.NotificationContext{
		ClientID:  sessionID,
		SessionID: sessionID,
	})

	w.Header().Set("mcp-session-id", sessionID)

	// 兼容批量 JSON-RPC 请求（以 '[' 开头）
	if trimmed[0] == '[' {
		var rawList []json.RawMessage
		if err := json.Unmarshal(trimmed, &rawList); err != nil {
			http.Error(w, "Invalid JSON-RPC batch", http.StatusBadRequest)
			return
		}
		var responses []mcp.JSONRPCMessage
		for _, raw := range rawList {
			resp := s.mcpServer.HandleMessage(ctx, raw)
			if resp != nil {
				responses = append(responses, resp)
			}
		}
		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		respBytes, _ := json.Marshal(responses)
		s.broadcastToSSE(sessionID, respBytes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
		return
	}

	// 处理单个 JSON-RPC 消息
	resp := s.mcpServer.HandleMessage(ctx, trimmed)
	if resp == nil {
		// 单向通知（如 notifications/initialized），按规范返回 202 Accepted
		w.WriteHeader(http.StatusAccepted)
		return
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	// 若当前 sessionID 同时挂载了 SSE 监听，同步推送事件
	s.broadcastToSSE(sessionID, respBytes)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

// handleSSEStream 维持 SSE 长连接，并向客户端推送初始 endpoint 握手事件
func (s *Server) handleSSEStream(w http.ResponseWriter, r *http.Request, sessionID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("mcp-session-id", sessionID)

	sess := &sseSession{
		writer:  w,
		flusher: flusher,
		done:    make(chan struct{}),
	}
	s.sseSessions.Store(sessionID, sess)
	defer func() {
		s.sseSessions.Delete(sessionID)
	}()

	baseURL := strings.TrimRight(s.cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%s", s.cfg.ServerPort)
	}

	messageEndpoint := fmt.Sprintf("%s/message?sessionId=%s", baseURL, sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\r\n\r\n", messageEndpoint)
	flusher.Flush()

	select {
	case <-sess.done:
	case <-r.Context().Done():
	}
}

// broadcastToSSE 向对应 sessionID 的活跃 SSE 连接广播事件
func (s *Server) broadcastToSSE(sessionID string, data []byte) {
	if val, ok := s.sseSessions.Load(sessionID); ok {
		sess := val.(*sseSession)
		fmt.Fprintf(sess.writer, "event: message\ndata: %s\r\n\r\n", string(data))
		sess.flusher.Flush()
	}
}
