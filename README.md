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

## 🛠 暴露给 AI 的 MCP 工具清单（共 31 个）

网关已完整内置并暴露官方 29 个 Arthas 核心诊断工具 + 2 个网关级运维工具，均支持通过 `pod_name` 自动完成动态注入与会话转发。

### 1. JVM 概况与资源分析（13 个）

| 工具名称 | 功能描述 | 对应排查的问题场景 | 核心参数 |
| :--- | :--- | :--- | :--- |
| **`arthas_dashboard`** | 实时获取 JVM/应用面板 | 整体健康度检查：CPU 飙高、GC 停顿频次、线程总数概览 | `pod_name`, `i`, `n` |
| **`arthas_thread`** | 排查线程信息及堆栈 | 死锁检测（`-b`）、CPU 消耗最高前 N 线程（`-n`）、线程池卡死 | `pod_name`, `n`, `b`, `id`, `state` |
| **`arthas_memory`** | 查看 JVM 内存分代详情 | 堆内存溢出、Eden/Old 占用高、Metaspace/DirectBuffer 泄漏 | `pod_name` |
| **`arthas_jvm`** | 查看 JVM 运行时详细信息 | 启动参数核对、垃圾回收器（GC）累计次数/耗时统计、类加载统计 | `pod_name` |
| **`arthas_heapdump`** | 生成堆内存转储 Dump 文件 | 内存严重泄漏、OOM 根因离线分析（支持 `--live` 仅导出存活对象） | `pod_name`, `live`, `file` |
| **`arthas_mbean`** | 查看或监控 MBean 属性信息 | 监控特定数据源（Druid/HikariCP 连接池占用）、Tomcat/Undertow 线程指标 | `pod_name`, `name`, `attribute` |
| **`arthas_sysprop`** | 查看或动态修改 JVM 系统属性 | 核对 `-D` 启动属性、线上不重启动态修改系统参数 | `pod_name`, `property_name`, `property_value` |
| **`arthas_sysenv`** | 查看容器系统环境变量 | K8s 注入的环境变量是否生效（如配置中心地址、Profile、Secret） | `pod_name`, `env_name` |
| **`arthas_vmoption`** | 查看或更新 VM 选项开关 | 动态开关 GC 日志（`PrintGC`）、动态开启堆转储参数（`HeapDumpOnOutOfMemoryError`） | `pod_name`, `name`, `value` |
| **`arthas_perfcounter`** | 查看 JVM 内部性能计数器 | 深度底层性能分析、JIT 编译耗时与类加载底层计数 | `pod_name` |
| **`arthas_vmtool`** | JVM 虚拟机万能交互工具 | 内存实例统计与获取（`getInstances`）、强制执行 Full GC、线程中断 | `pod_name`, `action`, `className`, `limit` |
| **`arthas_getstatic`** | 查看类的静态字段值 | 线上静态配置变量/常量值核对、排查单例对象运行期内部状态 | `pod_name`, `class_name`, `field_name` |
| **`arthas_ognl`** | 动态执行 OGNL 表达式 | 动态调用 Spring Bean 方法、查看复杂对象内部字段、调用静态工具方法 | `pod_name`, `express` |

### 2. 类加载与字节码管理（8 个）

