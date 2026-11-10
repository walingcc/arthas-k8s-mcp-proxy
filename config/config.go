package config

import (
	"flag"
	"os"
	"path/filepath"
	"time"
)

// Config 定义网关的核心运行配置
type Config struct {
	Kubeconfig        string        // Kubeconfig 配置文件路径
	ServerHost        string        // HTTP 监听的主机地址
	ServerPort        string        // HTTP 监听的端口
	BaseURL           string        // 对外宣告的公网 Base URL（如 Nginx 反向代理前缀）
	Transport         string        // MCP 传输层协议类型："sse" 或 "stdio"
	IdleTimeout       time.Duration // Pod 诊断会话空闲超时时长，超时后自动执行 stop 并释放资源
	ArthasDownloadURL string        // 容器内缺失 Arthas 时下载使用的 URL
	ArthasPort        int           // 容器内 Arthas 进程监听的 HTTP 端口（默认 8563）
}

// LoadConfig 从命令行参数及环境变量中加载并初始化配置
func LoadConfig() *Config {
	cfg := &Config{}

	// 检测当前服务是否运行在 Kubernetes 集群内部 Pod 中
	inCluster := os.Getenv("KUBERNETES_SERVICE_HOST") != ""

	defaultKubeconfig := os.Getenv("KUBECONFIG")
	if defaultKubeconfig == "" && !inCluster {
		// 仅在集群外部运行时，默认回退到 ~/.kube/config
		home, err := os.UserHomeDir()
		if err == nil {
			defaultKubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", defaultKubeconfig, "Kubeconfig 配置文件路径（集群内运行可留空）")
	flag.StringVar(&cfg.ServerHost, "host", "0.0.0.0", "HTTP 监听主机地址")
	flag.StringVar(&cfg.ServerPort, "port", "8080", "HTTP 监听端口")
	flag.StringVar(&cfg.BaseURL, "base-url", os.Getenv("BASE_URL"), "对外宣告的 Base URL（例如 http://my-ip:port/subpath）")
	flag.StringVar(&cfg.Transport, "transport", "sse", "MCP 传输协议：sse 或 stdio")

	idleTimeoutMinutes := flag.Int("idle-timeout", 5, "Pod 诊断会话的空闲超时分钟数，超时后自动回收卸载")
	flag.StringVar(&cfg.ArthasDownloadURL, "arthas-url", "https://arthas.aliyun.com/arthas-boot.jar", "容器内缺失 Arthas 时的下载地址")
	flag.IntVar(&cfg.ArthasPort, "arthas-port", 8563, "容器内 Arthas HTTP 服务端口")

	flag.Parse()

	cfg.IdleTimeout = time.Duration(*idleTimeoutMinutes) * time.Minute
	return cfg
}
