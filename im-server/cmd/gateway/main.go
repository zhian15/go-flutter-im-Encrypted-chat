package main

import (
	"context"
	"log"
	"os"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/service"
	"github.com/yourcompany/im-server/internal/store"

	"github.com/gin-gonic/gin"
)

func main() {
	// 与 api 同因：log.Printf 走 stderr，GIN 走 stdout；宝塔等部署只落 stdout
	// 时 stderr（含 [gateway] 日志）会静默丢失，统一归到 stdout 保证日志可见。
	log.SetOutput(os.Stdout)

	cfg := config.Load()

	if err := store.InitRedis(cfg); err != nil {
		log.Fatalf("init redis failed: %v", err)
	}
	if err := store.InitMongo(cfg); err != nil {
		log.Fatalf("init mongo failed: %v", err)
	}
	// MySQL：/ws 握手要复核「账号状态 + 令牌版本」（见 service.handleWS），数据源就是 store.DB。
	// 用 NoMigrate 版本 —— 网关不跑 AutoMigrate：那是 DDL，多副本同时启动会抢元数据锁，
	// 且网关的职责是转发实时消息，不该因一次建表动作而起不来。
	// 失败**不** Fatal：store.DB 会保持 nil，handleWS 短路跳过复核（退回改动前的行为），
	// 实时转发照常工作。不能因为 MySQL 不可用就把所有 WS 握手挡死 —— 那正是本次要修的故障面。
	// 授权判断并未失守：REST 侧每个 API 请求都由 middleware.Auth 做同样的复核。
	if err := store.InitMySQLNoMigrate(cfg); err != nil {
		log.Printf("[gateway] init mysql failed, WS 令牌复核将跳过（实时转发不受影响）: %v", err)
	}

	// 慢客户端踢连接开关（默认关闭，见 config.KickSlowClients 注释）：
	// 只有在客户端补拉机制验证通过后才应打开，否则弱网用户只会更频繁断线却拿不到补偿。
	service.SetSlowClientKick(cfg.KickSlowClients)

	// 启动事件消费：api 发布的消息经 Redis 广播，本节点推送给在线客户端
	service.StartEventConsumer(context.Background())

	r := gin.Default()
	service.RegisterWSRoutes(r, cfg)

	log.Printf("im gateway listening on :%s", cfg.WSPort)
	if err := r.Run(":" + cfg.WSPort); err != nil {
		log.Fatal(err)
	}
}
