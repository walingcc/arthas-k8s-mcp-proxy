package session

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/arthas-k8s-mcp-proxy/config"
	"github.com/arthas-k8s-mcp-proxy/k8s"
)

// Manager 统一管理所有 Pod 诊断会话的生命周期与空闲回收
type Manager struct {
	cfg      *config.Config
	client   *k8s.Client
	injector *k8s.Injector

	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewManager 创建会话管理器实例
func NewManager(cfg *config.Config, client *k8s.Client) *Manager {
	return &Manager{
		cfg:      cfg,
		client:   client,
		injector: k8s.NewInjector(client),
		sessions: make(map[string]*Session),
	}
}

func sessionKey(namespace, podName string) string {
	return fmt.Sprintf("%s/%s", namespace, podName)
}

// GetOrCreateSession 获取已就绪的诊断会话；若不存在，则动态在 Pod 内注入 Arthas 并建立本地端口转发
func (m *Manager) GetOrCreateSession(
	ctx context.Context,
	namespace string,
	podName string,
	containerName string,
) (*Session, error) {
	key := sessionKey(namespace, podName)

	m.mu.RLock()
	s, exists := m.sessions[key]
	m.mu.RUnlock()

	if exists {
		s.Touch()
		return s, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查锁定
	if s, exists = m.sessions[key]; exists {
		s.Touch()
		return s, nil
	}

	log.Printf("[Session] Initializing Arthas for Pod %s/%s (container: %s)...", namespace, podName, containerName)

	// 自动跨命名空间查找 Pod 并推断主业务容器
	podObj, realNamespace, err := m.injector.FindPod(ctx, namespace, podName)
	if err != nil {
		return nil, fmt.Errorf("failed to locate pod: %w", err)
	}
	namespace = realNamespace
	containerName = m.injector.AutoDetectContainer(podObj, containerName)
	log.Printf("[Session] Resolved target: Pod %s/%s (container: %s, pod IP: %s, status: %s)",
		namespace, podName, containerName, podObj.Status.PodIP, podObj.Status.Phase)

	// 1. 确保 Pod 内部已启动 Arthas HTTP 服务
	err = m.injector.EnsureArthasRunning(
		ctx,
		namespace,
		podName,
		containerName,
		m.cfg.ArthasPort,
		m.cfg.ArthasDownloadURL,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to attach arthas in pod: %w", err)
	}

	// 2. 建立本地到 Pod Arthas 端口的 SPDY 隧道
	log.Printf("[Session] Establishing K8s Port-Forward to Pod %s/%s:%d...", namespace, podName, m.cfg.ArthasPort)
	pf, err := k8s.StartPortForward(ctx, m.client.Clientset, m.client.RestConfig, namespace, podName, m.cfg.ArthasPort)
	if err != nil {
		return nil, fmt.Errorf("failed to establish port-forward: %w", err)
	}

	now := time.Now()
	newSession := &Session{
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: containerName,
		LocalPort:     pf.LocalPort(),
		CreatedAt:     now,
		LastActive:    now,
		forwarder:     pf,
		injector:      m.injector,
	}

	m.sessions[key] = newSession
	log.Printf("[Session] Session ready for %s/%s on local port %d", namespace, podName, pf.LocalPort())
	return newSession, nil
}

// CloseSession 手动断开并清理指定 Pod 的诊断会话
func (m *Manager) CloseSession(ctx context.Context, namespace, podName string) error {
	key := sessionKey(namespace, podName)

	m.mu.Lock()
	s, exists := m.sessions[key]
	if !exists {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, key)
	m.mu.Unlock()

	log.Printf("[Session] Closing session for %s/%s...", namespace, podName)
	return s.Close(ctx)
}

// ListSessions 获取当前所有活跃诊断会话的列表快照
func (m *Manager) ListSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	return list
}

// StartWatchdog 启动后台看门狗定时轮询，自动回收超过 idleTimeout 未活动的空闲会话
func (m *Manager) StartWatchdog(ctx context.Context, checkInterval time.Duration) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkIdleSessions(ctx)
		}
	}
}

// checkIdleSessions 扫描并异步清理所有超时的空闲会话
func (m *Manager) checkIdleSessions(ctx context.Context) {
	m.mu.Lock()
	now := time.Now()
	toClose := make([]*Session, 0)

	for key, s := range m.sessions {
		if now.Sub(s.LastActive) > m.cfg.IdleTimeout {
			log.Printf("[Watchdog] Session %s idle for %v (> %v), scheduling cleanup", key, now.Sub(s.LastActive), m.cfg.IdleTimeout)
			toClose = append(toClose, s)
			delete(m.sessions, key)
		}
	}
	m.mu.Unlock()

	for _, s := range toClose {
		go func(sess *Session) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := sess.Close(cleanupCtx); err != nil {
				log.Printf("[Watchdog] Error cleaning up session %s/%s: %v", sess.Namespace, sess.PodName, err)
			}
		}(s)
	}
}
