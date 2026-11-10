package k8s

import (
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client 封装 Kubernetes Clientset 与底层 REST 配置
type Client struct {
	Clientset  *kubernetes.Clientset
	RestConfig *rest.Config
}

// NewClient 初始化 Kubernetes 连接客户端，优先读取指定的 kubeconfig，留空或失败时自动降级使用 Pod 内的 InCluster ServiceAccount
func NewClient(kubeconfigPath string) (*Client, error) {
	var restConfig *rest.Config
	var err error

	// 1. 若配置了 kubeconfig 路径且文件存在，使用集群外认证
	if kubeconfigPath != "" {
		if _, statErr := os.Stat(kubeconfigPath); statErr == nil {
			restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
			if err == nil {
				fmt.Printf("[K8s] Authenticating via kubeconfig: %s\n", kubeconfigPath)
			}
		}
	}

	// 2. 否则自动回退到集群内 ServiceAccount 认证模式（/var/run/secrets/...）
	if restConfig == nil {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load K8s credentials (neither valid kubeconfig nor running in cluster with ServiceAccount): %w", err)
		}
		fmt.Println("[K8s] Authenticating via In-Cluster ServiceAccount Token (/var/run/secrets/...)")
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return &Client{
		Clientset:  clientset,
		RestConfig: restConfig,
	}, nil
}
