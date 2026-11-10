package k8s

import (
	"bytes"
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// Injector 负责在 Kubernetes Pod 容器内动态注入、启动及清理 Arthas 进程
type Injector struct {
	client *Client
}

// NewInjector 创建注入器实例
func NewInjector(client *Client) *Injector {
	return &Injector{client: client}
}

// FindPod 查找目标 Pod；若在指定命名空间未找到且命名空间为 default 或为空，自动全集群跨 Namespace 智能检索
func (inj *Injector) FindPod(ctx context.Context, namespace, podName string) (*corev1.Pod, string, error) {
	// 1. 优先在指定的命名空间中查询
	pod, err := inj.client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err == nil {
		return pod, namespace, nil
	}

	// 2. 若未找到且命名空间为默认，则在全集群所有 Namespace 中按 Pod 名称搜索
	if namespace == "default" || namespace == "" {
		podList, err := inj.client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", podName),
		})
		if err == nil && len(podList.Items) > 0 {
			foundPod := &podList.Items[0]
			return foundPod, foundPod.Namespace, nil
		}
	}

	return nil, namespace, fmt.Errorf("pod %q not found in namespace %q", podName, namespace)
}

// AutoDetectContainer 自动推断主业务容器（自动跳过 istio-proxy、日志收集等常见 Sidecar）
func (inj *Injector) AutoDetectContainer(pod *corev1.Pod, preferredContainer string) string {
	if preferredContainer != "" {
		return preferredContainer
	}
	if len(pod.Spec.Containers) == 1 {
		return pod.Spec.Containers[0].Name
	}
	// 多容器 Pod：自动跳过网关代理和日志 sidecar
	sidecars := map[string]bool{
		"istio-proxy": true, "envoy": true, "linkerd-proxy": true,
		"filebeat": true, "logstash": true, "fluentd": true, "promtail": true,
	}
	for _, c := range pod.Spec.Containers {
		if !sidecars[c.Name] {
			return c.Name
		}
	}
	return pod.Spec.Containers[0].Name
}

