# Arthas K8s MCP Dynamic Proxy Gateway

专为 **Kubernetes 容器化 Java 应用** 与 **AI 诊断助手（MCP）** 设计的智能动态代理中间件。

---

## 🎯 核心解决的问题

传统的 AI 客户端（如各类对话机器人、Cherry Studio、Cline 等）通常只能配置**固定的 MCP 地址**，无法在对话中动态增删 Pod 的诊断连接。

本项目通过 Go 语言封装了一个**常驻的 MCP Gateway**：
1. **对外（向 AI）**：暴露固定的标准 MCP 接口（原生支持 Streamable HTTP、SSE 与 Stdio 三种传输协议）。
2. **对内（向 K8s）**：当 AI 收到诊断任务（如 `arthas_dashboard(pod_name="order-xxx")`）时，网关在底层**全自动完成**：
   - 检测目标 Pod 容器内是否存在并启动带有 MCP 的 Arthas（自动下载 `arthas-boot.jar`、探测 Java PID、写入 MCP 配置并启动）；
   - 利用 `client-go` 原生 SPDY 协议建立内存级 K8s 端口转发（Port-Forward），映射到本地随机端口；
   - 动态路由与透传 MCP JSON-RPC 诊断请求；
   - **空闲超时看门狗（Watchdog）**：支持诊断完成后自动执行 `stop` 卸载 Arthas 增强类并释放网络隧道，避免常驻开销。

---

## 📐 架构设计

```
+---------------------+           固定的 MCP 端点 (SSE / Stdio)
|   AI 机器人 / Agent  | <============================================+
+---------------------+                                              |
                                                                     v
                                                   +------------------------------------+
                                                   | Arthas K8s MCP Gateway (Go 服务)   |
                                                   |                                    |
                                                   |  1. 固定 MCP Server (SSE / Stdio)  |
                                                   |  2. Pod 会话池 (Session Manager)   |
                                                   |  3. client-go 原生 Exec & 端口转发  |
                                                   |  4. 看门狗自动清理 (TTL Watchdog)    |
                                                   +------------------------------------+
                                                        |               |
                         client-go SPDY 内存隧道        |               | client-go SPDY 内存隧道
                       (自动注入 Arthas + PortForward)   |               |
                                                        v               v
                                                +---------------+ +---------------+
                                                | Pod A (Java)  | | Pod B (Java)  |
                                                | Arthas :8563  | | Arthas :8563  |
                                                +---------------+ +---------------+
```

---

## 🛠 暴露给 AI 的 MCP 工具清单

| 工具名称 | 功能描述 | 核心参数 |
| :--- | :--- | :--- |
| **`arthas_dashboard`** | 实时获取 JVM 概况面板（CPU 占用、内存分配、GC 次数、线程概览） | `pod_name`, `namespace` |
| **`arthas_thread`** | 排查线程堆栈、找出 CPU 消耗最高的前 N 个线程或死锁分析 | `pod_name`, `top_n`, `find_locked` |
| **`arthas_memory`** | 查看 JVM 各分代（Eden, Survivor, Old, Metaspace）内存使用详情 | `pod_name`, `namespace` |
| **`arthas_jvm`** | 查看 JVM 启动参数、JVM 运行时版本、系统属性与环境变量 | `pod_name`, `namespace` |
| **`arthas_jad`** | 反编译已加载的运行中 class 实际字节码为 Java 源码 | `pod_name`, `class_name` |
| **`arthas_sc`** | 搜索 JVM 中已加载的类信息（支持通配符或正则匹配） | `pod_name`, `class_pattern` |
| **`arthas_trace`** | 追踪目标方法的调用链路耗时，自动定位哪行代码慢（内置采样防爆） | `pod_name`, `class_name`, `method_name`, `sample_count` |
| **`arthas_watch`** | 实时捕获方法的入参（params）、返回值（returnObj）及异常（throwExp） | `pod_name`, `class_name`, `method_name`, `express` |
| **`arthas_call_tool`** | 自由透传调用 Arthas 官方原生 29 个诊断工具中的任意一个（如 `vmtool`, `tt`, `profiler` 等） | `pod_name`, `tool_name`, `arguments_json` |
| **`arthas_list_sessions`** | 查看当前所有处于诊断连接中的 Pod 会话及存活时长 | 无 |
| **`arthas_close_session`** | 手动断开目标 Pod 的 Arthas 连接，执行卸载并关闭端口隧道 | `pod_name`, `namespace` |

---

## 🚀 快速启动

### 1. 编译
```bash
go build -o bin/arthas-mcp-gateway .
```

### 2. 本地直接运行（读取 `~/.kube/config`）
```bash
./bin/arthas-mcp-gateway \
  --kubeconfig ~/.kube/config \
  --port 8080 \
  --idle-timeout 5
```
启动后终端将输出：
```
=== Starting Arthas K8s MCP Dynamic Proxy Gateway ===
[K8s] Successfully connected to Kubernetes cluster
[Watchdog] Session watchdog running (idle timeout: 5m0s)
[MCP] Starting Arthas K8s MCP Gateway SSE on 0.0.0.0:8080 (Endpoint: http://localhost:8080/sse)
```

---

## 🤖 接入现有机器人 / AI 客户端

### 方式 1：AstrBot / 现代 AI 平台（Streamable HTTP 模式，推荐）
在平台的 MCP 设置中选择 `streamable_http` 传输方式：
```json
{
  "mcpServers": {
    "k8s-arthas-gateway": {
      "type": "streamable_http",
      "url": "http://127.0.0.1:8080/mcp"
    }
  }
}
```
> 注：网关已兼容任意路径后缀（如 `/`、`/mcp`、`/sse` 均可），且全局支持 CORS 与 `mcp-session-id` 会话跟踪。

### 方式 2：Cherry Studio / 自定义 Bot（SSE 模式）
在客户端 MCP 设置中添加：
```json
{
  "mcpServers": {
    "k8s-arthas-gateway": {
      "type": "sse",
      "url": "http://127.0.0.1:8080/sse"
    }
  }
}
```

### 方式 3：Claude Desktop（Stdio 模式）
启动时传递 `--transport stdio` 参数：
```json
{
  "mcpServers": {
    "k8s-arthas-gateway": {
      "command": "/absolute/path/to/arthas-mcp-gateway",
      "args": ["--transport", "stdio", "--kubeconfig", "/Users/yourname/.kube/config"]
    }
  }
}
```

---

## ☸️ 集群内部署（可选：作为 K8s 常驻 Service）

若希望直接部署在 K8s 集群内部，提供给集群内的 AI 服务：

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: arthas-mcp-gateway
  namespace: ops-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: arthas-mcp-gateway-role
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["pods/exec", "pods/portforward"]
  verbs: ["create", "get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: arthas-mcp-gateway-binding
subjects:
- kind: ServiceAccount
  name: arthas-mcp-gateway
  namespace: ops-system
roleRef:
  kind: ClusterRole
  name: arthas-mcp-gateway-role
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: arthas-mcp-gateway
  namespace: ops-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: arthas-mcp-gateway
  template:
    metadata:
      labels:
        app: arthas-mcp-gateway
    spec:
      serviceAccountName: arthas-mcp-gateway
      containers:
      - name: gateway
        image: arthas-mcp-gateway:latest
        ports:
        - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: arthas-mcp-gateway
  namespace: ops-system
spec:
  selector:
    app: arthas-mcp-gateway
  ports:
  - port: 8080
    targetPort: 8080
```
