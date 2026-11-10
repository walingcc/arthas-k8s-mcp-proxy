package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/arthas-k8s-mcp-proxy/session"
	mcpSDK "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ToolRegistry struct {
	sessionMgr   *session.Manager
	arthasClient *ArthasClient
}

func NewToolRegistry(sm *session.Manager, ac *ArthasClient) *ToolRegistry {
	return &ToolRegistry{
		sessionMgr:   sm,
		arthasClient: ac,
	}
}

func (tr *ToolRegistry) RegisterAll(s *server.MCPServer) {
	// ==========================================
	// 1. JVM 相关工具 (13 个)
	// ==========================================
	s.AddTool(mcpSDK.NewTool("arthas_dashboard",
		mcpSDK.WithDescription("实时展示 JVM/应用面板，支持自定义刷新间隔和次数控制"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("container", mcpSDK.Description("目标容器名称")),
		mcpSDK.WithNumber("i", mcpSDK.Description("刷新间隔(毫秒)，默认 5000")),
		mcpSDK.WithNumber("n", mcpSDK.Description("执行刷新的次数，默认 1")),
	), tr.makePassthroughHandler("dashboard"))

	s.AddTool(mcpSDK.NewTool("arthas_heapdump",
		mcpSDK.WithDescription("生成 JVM heap dump 文件，支持 --live 选项只导出存活对象"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("container", mcpSDK.Description("目标容器名称")),
		mcpSDK.WithBoolean("live", mcpSDK.Description("是否只导出存活对象")),
		mcpSDK.WithString("file", mcpSDK.Description("保存文件路径")),
	), tr.makePassthroughHandler("heapdump"))

	s.AddTool(mcpSDK.NewTool("arthas_jvm",
		mcpSDK.WithDescription("查看当前 JVM 的详细运行时信息"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("container", mcpSDK.Description("目标容器名称")),
	), tr.makePassthroughHandler("jvm"))

	s.AddTool(mcpSDK.NewTool("arthas_mbean",
		mcpSDK.WithDescription("查看或监控 MBean 属性信息，支持实时刷新和模式匹配"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("name", mcpSDK.Description("MBean 名称模式，如 java.lang:type=Threading")),
		mcpSDK.WithString("attribute", mcpSDK.Description("属性名称")),
	), tr.makePassthroughHandler("mbean"))

	s.AddTool(mcpSDK.NewTool("arthas_memory",
		mcpSDK.WithDescription("查看 JVM 的内存使用情况"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("container", mcpSDK.Description("目标容器名称")),
	), tr.makePassthroughHandler("memory"))

	s.AddTool(mcpSDK.NewTool("arthas_thread",
		mcpSDK.WithDescription("查看线程信息及堆栈，支持查找阻塞线程和最忙线程"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithNumber("n", mcpSDK.Description("查看最忙的前 N 个线程堆栈")),
		mcpSDK.WithBoolean("b", mcpSDK.Description("找出当前阻塞其他线程的线程")),
		mcpSDK.WithNumber("id", mcpSDK.Description("查看指定线程 ID 的堆栈")),
		mcpSDK.WithString("state", mcpSDK.Description("按状态过滤线程 (RUNNABLE, WAITING, BLOCKED 等)")),
	), tr.makePassthroughHandler("thread"))

	s.AddTool(mcpSDK.NewTool("arthas_sysprop",
		mcpSDK.WithDescription("查看或修改系统属性，支持动态修改 JVM 系统属性"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("property_name", mcpSDK.Description("系统属性键名")),
		mcpSDK.WithString("property_value", mcpSDK.Description("设置新的属性值")),
	), tr.makePassthroughHandler("sysprop"))

	s.AddTool(mcpSDK.NewTool("arthas_sysenv",
		mcpSDK.WithDescription("查看系统环境变量"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("env_name", mcpSDK.Description("环境变量名")),
	), tr.makePassthroughHandler("sysenv"))

	s.AddTool(mcpSDK.NewTool("arthas_vmoption",
		mcpSDK.WithDescription("查看或更新 VM 选项，支持动态调整 JVM 参数"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
		mcpSDK.WithString("name", mcpSDK.Description("VM 选项名，例如 PrintGC")),
		mcpSDK.WithString("value", mcpSDK.Description("更新的 VM 选项值")),
	), tr.makePassthroughHandler("vmoption"))

	s.AddTool(mcpSDK.NewTool("arthas_perfcounter",
		mcpSDK.WithDescription("查看 JVM Perf Counter 性能计数器信息"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("perfcounter"))

	s.AddTool(mcpSDK.NewTool("arthas_vmtool",
		mcpSDK.WithDescription("虚拟机工具集合，支持强制 GC、获取实例、线程中断等"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("action", mcpSDK.Required(), mcpSDK.Description("操作类型：getInstances, forceGc, interruptThread")),
		mcpSDK.WithString("className", mcpSDK.Description("目标类全名")),
		mcpSDK.WithString("express", mcpSDK.Description("执行的表达式")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("vmtool"))

	s.AddTool(mcpSDK.NewTool("arthas_getstatic",
		mcpSDK.WithDescription("查看类的静态字段值，支持指定 ClassLoader 和 OGNL 表达式"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class_name", mcpSDK.Required(), mcpSDK.Description("目标类名")),
		mcpSDK.WithString("field_name", mcpSDK.Required(), mcpSDK.Description("静态字段名")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("getstatic"))

	s.AddTool(mcpSDK.NewTool("arthas_ognl",
		mcpSDK.WithDescription("执行 OGNL 表达式，动态调用方法和访问字段"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("express", mcpSDK.Required(), mcpSDK.Description("OGNL 表达式")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("ognl"))

	// ==========================================
	// 2. Class/ClassLoader 相关工具 (8 个)
	// ==========================================
	s.AddTool(mcpSDK.NewTool("arthas_sc",
		mcpSDK.WithDescription("搜索 JVM 已加载的类，支持通配符和正则表达式匹配，可查看类详情"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithBoolean("d", mcpSDK.Description("打印详细类信息")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("sc"))

	s.AddTool(mcpSDK.NewTool("arthas_sm",
		mcpSDK.WithDescription("搜索已加载类的方法信息，支持通配符和正则，可查看方法签名、参数等"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("method-pattern", mcpSDK.Description("方法名匹配模式")),
		mcpSDK.WithBoolean("d", mcpSDK.Description("打印方法详细信息")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("sm"))

	s.AddTool(mcpSDK.NewTool("arthas_jad",
		mcpSDK.WithDescription("反编译指定已加载类的源码，将 JVM 中实际运行的 class 字节码反编译为 Java 代码"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("要反编译的类名")),
		mcpSDK.WithString("method-name", mcpSDK.Description("指定只反编译的方法名")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("jad"))

	s.AddTool(mcpSDK.NewTool("arthas_classloader",
		mcpSDK.WithDescription("ClassLoader 诊断工具，查看类加载器统计、继承树、URLs，支持资源查找和类加载操作"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithBoolean("t", mcpSDK.Description("打印 ClassLoader 树")),
		mcpSDK.WithBoolean("l", mcpSDK.Description("按类加载器实例统计已加载的类")),
		mcpSDK.WithString("classLoaderClass", mcpSDK.Description("指定类加载器的类名")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("classloader"))

	s.AddTool(mcpSDK.NewTool("arthas_mc",
		mcpSDK.WithDescription("内存编译器，将 .java 源码文件编译为 .class 字节码文件"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("source_file", mcpSDK.Required(), mcpSDK.Description(".java 源代码文件路径")),
		mcpSDK.WithString("dest_dir", mcpSDK.Description("输出目录")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("mc"))

	s.AddTool(mcpSDK.NewTool("arthas_redefine",
		mcpSDK.WithDescription("重新加载类的字节码，允许在 JVM 运行时对已存在的类进行热更新"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class_path", mcpSDK.Required(), mcpSDK.Description(".class 文件绝对路径")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("redefine"))

	s.AddTool(mcpSDK.NewTool("arthas_retransform",
		mcpSDK.WithDescription("热加载类的字节码，对已加载的类进行字节码修改并使其生效"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class_path", mcpSDK.Required(), mcpSDK.Description(".class 文件绝对路径")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("retransform"))

	s.AddTool(mcpSDK.NewTool("arthas_dump",
		mcpSDK.WithDescription("将 JVM 中实际运行的 class 字节码导出到指定目录，适用于批量下载指定包的字节码"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("directory", mcpSDK.Description("保存目标目录")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("dump"))

	// ==========================================
	// 3. 监控诊断工具 (6 个)
	// ==========================================
	s.AddTool(mcpSDK.NewTool("arthas_monitor",
		mcpSDK.WithDescription("实时监控指定类的指定方法的调用情况，输出调用次数、成功率、平均 RT 等统计信息"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("method-pattern", mcpSDK.Required(), mcpSDK.Description("方法名匹配模式")),
		mcpSDK.WithNumber("c", mcpSDK.Description("周期时间(秒)，默认 5")),
		mcpSDK.WithNumber("n", mcpSDK.Description("监控次数，默认 1")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("monitor"))

	s.AddTool(mcpSDK.NewTool("arthas_stack",
		mcpSDK.WithDescription("输出当前方法被调用的调用路径，帮助分析方法的调用链路"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("method-pattern", mcpSDK.Required(), mcpSDK.Description("方法名匹配模式")),
		mcpSDK.WithNumber("n", mcpSDK.Description("抓取次数，默认 1")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("stack"))

	s.AddTool(mcpSDK.NewTool("arthas_trace",
		mcpSDK.WithDescription("追踪方法内部调用路径，输出每个节点的耗时信息，支持条件过滤"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("method-pattern", mcpSDK.Required(), mcpSDK.Description("方法名匹配模式")),
		mcpSDK.WithNumber("n", mcpSDK.Description("抓取采样次数，默认 3")),
		mcpSDK.WithString("condition", mcpSDK.Description("条件表达式，例如 '#cost > 10'")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("trace"))

	s.AddTool(mcpSDK.NewTool("arthas_tt",
		mcpSDK.WithDescription("方法执行数据的时空隧道，记录指定方法每次调用的入参和返回信息，支持事后查看和重放"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("method-pattern", mcpSDK.Description("方法名匹配模式")),
		mcpSDK.WithNumber("n", mcpSDK.Description("记录次数")),
		mcpSDK.WithBoolean("t", mcpSDK.Description("开启时空隧道记录")),
		mcpSDK.WithBoolean("l", mcpSDK.Description("查看记录列表")),
		mcpSDK.WithNumber("i", mcpSDK.Description("指定时间片段 index 查看或重放")),
		mcpSDK.WithBoolean("p", mcpSDK.Description("重放执行")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("tt"))

	s.AddTool(mcpSDK.NewTool("arthas_watch",
		mcpSDK.WithDescription("观察指定方法的调用情况，包含入参、返回值和抛出异常等信息，支持实时流式输出"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("class-pattern", mcpSDK.Required(), mcpSDK.Description("类名匹配模式")),
		mcpSDK.WithString("method-pattern", mcpSDK.Required(), mcpSDK.Description("方法名匹配模式")),
		mcpSDK.WithString("express", mcpSDK.Description("观察表达式，例如 '{params,returnObj,throwExp}'")),
		mcpSDK.WithNumber("n", mcpSDK.Description("观察次数，默认 1")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("watch"))

	s.AddTool(mcpSDK.NewTool("arthas_profiler",
		mcpSDK.WithDescription("Async Profiler 诊断工具，用于采样 CPU/alloc/lock 等事件并输出火焰图、JFR 等格式"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("action", mcpSDK.Required(), mcpSDK.Description("操作：start, stop, status, list, dump")),
		mcpSDK.WithString("event", mcpSDK.Description("采样事件：cpu, alloc, lock")),
		mcpSDK.WithNumber("duration", mcpSDK.Description("采样持续时间(秒)")),
		mcpSDK.WithString("file", mcpSDK.Description("火焰图输出路径")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("profiler"))

	// ==========================================
	// 4. Arthas 辅助工具 (2 个)
	// ==========================================
	s.AddTool(mcpSDK.NewTool("arthas_viewfile",
		mcpSDK.WithDescription("查看文件内容（仅允许在配置的目录白名单内查看），支持 cursor/offset 分段读取"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("path", mcpSDK.Required(), mcpSDK.Description("目标文件绝对路径")),
		mcpSDK.WithNumber("offset", mcpSDK.Description("分段起始偏移量")),
		mcpSDK.WithNumber("limit", mcpSDK.Description("读取字节上限")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("viewfile"))

	s.AddTool(mcpSDK.NewTool("arthas_options",
		mcpSDK.WithDescription("查看或修改 Arthas 全局开关选项"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("name", mcpSDK.Description("选项名，例如 unsafe, dump")),
		mcpSDK.WithString("value", mcpSDK.Description("设置新选项值")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.makePassthroughHandler("options"))

	// ==========================================
	// 5. 通用透传与运维管理
	// ==========================================
	s.AddTool(mcpSDK.NewTool("arthas_call_tool",
		mcpSDK.WithDescription("自由透传调用 Arthas 原生工具（支持直接传递任意原生 JSON 参数）"),
		mcpSDK.WithString("pod_name", mcpSDK.Required(), mcpSDK.Description("目标 K8s Pod 名称")),
		mcpSDK.WithString("tool_name", mcpSDK.Required(), mcpSDK.Description("工具名，如 jad, thread, ognl")),
		mcpSDK.WithString("arguments_json", mcpSDK.Description("传递给原生工具的 JSON 参数")),
		mcpSDK.WithString("namespace", mcpSDK.Description("Pod 所在命名空间，默认 default")),
	), tr.handleCallTool)

	s.AddTool(mcpSDK.NewTool("arthas_list_sessions",
		mcpSDK.WithDescription("列出当前所有保持 Arthas 连接并已建立端口代理的 Pod 会话列表"),
	), tr.handleListSessions)
}

// makePassthroughHandler 构建通用的工具处理函数，自动过滤网关路由参数并将业务参数透传给目标 Pod 的 Arthas
func (tr *ToolRegistry) makePassthroughHandler(targetToolName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpSDK.CallToolRequest) (*mcpSDK.CallToolResult, error) {
		args := req.Params.Arguments
		toolArgs := make(map[string]interface{})
		for k, v := range args {
			// 过滤掉网关代理层的路由参数（pod_name, namespace, container）
			if k != "pod_name" && k != "namespace" && k != "container" {
				toolArgs[k] = v
			}
		}
		return tr.executeOnPod(ctx, args, targetToolName, toolArgs)
	}
}

func extractTarget(args map[string]interface{}) (namespace, podName, container string) {
	namespace = "default"
	if ns, ok := args["namespace"].(string); ok && ns != "" {
		namespace = ns
	}
	if pod, ok := args["pod_name"].(string); ok {
		podName = pod
	}
	if c, ok := args["container"].(string); ok {
		container = c
	}
	return
}

func (tr *ToolRegistry) executeOnPod(
	ctx context.Context,
	args map[string]interface{},
	toolName string,
	toolArgs map[string]interface{},
) (*mcpSDK.CallToolResult, error) {
	namespace, podName, container := extractTarget(args)
	if podName == "" {
		return mcpSDK.NewToolResultError("缺少必填参数: pod_name"), nil
	}

	sess, err := tr.sessionMgr.GetOrCreateSession(ctx, namespace, podName, container)
	if err != nil {
		return mcpSDK.NewToolResultError(fmt.Sprintf("连接目标 Pod [%s/%s] 失败: %v", namespace, podName, err)), nil
	}

	output, err := tr.arthasClient.CallTool(ctx, sess, toolName, toolArgs)
	if err != nil {
		return mcpSDK.NewToolResultError(fmt.Sprintf("调用 Arthas [%s] 工具失败: %v", toolName, err)), nil
	}

	return mcpSDK.NewToolResultText(output), nil
}

func (tr *ToolRegistry) handleCallTool(ctx context.Context, req mcpSDK.CallToolRequest) (*mcpSDK.CallToolResult, error) {
	toolName, _ := req.Params.Arguments["tool_name"].(string)
	if toolName == "" {
		return mcpSDK.NewToolResultError("缺少必填参数: tool_name"), nil
	}

	toolArgs := make(map[string]interface{})
	if argsJSON, ok := req.Params.Arguments["arguments_json"].(string); ok && argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &toolArgs); err != nil {
			return mcpSDK.NewToolResultError(fmt.Sprintf("解析 arguments_json 失败: %v", err)), nil
		}
	}

	return tr.executeOnPod(ctx, req.Params.Arguments, toolName, toolArgs)
}

func (tr *ToolRegistry) handleListSessions(ctx context.Context, req mcpSDK.CallToolRequest) (*mcpSDK.CallToolResult, error) {
	sessions := tr.sessionMgr.ListSessions()
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return mcpSDK.NewToolResultError(fmt.Sprintf("序列化会话列表失败: %v", err)), nil
	}
	return mcpSDK.NewToolResultText(fmt.Sprintf("当前活跃的 Pod 诊断会话（共 %d 个）:\n%s", len(sessions), string(data))), nil
}

func (tr *ToolRegistry) handleCloseSession(ctx context.Context, req mcpSDK.CallToolRequest) (*mcpSDK.CallToolResult, error) {
	namespace, podName, _ := extractTarget(req.Params.Arguments)
	if podName == "" {
		return mcpSDK.NewToolResultError("缺少必填参数: pod_name"), nil
	}

	log.Printf("[Tool] AI requested close session for %s/%s", namespace, podName)
	if err := tr.sessionMgr.CloseSession(ctx, namespace, podName); err != nil {
		return mcpSDK.NewToolResultError(fmt.Sprintf("关闭会话失败: %v", err)), nil
	}
	return mcpSDK.NewToolResultText(fmt.Sprintf("已成功关闭 Pod [%s/%s] 的 Arthas 诊断会话并释放网络代理。", namespace, podName)), nil
}