// ExecInPod 在 Pod 指定容器内执行远程 Shell 命令，返回 stdout 与 stderr
func (inj *Injector) ExecInPod(
	ctx context.Context,
	namespace string,
	podName string,
	containerName string,
	cmd []string,
) (string, string, error) {
	req := inj.client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(inj.client.RestConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	return stdout.String(), stderr.String(), err
}

// EnsureArthasRunning 保证目标 Pod 内 Arthas 正在运行：自动下载配置包、解压、查找 Java 进程并执行 attach
func (inj *Injector) EnsureArthasRunning(
	ctx context.Context,
	namespace string,
	podName string,
	containerName string,
	arthasPort int,
	downloadURL string,
) error {
	// 8563 端口的十六进制为 2173；Linux /proc/net/tcp 存在于每个容器内，无需依赖 netstat/ss
	portHex := fmt.Sprintf("%04X", arthasPort)
	checkPortCmd := fmt.Sprintf("grep -qi ':%s ' /proc/net/tcp /proc/net/tcp6 2>/dev/null || (command -v netstat >/dev/null && netstat -tuln 2>/dev/null | grep -q %d) || (command -v curl >/dev/null && curl -s http://127.0.0.1:%d/ >/dev/null 2>&1)", portHex, arthasPort, arthasPort)

	// 1. 检查目标端口是否已经有 Arthas 处于监听状态
	checkCmd := []string{"sh", "-c", checkPortCmd}
	_, _, err := inj.ExecInPod(ctx, namespace, podName, containerName, checkCmd)
	if err == nil {
		// 端口已在监听，无需重复启动
		return nil
	}

	// 2. 准备启动脚本：下载安装包 -> 解压 -> 探测 PID -> 启动 boot jar
	setupScript := fmt.Sprintf(`
set -e
mkdir -p /tmp/arthas-mcp

# 1. 若尚未解压，则执行下载与解压
BOOT_JAR=$(find /tmp/arthas-mcp -name "arthas-boot.jar" 2>/dev/null | head -n 1)
if [ -z "$BOOT_JAR" ]; then
    echo "Downloading Arthas package from %s..."
    PKG_FILE="/tmp/arthas-pkg.tmp"
    if command -v curl >/dev/null 2>&1; then
        curl -sL '%s' -o "$PKG_FILE"
    elif command -v wget >/dev/null 2>&1; then
        wget -q '%s' -O "$PKG_FILE"
    else
        echo "Error: neither curl nor wget found in container" >&2
        exit 1
    fi

    # 解压（支持 unzip 或 JDK 自带的 jar 工具）
    if command -v unzip >/dev/null 2>&1; then
        unzip -o "$PKG_FILE" -d /tmp/arthas-mcp >/dev/null
    elif command -v jar >/dev/null 2>&1; then
        (cd /tmp/arthas-mcp && jar -xf "$PKG_FILE")
    else
        # 若下载的是单 jar 文件
        mv "$PKG_FILE" /tmp/arthas-mcp/arthas-boot.jar
    fi
    rm -f "$PKG_FILE"
fi

BOOT_JAR=$(find /tmp/arthas-mcp -name "arthas-boot.jar" 2>/dev/null | head -n 1)
if [ -z "$BOOT_JAR" ]; then
    echo "Error: arthas-boot.jar not found after extracting package" >&2
    exit 1
fi

# 确保 arthas.properties 位于 /tmp/arthas-mcp 根目录下
PROP_FILE=$(find /tmp/arthas-mcp -name "arthas.properties" 2>/dev/null | head -n 1)
if [ -n "$PROP_FILE" ] && [ "$PROP_FILE" != "/tmp/arthas-mcp/arthas.properties" ]; then
    cp -f "$PROP_FILE" /tmp/arthas-mcp/arthas.properties 2>/dev/null || true
fi

# 2. 定位容器内目标 Java 进程 PID
PID=""
if command -v jps >/dev/null 2>&1; then
    PID=$(jps -l 2>/dev/null | grep -v "sun.tools.jps" | grep -v "arthas" | awk 'NR==1{print $1}')
fi
if [ -z "$PID" ]; then
    PID=$(ps -ef 2>/dev/null | grep java | grep -v grep | grep -v arthas | awk 'NR==1{print $2}')
fi
if [ -z "$PID" ]; then
    PID="1"
fi

# 3. 故障自愈：若目标处于半挂载异常状态（3658 存活但 HTTP 8563 未打开），先执行 stop 清除
if (netstat -tuln 2>/dev/null || ss -tuln 2>/dev/null) | grep -q 3658; then
    if ! (netstat -tuln 2>/dev/null || ss -tuln 2>/dev/null) | grep -q %d; then
        echo "Detected half-attached Arthas (3658 is open but %d is missing). Stopping old instance first..."
        java -jar "$BOOT_JAR" --stop >/dev/null 2>&1 || true
        sleep 1
    fi
fi

echo "Attaching Arthas to Java PID $PID via $BOOT_JAR..."
# 显式指定 --http-port 和 --telnet-port 进行附加
java -jar "$BOOT_JAR" \
    --attach-only \
    --http-port %d \
    --telnet-port 3658 \
    ${PID} >/tmp/arthas-mcp/boot.log 2>&1 || true
`, downloadURL, downloadURL, downloadURL, arthasPort, arthasPort, arthasPort)

	runCmd := []string{"sh", "-c", setupScript}
	out, errOut, err := inj.ExecInPod(ctx, namespace, podName, containerName, runCmd)
	if err != nil {
		return fmt.Errorf("failed to extract and start arthas: %w (out: %s, err: %s)", err, out, errOut)
	}

	// 3. 轮询等待端口就绪（最多等待 15 秒）
	for i := 0; i < 15; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			_, _, pollErr := inj.ExecInPod(ctx, namespace, podName, containerName, checkCmd)
			if pollErr == nil {
				return nil
			}
		}
	}

	logCmd := []string{"sh", "-c", "cat /tmp/arthas-mcp/boot.log 2>/dev/null || cat ~/logs/arthas/arthas.log 2>/dev/null || true"}
	logs, _, _ := inj.ExecInPod(ctx, namespace, podName, containerName, logCmd)
	return fmt.Errorf("timeout waiting for Arthas port %d. Log: %s", arthasPort, logs)
}

// StopArthas 在 Pod 容器内调用 stop 卸载增强类并彻底清理 /tmp/arthas-mcp 临时文件
func (inj *Injector) StopArthas(
	ctx context.Context,
	namespace string,
	podName string,
	containerName string,
) error {
	cleanupCmd := []string{
		"sh", "-c",
		`
BOOT_JAR=$(find /tmp/arthas-mcp -name "arthas-boot.jar" 2>/dev/null | head -n 1)
if [ -n "$BOOT_JAR" ]; then
    java -jar "$BOOT_JAR" --stop >/dev/null 2>&1 || true
fi
# 彻底删除临时目录和下载文件
rm -rf /tmp/arthas-mcp /tmp/arthas-pkg.tmp 2>/dev/null || true
`,
	}
	_, _, err := inj.ExecInPod(ctx, namespace, podName, containerName, cleanupCmd)
	return err
}
