package store

import (
	"context"
	"log"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/model"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var (
	DB    *gorm.DB
	RDB   *redis.Client
	Mongo *mongo.Database
)

// InitMySQL 连接 MySQL 并执行幂等建表/加列（AutoMigrate）。
// 只应由 api 进程调用：AutoMigrate 是 DDL，多进程同时跑会互相抢元数据锁。
func InitMySQL(cfg *config.Config) error {
	if err := InitMySQLNoMigrate(cfg); err != nil {
		return err
	}
	// 幂等建表/加列：钱包流水、朋友圈、群聊；user 表补 balance 列
	if err := DB.AutoMigrate(
		&model.WalletTransaction{},
		&model.MomentsPost{},
		&model.MomentsComment{},
		&model.RedPacketClaim{},
		&model.TransferClaim{},
		&model.MoneyPacket{},
		&model.User{},
		&model.InviteFriendCode{},
		&model.Conversation{},
		&model.ConversationMember{},
	); err != nil {
		return err
	}
	log.Println("mysql connected")
	return nil
}

// InitMySQLNoMigrate 只连接 MySQL 并配置连接池，**不执行任何 DDL**。
// 给 gateway 进程用：/ws 握手要读 user.status / user.token_version 做复核（见 service.handleWS），
// 但网关不该跑 AutoMigrate，原因两条：
//  1. AutoMigrate 是 DDL，多副本网关同时启动会互相抢元数据锁，且它失败即 fatal；
//  2. 网关的职责是转发实时消息，不该因为一次建表动作而启动不了。
//
// 注意：只在全部配置成功后才给包级 DB 赋值，失败时 DB 保持 nil，
// handleWS 因此会短路跳过复核（退回改动前的行为）而不是 nil panic。
func InitMySQLNoMigrate(cfg *config.Config) error {
	db, err := gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{})
	if err != nil {
		return err
	}
	if sqlDB, e := db.DB(); e == nil && sqlDB != nil {
		sqlDB.SetMaxOpenConns(50)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxLifetime(time.Hour)
		// 空闲连接保活上限：云数据库/防火墙常会掐断空闲 TCP 连接，
		// 不设这个值时池里的死连接最长活 1 小时，拿到就报 invalid connection。
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	} else {
		// 拿不到 *sql.DB 就设不了池参数。这里**不**返回错误：gorm 自身仍可用，
		// 为此让进程起不来不值当（api 侧会因此 Fatal），但必须留下痕迹。
		log.Printf("[store] obtain *sql.DB failed, connection pool params not applied: %v", e)
	}
	DB = db
	log.Println("mysql connected (no migrate)")
	return nil
}

// InitRedis 建立 Redis 连接。
//
// 【本次改动 R-23】旧代码只给了 Addr/Password/DB，池大小、拨号/读/写超时、重试次数
// 全是 go-redis 默认值（PoolSize=10×CPU、Dial 5s、Read 3s、重试 3 次）。
// 高负载下 Redis 一抖动，3 次重试会把单次发送 RT 放大 3 倍，进而拖垮整条发送链路。
//
// 取值原则：
//   - 推送类调用失败宁可丢（客户端补拉兜底），也不要重试放大 → MaxRetries=1
//   - 读写超时压到 1~3s，超时的请求快速失败，把线程还给业务
//   - PoolSize 显式给足（大群扇出时 gateway 与 api 都会密集用 Redis）
//
// 注意：ReadTimeout **保持 3s 不缩短** —— 这条连接同时承载 Pub/Sub 订阅，
// go-redis 的 PubSub 复用 read deadline，压到 1s 会导致空闲期频繁重建订阅。
// 如需进一步压低，请先在压测环境验证 pubsub 订阅稳定性。
func InitRedis(cfg *config.Config) error {
	poolSize := cfg.RedisPoolSize
	if poolSize <= 0 {
		poolSize = 50
	}
	RDB = redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPass,
		DB:           cfg.RedisDB,
		PoolSize:     poolSize,
		MinIdleConns: 10,
		MaxIdleConns: poolSize,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolTimeout:  2 * time.Second,
		MaxRetries:   1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := RDB.Ping(ctx).Err(); err != nil {
		return err
	}
	log.Printf("redis connected (pool=%d)", poolSize)
	return nil
}

func InitMongo(cfg *config.Config) error {
	opts := options.Client().ApplyURI(cfg.MongoURI)
	if cfg.MongoUser != "" {
		opts.SetAuth(options.Credential{Username: cfg.MongoUser, Password: cfg.MongoPassword})
	}
	client, err := mongo.Connect(context.Background(), opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		return err
	}
	Mongo = client.Database(cfg.MongoDB)
	// 消息集合索引：幂等去重 + 补拉 + 历史分页
	msgIdx := Mongo.Collection("message").Indexes()
	_, _ = msgIdx.CreateMany(ctx, []mongo.IndexModel{
		{Keys: bsonxDoc("sender_id", 1, "client_msg_id", 1), Options: options.Index().SetUnique(true)},
		{Keys: bsonxDoc("conversation_id", 1, "seq", 1)},
		{Keys: bsonxDoc("conversation_id", 1, "msg_id", -1)},
	})
	_, _ = Mongo.Collection("message_receipt").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bsonxDoc("conversation_id", 1, "msg_id", 1),
	})
	log.Println("mongo connected, indexes ensured")
	return nil
}

// bsonxDoc 简化 bson.D 构造
func bsonxDoc(pairs ...interface{}) bson.D {
	var d bson.D
	for i := 0; i+1 < len(pairs); i += 2 {
		d = append(d, bson.E{Key: pairs[i].(string), Value: pairs[i+1]})
	}
	return d
}
