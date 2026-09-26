package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/handler"
	"github.com/yourcompany/im-server/internal/pkg/id"
	"github.com/yourcompany/im-server/internal/service"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/gin-gonic/gin"
)

func main() {
	// 标准 log 输出归到 stdout：GIN 访问日志走 stdout、log.Printf 走 stderr，
	// 宝塔等只重定向 stdout 的部署会把 stderr（含 [jpush] 推送日志）静默吞掉，
	// 排障时"日志里什么都没有"实为"stderr 根本没落盘"（2026-09-17 实锤）。
	log.SetOutput(os.Stdout)

	cfg := config.Load()

	// 雪花 ID 初始化（节点 ID 来自 NODE_ID 末尾数字，默认 0）
	nodeID := int64(0)
	if n, err := id.ParseNodeID(cfg.NodeID); err == nil {
		nodeID = n
	}
	id.Init(nodeID)

	// 初始化数据层
	if err := store.InitMySQL(cfg); err != nil {
		log.Fatalf("init mysql failed: %v", err)
	}
	if err := store.InitRedis(cfg); err != nil {
		log.Fatalf("init redis failed: %v", err)
	}
	if err := store.InitMongo(cfg); err != nil {
		log.Fatalf("init mongo failed: %v", err)
	}
	// 幂等迁移（启动时自动执行）
	if err := store.MigrateMySQL(); err != nil {
		log.Fatalf("migrate failed: %v", err)
	}
	// 管理员初始化（环境变量）
	if err := service.EnsureAdmin(cfg); err != nil {
		log.Fatalf("ensure admin failed: %v", err)
	}

	r := gin.Default()
	// 反代场景解析真实客户端 IP（落地「按 IP 拉黑」前置依赖）：
	// 仅 TrustedProxies 内网段才信任 X-Forwarded-For / X-Real-IP，
	// 否则 c.ClientIP() 只会返回代理 IP，拉黑会变成误伤全体用户。
	if len(cfg.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
			log.Fatalf("set trusted proxies failed: %v", err)
		}
	}
	handler.RegisterRoutes(r, cfg)

	// 红包 / 转账的 24 小时到期退回任务（启动时先补跑一次，之后每分钟扫描）
	service.StartMoneyPacketExpiryWorker(context.Background())
	// 会话 seq 水位自愈：启动时全量扫描一次 + 之后每 5 分钟扫描活跃会话（修 R-04）
	service.StartSeqRepairWorker(context.Background())

	port := cfg.HTTPPort
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}
	log.Printf("im api server listening on :%s (node=%s)", port, cfg.NodeID)
	// 自定义 http.Server：r.Run() 是零超时配置，慢请求会无限占用连接与后端
	// 资源，雪崩时没有自愈能力。ReadTimeout 兜底慢客户端，WriteTimeout 兜底
	// 慢查询（超过 30s 客户端早已超时），IdleTimeout 收割空闲 keep-alive 连接。
	srv := &http.Server{
		Addr:        ":" + port,
		Handler:     r,
		ReadTimeout: 15 * time.Second, ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
