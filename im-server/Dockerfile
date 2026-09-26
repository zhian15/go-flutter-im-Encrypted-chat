# 构建阶段
FROM golang:1.22-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 补充新依赖的 go.sum 条目（需在 COPY 代码后，才能识别 upload.go 的 import）
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api

# 运行阶段
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*
RUN adduser --disabled-password --gecos '' appuser
WORKDIR /app
COPY --from=builder /out/api /usr/local/bin/api
COPY --from=builder /app/migrations /app/migrations
USER appuser
ENV TZ=UTC
EXPOSE 8080
ENTRYPOINT ["api"]
