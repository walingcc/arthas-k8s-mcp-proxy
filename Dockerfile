# 使用极简的 alpine 或 distroless 基础镜像
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 默认拷贝 Linux AMD64 二进制（如果在 ARM 节点可改为 arm64）
COPY bin/arthas-mcp-gateway-linux-amd64 /app/arthas-mcp-gateway

EXPOSE 8080

ENTRYPOINT ["/app/arthas-mcp-gateway"]
CMD ["--port", "8080", "--idle-timeout", "5"]