| 工具名称 | 功能描述 | 对应排查的问题场景 | 核心参数 |
| :--- | :--- | :--- | :--- |
| **`arthas_sc`** | 搜索 JVM 已加载的类信息 | 类冲突、排查某个类到底从哪个 jar 包加载（`code-source`）、验证类加载器 | `pod_name`, `class-pattern`, `d` |
| **`arthas_sm`** | 搜索已加载类的方法信息 | `NoSuchMethodError`、验证发布的代码是否包含某个方法或重载签名 | `pod_name`, `class-pattern`, `method-pattern`, `d` |
| **`arthas_jad`** | 反编译运行中的 class 字节码为源码 | 线上版本核对：确定运行的代码是否包含最新修复 commit，杜绝“代码到底上没上”争议 | `pod_name`, `class-pattern` |
| **`arthas_classloader`** | ClassLoader 诊断工具 | 类加载器继承树分析、双亲委派机制排查、查找特定资源文件位置 | `pod_name`, `tree`, `url` |
| **`arthas_mc`** | 内存编译器（Memory Compiler） | 线上紧急热修复：在内存中将修复后的 `.java` 源码直接编译为 `.class` 字节码 | `pod_name`, `file` |
| **`arthas_redefine`** | 重新加载类的字节码（Redefine） | 结合 `mc` 编译产物，不重启 Pod 直接对目标类进行热更新替换 | `pod_name`, `file` |
| **`arthas_retransform`** | 增强型字节码热替换（Retransform） | 线上动态热替换已加载的类，支持反复加载和取消还原 | `pod_name`, `file` |
| **`arthas_dump`** | 导出 JVM 运行中的 class 字节码 | 保存线上被动态代理（CGLIB/ASM/Spring AOP）增强后的真实 class 文件进行逆向比对 | `pod_name`, `class-pattern`, `directory` |

### 3. 监控诊断与调用追踪（6 个）

| 工具名称 | 功能描述 | 对应排查的问题场景 | 核心参数 |
| :--- | :--- | :--- | :--- |
| **`arthas_monitor`** | 实时统计方法调用指标 | 统计某个接口的真实吞吐量（QPS）、平均 RT 耗时、成功率与失败次数 | `pod_name`, `class-pattern`, `method-pattern`, `c`, `n` |
| **`arthas_stack`** | 抓取当前方法的调用来源堆栈 | 分析特定方法到底是由哪个 Controller、MQ 消费者或定时任务触发的链路来源 | `pod_name`, `class-pattern`, `method-pattern`, `n` |
| **`arthas_trace`** | 追踪方法内部各节点耗时 | **接口变慢首选用它**：逐行展开子方法耗时，定位哪一行代码、慢 SQL 或下游 RPC 导致超时 | `pod_name`, `class-pattern`, `method-pattern`, `condition`, `n` |
| **`arthas_tt`** | 时空隧道（TimeTunnel）调用记录与回放 | 偶发故障排查：记录多次真实调用的入参、出参快照，支持线上发起无损调用重放 | `pod_name`, `class-pattern`, `method-pattern`, `t`, `l`, `i`, `p` |
| **`arthas_watch`** | 实时观察方法入参、返回值与抛出异常 | 业务逻辑异常诊断：无侵入抓取线上触发异常时的入参 `params` 与异常堆栈 `throwExp` | `pod_name`, `class-pattern`, `method-pattern`, `express`, `n` |
| **`arthas_profiler`** | Async Profiler 性能采样与火焰图 | 复杂性能瓶颈：针对 CPU、内存分配（alloc）、锁竞争（lock）采样并直接生成火焰图 | `pod_name`, `action`, `event`, `duration`, `file` |

### 4. Arthas 辅助工具（2 个）

| 工具名称 | 功能描述 | 对应排查的问题场景 | 核心参数 |
| :--- | :--- | :--- | :--- |
| **`arthas_viewfile`** | 限制性分段读取容器内日志/配置文件 | 快速查看应用日志（配合 offset/limit 分页防爆，仅限安全白名单目录） | `pod_name`, `path`, `offset`, `limit` |
| **`arthas_options`** | 查看或修改 Arthas 全局配置开关 | 动态开启或关闭 Arthas 增强保护（如 `unsafe`、`dump`） | `pod_name`, `name`, `value` |

### 5. 网关运维管理工具（2 个）

| 工具名称 | 功能描述 | 对应排查的问题场景 | 核心参数 |
| :--- | :--- | :--- | :--- |
| **`arthas_call_tool`** | 万能自由透传调用原生工具 | 允许 AI 构造任意复杂的原生 JSON-RPC 参数直接透传调用底层的 Arthas 原生工具 | `pod_name`, `tool_name`, `arguments_json` |
| **`arthas_list_sessions`** | 查看活跃诊断会话列表 | 运维排查：查看当前所有被网关接管、已建立 SPDY 隧道转发的 Pod 及会话存活时长 | 无 |

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
