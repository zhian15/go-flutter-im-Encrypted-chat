package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/yourcompany/im-server/internal/config"
)

// storageEnvDefault 环境变量兜底（sys_config 未配置时生效）
func storageEnvDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// StorageDriver 当前生效的存储驱动：
//   - sys_config.storage_driver 优先（后台「对象存储」页签可切，改完即生效无需重启）
//   - 环境变量 STORAGE_DRIVER 兜底
//   - 默认 minio（历史行为）
//
// 取值："minio" | "oss"
func StorageDriverGet(ctx context.Context) string {
	d := strings.ToLower(minioStr(ctx, "storage_driver", storageEnvDefault("STORAGE_DRIVER", "minio")))
	if d == "oss" || d == "aliyun-oss" || d == "aliyun_oss" {
		return "oss"
	}
	return "minio"
}

// ============ 阿里云 OSS（纯标准库实现，V1 签名）============

// OssConfig 阿里云 OSS 生效配置。后台「对象存储」页签写入 sys_config
// （oss_endpoint / oss_access_key_id / oss_access_key_secret / oss_bucket / oss_public_url），
// 读取时 sys_config 优先，环境变量（ALIYUN_OSS_*）仅兜底。
type OssConfig struct {
	Endpoint  string // 如 oss-cn-hangzhou.aliyuncs.com（不带 https://）
	AccessID  string
	AccessKey string
	Bucket    string
	PublicURL string // 可选；空则默认 https://{bucket}.{endpoint}
}

// OssConfigGet 读取生效的 OSS 配置（DB 优先，env 兜底）
func OssConfigGet(ctx context.Context) OssConfig {
	return OssConfig{
		Endpoint:  minioStr(ctx, "oss_endpoint", storageEnvDefault("ALIYUN_OSS_ENDPOINT", "")),
		AccessID:  minioStr(ctx, "oss_access_key_id", storageEnvDefault("ALIYUN_OSS_ACCESS_KEY_ID", "")),
		AccessKey: minioStr(ctx, "oss_access_key_secret", storageEnvDefault("ALIYUN_OSS_ACCESS_KEY_SECRET", "")),
		Bucket:    minioStr(ctx, "oss_bucket", storageEnvDefault("ALIYUN_OSS_BUCKET", "")),
		PublicURL: minioStr(ctx, "oss_public_url", storageEnvDefault("ALIYUN_OSS_PUBLIC_URL", "")),
	}
}

// ossPublicBase 对象公开访问基地址：显式配置的 PublicURL 优先（可挂 CDN/自定义域名），
// 否则按虚拟主机样式 https://{bucket}.{endpoint}。
func (c OssConfig) ossPublicBase() string {
	if c.PublicURL != "" {
		return strings.TrimRight(c.PublicURL, "/")
	}
	scheme := "https"
	if strings.HasPrefix(c.Endpoint, "localhost") || strings.HasPrefix(c.Endpoint, "127.0.0.1") {
		scheme = "http"
	}
	return scheme + "://" + c.Bucket + "." + c.Endpoint
}

// OssPublicURL 拼对象公开访问 URL
func OssPublicURL(c OssConfig, objectName string) string {
	return c.ossPublicBase() + "/" + objectName
}

