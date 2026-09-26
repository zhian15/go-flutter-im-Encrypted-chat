package service

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/yourcompany/im-server/internal/config"
)

// ============ 对象存储（MinIO / 阿里云 OSS，驱动见 storage.go）============

// MinioConfig MinIO 生效配置。后台「对象存储」页签写入 sys_config
// （minio_endpoint / minio_public_url / minio_access_key / minio_secret_key / minio_bucket），
// 读取时 sys_config 优先，环境变量（MINIO_*）仅兜底——后台改完即生效，无需重启。
type MinioConfig struct {
	Endpoint  string
	PublicURL string
	AccessKey string
	SecretKey string
	Bucket    string
}

// minioStr 读 sys_config 字符串值；DB 值为空（后台未配/被清空）时回落 env 默认
func minioStr(ctx context.Context, key, def string) string {
	v := strings.TrimSpace(SysConfigString(ctx, key, ""))
	if v == "" {
		return def
	}
	return v
}

// MinioConfigGet 读取生效的 MinIO 配置（DB 优先，env 兜底）
func MinioConfigGet(ctx context.Context, cfg *config.Config) MinioConfig {
	return MinioConfig{
		Endpoint:  minioStr(ctx, "minio_endpoint", cfg.MinIOEndpoint),
		PublicURL: minioStr(ctx, "minio_public_url", cfg.MinIOPublicURL),
		AccessKey: minioStr(ctx, "minio_access_key", cfg.MinIOAccessKey),
		SecretKey: minioStr(ctx, "minio_secret_key", cfg.MinIOSecretKey),
		Bucket:    minioStr(ctx, "minio_bucket", cfg.MinIOBucket),
	}
}

var (
	minioMu        sync.Mutex
	minioClient    *minio.Client
	minioClientSig string // 生成当前 client 时的配置签名；后台配置变更后签名不一致 → 自动重建
)

// EnsureMinioClient 懒初始化 MinIO 客户端；后台配置变更（签名不一致）时自动重建
func EnsureMinioClient(ctx context.Context, cfg *config.Config) (*minio.Client, MinioConfig, error) {
	mc := MinioConfigGet(ctx, cfg)
	sig := fmt.Sprintf("%s|%s|%s|%s", mc.Endpoint, mc.AccessKey, mc.SecretKey, mc.Bucket)
	minioMu.Lock()
	defer minioMu.Unlock()
	if minioClient != nil && minioClientSig == sig {
		return minioClient, mc, nil
	}
	client, err := minio.New(mc.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(mc.AccessKey, mc.SecretKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, mc, err
	}
	// 确保 bucket 存在
	exists, err := client.BucketExists(ctx, mc.Bucket)
	if err == nil && !exists {
		client.MakeBucket(ctx, mc.Bucket, minio.MakeBucketOptions{})
	}
	minioClient = client
	minioClientSig = sig
	return minioClient, mc, nil
}

// UploadFile 上传文件到对象存储（驱动由 storage_driver 决定：minio / 阿里云 oss），返回可访问 URL
// objectPrefix：chat/ 或 avatar/ 等分类
// cfg 仅作为 sys_config 未配置时的 env 兜底
func UploadFile(ctx context.Context, cfg *config.Config, objectPrefix string, file multipart.File, filename string, contentType string) (string, string, int64, error) {
	// 对象名：分类/时间戳_随机.ext（防重名）
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = guessExt(contentType)
	}
	objectName := fmt.Sprintf("%s%d_%d%s", objectPrefix, time.Now().UnixMilli(), time.Now().Nanosecond(), strings.ToLower(ext))

	// 阿里云 OSS 模式：直接 Put（V1 签名，公开读 URL 直出）
	if StorageDriverGet(ctx) == "oss" {
		oc := OssConfigGet(ctx)
		url, sz, err := OssPut(ctx, oc, objectName, file, contentType)
		if err != nil {
			return "", "", 0, err
		}
		return url, objectName, sz, nil
	}

	client, mc, err := EnsureMinioClient(ctx, cfg)
	if err != nil {
		return "", "", 0, err
	}
	info, err := client.PutObject(ctx, mc.Bucket, objectName, file, -1, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", "", 0, err
	}

	// 公开访问 URL（浏览器可达地址，后台 minio_public_url 优先，env MINIO_PUBLIC_URL 兜底）
	base := mc.PublicURL
	if base == "" {
		base = "http://" + mc.Endpoint
	}
	url := fmt.Sprintf("%s/%s/%s", strings.TrimRight(base, "/"), mc.Bucket, objectName)
	return url, objectName, info.Size, nil
}

// MinioGetObject 从 MinIO 读对象流（调用方负责 Close）。StorageGetObject 的 MinIO 分支。
func MinioGetObject(ctx context.Context, cfg *config.Config, bucket, objectName string) (io.ReadCloser, error) {
	client, _, err := EnsureMinioClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	obj, err := client.GetObject(ctx, bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// MinioPutFile 向 MinIO 写入对象（thumb 等派生物）。StoragePutFile 的 MinIO 分支。
func MinioPutFile(ctx context.Context, cfg *config.Config, bucket, objectName string, r io.Reader, size int64, contentType string) error {
	client, _, err := EnsureMinioClient(ctx, cfg)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err = client.PutObject(ctx, bucket, objectName, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func guessExt(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	default:
		return ".bin"
	}
}
