package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/arthas-k8s-mcp-proxy/k8s"
)

// Session 代表针对单个 K8s Pod 建立的 Arthas MCP 诊断会话状态
type Session struct {
	Namespace     string    `json:"namespace"`      // Pod 所在的命名空间
	PodName       string    `json:"pod_name"`       // 目标 Pod 名称
	ContainerName string    `json:"container_name"` // 目标业务容器名称
	LocalPort     uint16    `json:"local_port"`     // 映射到本机的 SPDY 端口
	MCPSessionID  string    `json:"mcp_session_id"` // Pod 内 Arthas 返回的 MCP 会话标识
	CreatedAt     time.Time `json:"created_at"`     // 会话建立时间戳
	LastActive    time.Time `json:"last_active"`    // 最后一次执行工具调用的活跃时间戳

	forwarder *k8s.PortForwarder
	injector  *k8s.Injector
	mu        sync.Mutex
}

// Touch 刷新当前会话的最近活跃时间戳，防止被看门狗作为空闲会话回收
func (s *Session) Touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActive = time.Now()
}

// BaseURL 获取访问该 Pod 端口转发的本地 HTTP 地址
func (s *Session) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.LocalPort)
}

// Close 优雅关闭诊断会话：断开本地端口转发并调用 stop 卸载 Pod 内的 Arthas 增强字节码
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.forwarder != nil {
		s.forwarder.Stop()
		s.forwarder = nil
	}

	if s.injector != nil {
		if err := s.injector.StopArthas(ctx, s.Namespace, s.PodName, s.ContainerName); err != nil {
			firstErr = err
		}
	}
	return firstErr
}
