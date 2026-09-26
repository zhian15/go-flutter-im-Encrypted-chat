package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/service"
)

// ============ 文件下载（走后端代理，不再直连 MinIO）============
//
// 背景：MinIO 签名 URL 的 host 来自 MINIO_ENDPOINT（默认 127.0.0.1:9000），
// 手机上根本访问不到，所以预览/下载统一改走后端 /api/v1/files/:fileId/raw 代理，
// raw 端点不挂鉴权中间件，改用 HMAC 签名（fileID + 过期时间）自证。

// fileSignKey 文件链接签名密钥（复用 JWT 密钥，避免额外配置项）。
func fileSignKey(cfg *config.Config) []byte {
	return []byte(cfg.JWTSecret)
}

// signFileRawURL 生成带 HMAC 签名的 raw 代理地址（相对路径，前端自行拼接 apiBase）。
// 签名内容 = "<fileID>.<exp>.<kind>"（kind 参与签名，防止越权把下载链改成预览链或反之）。
// kind 取值："" = 原文件；"preview" = 转换后的预览 PDF（cf.PreviewPDFKey）。
func signFileRawURL(cfg *config.Config, fileID string, kind string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	sign := fileRawSign(cfg, fileID, exp, kind)
	u := fmt.Sprintf("/api/v1/files/%s/raw?exp=%d&sign=%s", url.PathEscape(fileID), exp, sign)
	if kind != "" {
		u += "&kind=" + url.QueryEscape(kind)
	}
	return u
}

// fileRawSign 计算 raw 链接的 HMAC-SHA256 摘要（hex）。
func fileRawSign(cfg *config.Config, fileID string, exp int64, kind string) string {
	msg := fmt.Sprintf("%s.%d.%s", fileID, exp, kind)
	mac := hmac.New(sha256.New, fileSignKey(cfg))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyFileRawSign 校验 raw 代理链接的签名与过期时间。
func verifyFileRawSign(cfg *config.Config, fileID, expStr, sign, kind string) bool {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > exp {
		return false
	}
	// 客户端传来的是 hex 字符串，两侧统一解码成原始摘要字节再比对
	// （长度不等时 hmac.Equal 直接返回 false，天然防伪造）。
	provided, err := hex.DecodeString(sign)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(fileRawSign(cfg, fileID, exp, kind))
	if err != nil {
		return false
	}
	return hmac.Equal(expected, provided)
}

// asciiFallback 把文件名中的非 ASCII 字符替换成 '_'，
// 避免中文文件名破坏 Content-Disposition 头（RFC 7230 要求头字段为 ASCII）。
func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > 127 {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if strings.TrimSpace(out) == "" {
		return "file"
	}
	return out
}

// FileDownloadHandler GET /api/v1/files/:fileId/download （需登录）
// 返回一个带 HMAC 签名的 raw 代理地址，前端直接拿它下载，不再依赖 MinIO 内网地址。
//
// 错误：400 参数错误 / 404 文件不存在 / 403 非会话成员。
func FileDownloadHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		fileID := c.Param("fileId")
		if fileID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误"})
			return
		}
		cf, err := repo.GetConvFileByFileID(c.Request.Context(), fileID)
		if err != nil || cf.Deleted {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在"})
			return
		}
		if !service.IsConvMember(c.Request.Context(), cf.ConvID, uid) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权访问"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "ok",
			"data": gin.H{
				"url":  signFileRawURL(cfg, cf.FileID, "", time.Hour),
				"name": cf.Name,
				"size": cf.Size,
				"mime": cf.Mime,
			},
		})
	}
}

// FileRawHandler GET /api/v1/files/:fileId/raw?exp=&sign=
// 不挂鉴权中间件（外部浏览器/下载器可能不带 token），靠 HMAC 签名自证，
// 从 MinIO 读取原文件并以 attachment 形式回写，供预览直出与下载共用。
//
// 错误：401 链接无效或已过期 / 404 文件不存在 / 500 存储异常。
func FileRawHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileID := c.Param("fileId")
		exp := c.Query("exp")
		sign := c.Query("sign")
		kind := c.Query("kind") // "" = 原文件；"preview" = 转换后的预览 PDF
		if !verifyFileRawSign(cfg, fileID, exp, sign, kind) {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "链接无效或已过期"})
			return
		}

		cf, err := repo.GetConvFileByFileID(c.Request.Context(), fileID)
		if err != nil || cf.Deleted {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在"})
			return
		}

		// 取对象：kind=thumb 取视频首帧封面；kind=preview 取转换后的预览 PDF；否则取原始文件。
		objectKey := cf.MinioKey
		if kind == "thumb" {
			if cf.ThumbKey == "" {
				c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在"})
				return
			}
			objectKey = cf.ThumbKey
		} else if kind == "preview" {
			if cf.PreviewPDFKey == "" {
				c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在"})
				return
			}
			objectKey = cf.PreviewPDFKey
		}

		contentType := cf.Mime
		if kind == "thumb" {
			contentType = "image/jpeg"
		} else if kind == "preview" {
			contentType = "application/pdf"
		} else if isVideoContent(cf) {
			// 视频文件若以 application/octet-stream 之类不可信 MIME 入库，播放器拿不到
			// 正确 Content-Type 会无法解码。兜底成具体 video/*（按扩展名推导），
			// 保证 video_player / <video> 能正确选解码器流式播放（大视频无法预览 bug 修复）。
			contentType = videoMimeByExt(cf.Name, cf.Mime)
		}
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/octet-stream"
		}
		if kind == "thumb" {
			// 封面图用 inline，供 <img>/Image 直接展示
			c.Header("Content-Disposition", "inline; filename=\"thumb.jpg\"")
		} else if kind == "preview" {
			// 预览 PDF 用 inline，避免部分浏览器/下载器强制另存
			c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", asciiFallback(cf.Name)))
		} else {
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s",
				asciiFallback(cf.Name), url.QueryEscape(cf.Name)))
		}

		// 按当前存储驱动（minio / 阿里云 OSS）读对象并回写：
		// OSS 不可 Seek，改由内部把 Range 头转发给 OSS 实现 206 流式；
		// 驱动切换前写入的历史对象自动回落另一驱动读取，不断链。
		if err := service.StorageServeHTTP(c.Writer, c.Request, cfg, cf.MinioBucket, objectKey, contentType, asciiFallback(cf.Name)); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "读取文件失败"})
			return
		}
	}
}
