package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arthas-k8s-mcp-proxy/config"
	"github.com/arthas-k8s-mcp-proxy/k8s"
	"github.com/arthas-k8s-mcp-proxy/mcp"
	"github.com/arthas-k8s-mcp-proxy/session"
)

func main() {
	log.Println("=== Starting Arthas K8s MCP Dynamic Proxy Gateway ===")

	cfg := config.LoadConfig()

	// 1. 初始化 Kubernetes 客户端
	k8sClient, err := k8s.NewClient(cfg.Kubeconfig)
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}
	log.Println("[K8s] Successfully connected to Kubernetes cluster")

	// 2. 初始化会话管理器与空闲看门狗
	sessionMgr := session.NewManager(cfg, k8sClient)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动看门狗后台协程：每隔 30 秒扫描并清理超时空闲的 Pod 会话
	go sessionMgr.StartWatchdog(ctx, 30*time.Second)
	log.Printf("[Watchdog] Session watchdog running (idle timeout: %v)", cfg.IdleTimeout)

	// 3. 构建并初始化 MCP 网关服务端
	mcpGateway := mcp.NewServer(cfg, sessionMgr)

	// 4. 监听系统信号以实现优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		cancel()

		// 优雅清理所有处于活动状态的 Pod 诊断会话（执行 stop 卸载并关闭端口转发）
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, sess := range sessionMgr.ListSessions() {
			_ = sess.Close(cleanupCtx)
		}
		os.Exit(0)
	}()

	// 5. 启动 MCP 网关（支持 Streamable HTTP / SSE / Stdio）
	if err := mcpGateway.Start(ctx); err != nil {
		log.Fatalf("MCP Server terminated with error: %v", err)
	}
}
