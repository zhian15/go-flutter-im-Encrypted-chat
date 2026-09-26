package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/pkg/errs"
)

// ConvertFile 调用 im-convert 微服务，把 MinIO 上的文档对象转为预览 PDF。
// 返回：
//   - (pdfKey, false, nil)：转换完成，pdfKey 为 MinIO 上的预览对象名（preview/ 前缀）
//   - ("", true, nil)：转换中（im-convert 仍在队列/处理），调用方应返回 processing
//   - ("", false, errs.ConvertTooLarge)：文件 >50MB，im-convert 拒绝
//   - ("", false, errs.ConvertFailed)：转换失败
//
// 带超时（65s，覆盖 im-convert 内部 60s 整体超时 + 网络余量）与 1 次重试。
func ConvertFile(ctx context.Context, minioKey string) (pdfKey string, processing bool, err error) {
	base := os.Getenv("IM_CONVERT_URL")
	if base == "" {
		base = "http://127.0.0.1:8088"
	}
	reqURL := strings.TrimRight(base, "/") + "/convert"

	payload, _ := json.Marshal(map[string]string{"minio_key": minioKey})

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		pdfKey, processing, lastErr = doConvert(ctx, reqURL, payload)
		if lastErr == nil {
			return pdfKey, processing, nil
		}
		// 业务错误（过大/失败/冲突）不重试，直接返回
		if lastErr == errs.ConvertTooLarge || lastErr == errs.ConvertFailed {
			return "", false, lastErr
		}
		// 网络/超时类错误重试一次
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", false, lastErr
}

func doConvert(ctx context.Context, reqURL string, payload []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 65 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		var r struct {
			PdfKey string `json:"pdf_key"`
		}
		if json.Unmarshal(body, &r) != nil {
			return "", false, fmt.Errorf("convert: 响应解析失败: %s", string(body))
		}
		return r.PdfKey, false, nil
	case http.StatusAccepted:
		return "", true, nil // 转换中
	case http.StatusUnprocessableEntity:
		return "", false, errs.ConvertTooLarge
	case http.StatusConflict:
		return "", false, errs.ConvertFailed
	default:
		return "", false, fmt.Errorf("convert: 服务返回状态 %d: %s", resp.StatusCode, string(body))
	}
}

// PresignMinio 生成 MinIO 对象的可读签名 URL（默认 1h 过期）。
func PresignMinio(ctx context.Context, cfg *config.Config, bucket, key string, ttl time.Duration) (string, error) {
	client, _, err := EnsureMinioClient(ctx, cfg)
	if err != nil {
		return "", err
	}
	u, err := client.PresignedGetObject(ctx, bucket, key, ttl, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
