package k8s

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwarder 维护一个指向目标 Pod 端口的本地网络转发隧道
type PortForwarder struct {
	stopChan  chan struct{}
	readyChan chan struct{}
	localPort uint16
	errChan   chan error
}

// LocalPort 返回操作系统分配给该隧道的本地随机监听端口
func (pf *PortForwarder) LocalPort() uint16 {
	return pf.localPort
}

// Stop 停止并关闭当前端口转发隧道
func (pf *PortForwarder) Stop() {
	select {
	case <-pf.stopChan:
		// 已经关闭，忽略
	default:
		close(pf.stopChan)
	}
}

// StartPortForward 建立指向目标 Pod 端口（如 Arthas HTTP 8563）的 SPDY 隧道，
// 本地端口由操作系统随机动态分配（0:remotePort），避免多 Pod 冲突。
func StartPortForward(
	ctx context.Context,
	clientset kubernetes.Interface,
	restConfig *rest.Config,
	namespace string,
	podName string,
	remotePort int,
) (*PortForwarder, error) {
	// 使用 client-go 原生 RESTClient 构建 portforward 子资源 URL，
	// 能够无缝兼容带有反向代理子路径的 API Server 地址（如 Rancher 的 /k8s/clusters/...）
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward")

	serverURL := req.URL()

	roundTripper, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create spdy round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, serverURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	var outBuff, errBuff bytes.Buffer

	// "0:remotePort" 表示由操作系统自动选择一个空闲的本地端口
	ports := []string{fmt.Sprintf("0:%d", remotePort)}

	forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, &outBuff, &errBuff)
	if err != nil {
		return nil, fmt.Errorf("failed to init port forwarder: %w", err)
	}

	go func() {
		if err := forwarder.ForwardPorts(); err != nil {
			errChan <- fmt.Errorf("port forward error: %w (stderr: %s)", err, errBuff.String())
		}
	}()

	select {
	case <-readyChan:
		forwardedPorts, err := forwarder.GetPorts()
		if err != nil || len(forwardedPorts) == 0 {
			close(stopChan)
			return nil, fmt.Errorf("failed to get forwarded ports: %w", err)
		}
		localPort := forwardedPorts[0].Local
		return &PortForwarder{
			stopChan:  stopChan,
			readyChan: readyChan,
			localPort: localPort,
			errChan:   errChan,
		}, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		close(stopChan)
		return nil, ctx.Err()
	}
}