// ossSign 计算 OSS V1 签名（官方 Authorization: OSS AccessKeyId:Signature 格式）。
// 待签串 = VERB\nContent-MD5\nContent-Type\nDate\nCanonicalizedOSSHeaders + CanonicalizedResource
// 简单场景无 x-oss-* 头，CanonicalizedOSSHeaders 为空；子资源仅需 Expires（presign）或为空。
func ossSign(accessKey, verb, contentMD5, contentType, date, canonicalizedResource string) string {
	raw := verb + "\n" + contentMD5 + "\n" + contentType + "\n" + date + "\n" + canonicalizedResource
	mac := hmac.New(sha1.New, []byte(accessKey))
	mac.Write([]byte(raw))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ossAuthHeader 生成签名请求头
func ossAuthHeader(oc OssConfig, verb, contentType string, date time.Time, canonicalizedResource string) string {
	sign := ossSign(oc.AccessKey, verb, "", contentType, date.UTC().Format(http.TimeFormat), canonicalizedResource)
	return "OSS " + oc.AccessID + ":" + sign
}

// OssPut 上传对象到阿里云 OSS（Put Object，读整个入参到内存——IM 场景单文件受
// nginx client_max_body_size 约束，体量可控）。返回公开访问 URL 与字节数。
func OssPut(ctx context.Context, oc OssConfig, objectName string, r io.Reader, contentType string) (string, int64, error) {
	if oc.Endpoint == "" || oc.Bucket == "" || oc.AccessID == "" || oc.AccessKey == "" {
		return "", 0, fmt.Errorf("阿里云 OSS 未配置完整（endpoint/bucket/accessKey）")
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	url := "https://" + oc.Bucket + "." + oc.Endpoint + "/" + objectName
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("Authorization", ossAuthHeader(oc, http.MethodPut, contentType,
		time.Now().UTC(), "/"+oc.Bucket+"/"+objectName))
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", 0, fmt.Errorf("OSS 上传失败: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return OssPublicURL(oc, objectName), int64(len(body)), nil
}

// OssGet 拉取 OSS 对象流（带 V1 签名，私有 Bucket 也可读）。调用方负责 Close。
func OssGet(ctx context.Context, oc OssConfig, bucket, objectName string) (io.ReadCloser, error) {
	if oc.Endpoint == "" || bucket == "" || oc.AccessID == "" || oc.AccessKey == "" {
		return nil, fmt.Errorf("阿里云 OSS 未配置完整（endpoint/bucket/accessKey）")
	}
	url := "https://" + bucket + "." + oc.Endpoint + "/" + objectName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	req.Header.Set("Date", now.Format(http.TimeFormat))
	req.Header.Set("Authorization", ossAuthHeader(oc, http.MethodGet, "", now, "/"+bucket+"/"+objectName))
	resp, err := (&http.Client{Timeout: 0}).Do(req) // 读流不限时，交给 ctx 控制
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("OSS 读取失败: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return resp.Body, nil
}

// StorageGetObject 按当前驱动读对象流。先按当前驱动读，失败（对象可能在切换前的
// 另一个存储里）时回落另一驱动读一次，保证切换存储后历史文件仍可下载/预览。
func StorageGetObject(ctx context.Context, cfg *config.Config, bucket, objectName string) (io.ReadCloser, error) {
	driver := StorageDriverGet(ctx)
	if driver == "oss" {
		if rc, err := OssGet(ctx, OssConfigGet(ctx), bucket, objectName); err == nil {
			return rc, nil
		} else if rc2, err2 := MinioGetObject(ctx, cfg, bucket, objectName); err2 == nil {
			return rc2, nil
		} else {
			return nil, err // 两个都失败时以当前驱动的错误为准
		}
	}
	if rc, err := MinioGetObject(ctx, cfg, bucket, objectName); err == nil {
		return rc, nil
	} else if rc2, err2 := OssGet(ctx, OssConfigGet(ctx), bucket, objectName); err2 == nil {
		return rc2, nil
	} else {
		return nil, err
	}
}

// StoragePutFile 按当前驱动写入对象（thumb 等派生物）。
func StoragePutFile(ctx context.Context, cfg *config.Config, bucket, objectName string, r io.Reader, size int64, contentType string) error {
	if StorageDriverGet(ctx) == "oss" {
		_, _, err := OssPut(ctx, OssConfigGet(ctx), objectName, r, contentType)
		return err
	}
	return MinioPutFile(ctx, cfg, bucket, objectName, r, size, contentType)
}

// OssGetRange 带客户端 Range 头的签名 GET（OSS 原生支持 Range → 206 流式）。
// 返回响应体流、状态码与 Content-Range。调用方负责 Close。
func OssGetRange(ctx context.Context, oc OssConfig, bucket, objectName, rangeHeader string) (io.ReadCloser, int, string, error) {
	if oc.Endpoint == "" || bucket == "" || oc.AccessID == "" || oc.AccessKey == "" {
		return nil, 0, "", fmt.Errorf("阿里云 OSS 未配置完整（endpoint/bucket/accessKey）")
	}
	url := "https://" + bucket + "." + oc.Endpoint + "/" + objectName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, "", err
	}
	now := time.Now().UTC()
	req.Header.Set("Date", now.Format(http.TimeFormat))
	req.Header.Set("Authorization", ossAuthHeader(oc, http.MethodGet, "", now, "/"+bucket+"/"+objectName))
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, resp.StatusCode, "", fmt.Errorf("OSS 读取失败: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return resp.Body, resp.StatusCode, resp.Header.Get("Content-Range"), nil
}

// StorageServeHTTP 把存储对象回写给 HTTP 客户端（raw 下载/预览代理共用）：
//   - MinIO 分支：对象是 io.ReadSeeker，直接 http.ServeContent（原生 Range/206，
//     视频拖进度条、断点续传行为不变）
//   - OSS 分支：OSS 响应流不可 Seek，改为把客户端 Range 头转发给 OSS，
//     由 OSS 返回 206/200 后流式回写，同样支持拖进度条与断点续传
//
// 对象定位：先按当前驱动找；不存在（驱动切换前写入的历史对象）自动回落另一驱动，
// 保证切换存储后老文件不断链。两个驱动都没有 → 返回错误（调用方负责 404/500 响应）。
// 成功时已写完整响应，调用方不要再写。
func StorageServeHTTP(w http.ResponseWriter, r *http.Request, cfg *config.Config, bucket, objectName, contentType, downloadName string) error {
	ctx := r.Context()
	primary := StorageDriverGet(ctx)
	drivers := []string{primary, "minio"}
	if primary == "minio" {
		drivers[1] = "oss"
	}
	var lastErr error
	for i, d := range drivers {
		if i > 0 && !storageObjectExists(ctx, cfg, d, bucket, objectName) {
			continue
		}
		if d == "oss" {
			rc, status, contentRange, err := OssGetRange(ctx, OssConfigGet(ctx), bucket, objectName, r.Header.Get("Range"))
			if err != nil {
				lastErr = err
				continue
			}
			defer rc.Close()
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			if status == http.StatusPartialContent {
				w.Header().Set("Content-Range", contentRange)
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			_, err = io.Copy(w, rc)
			return err
		}
		// MinIO 分支：保持原有 ServeContent 行为（206/Content-Range/416 全套）
		rc, err := MinioGetObject(ctx, cfg, bucket, objectName)
		if err != nil {
			lastErr = err
			continue
		}
		defer rc.Close()
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		// minio Object 实现了 io.ReadSeeker → ServeContent 处理 Range（206/416）；
		// 若拿到非 Seeker 的流则降级整流回写。
		if rs, ok := rc.(io.ReadSeeker); ok {
			http.ServeContent(w, r, downloadName, time.Time{}, rs)
			return nil
		}
		w.WriteHeader(http.StatusOK)
		_, err = io.Copy(w, rc)
		return err
	}
	return lastErr
}

// storageObjectExists 探测对象在指定驱动上是否存在（不读完整内容）。
func storageObjectExists(ctx context.Context, cfg *config.Config, driver, bucket, objectName string) bool {
	if driver == "oss" {
		rc, _, _, err := OssGetRange(ctx, OssConfigGet(ctx), bucket, objectName, "bytes=0-0")
		if err != nil {
			return false
		}
		rc.Close()
		return true
	}
	client, _, err := EnsureMinioClient(ctx, cfg)
	if err != nil {
		return false
	}
	_, err = client.StatObject(ctx, bucket, objectName, minio.StatObjectOptions{})
	return err == nil
}
