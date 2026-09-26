package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/pkg/errs"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/service"
)

// FilePreviewHandler GET /api/v1/files/:fileId/preview （功能 A-4/A-5）
// 返回 data.kind 说明预览形态：
//   - text：纯文本（txt/log/md/csv/json/代码等），直出后端代理 raw 链接，无需转换；
//   - image：图片，直出后端代理 raw 链接；
//   - pdf：原生 PDF 或 Office 转换后的预览 PDF（缺省按二进制/PDF 处理，走 pdfx/浏览器渲染）。
//
// document 走转换状态机：
//   - pdf_ready：返回预览 PDF 签名 URL（预览 PDF 不是原文件，仍走 MinIO presign）
//   - pdf_failed：409 ConvertFailed
//   - none：调 convert_client 同步转换（或返回 processing），>50MB 直接 422 ConvertTooLarge
//
// 注意：预览/下载一律返回后端代理地址（/api/v1/files/:fileId/raw，带 HMAC 签名），
// 不再直出 MinIO 签名 URL——MinIO 的 host 来自 MINIO_ENDPOINT（默认 127.0.0.1:9000），
// 移动端无法访问。
//
// 错误：404 文件不存在 / 403 非成员 / 409 转换失败 / 422 转换过大 / 500 生成链接失败。
func FilePreviewHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := middleware.CurrentUserID(c)
		fileID := c.Param("fileId")
		if fileID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误"})
			return
		}
		cf, err := repo.GetConvFileByFileID(c.Request.Context(), fileID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "文件不存在"})
			return
		}
		if !service.IsConvMember(c.Request.Context(), cf.ConvID, uid) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权访问"})
			return
		}

		// 纯文本（txt/log/md/csv/json/各类源码）：直接返回原始文件代理地址，
		// 不需要也不应该走 im-convert（转换服务不可用时会导致 txt 也「不支持预览」）。
		if isPlainText(cf) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"url":         signFileRawURL(cfg, cf.FileID, "", time.Hour),
				"status":      "ready",
				"kind":        "text",
				"contentType": cf.Mime,
				"name":        cf.Name,
				"size":        cf.Size,
			}})
			return
		}

		// image / 原生 pdf 直接签名返回
		if cf.Category == model.ConvFileCategoryImage || cf.Mime == "application/pdf" {
			kind := "pdf"
			if cf.Category == model.ConvFileCategoryImage {
				kind = "image"
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"url":         signFileRawURL(cfg, cf.FileID, "", time.Hour),
				"status":      "ready",
				"kind":        kind,
				"contentType": cf.Mime,
				"name":        cf.Name,
				"size":        cf.Size,
			}})
			return
		}

		// 视频（mp4/mov/avi/mkv/webm…）：不走文档转换状态机（视频转 PDF 必然失败/超限），
		// 直接签名返回原文件流（kind=video），客户端用播放器在线播放（App video_player / PC <video>）。
		// raw 代理已支持 Range 分段（见 FileRawHandler），进度条拖动/断点续传都可用。
		//
		// 判定放宽：不仅看 cf.Category（发送端按 MIME 标 video 才会是 video），
		// 还兼容 MIME 以 video/ 开头、或文件名是视频扩展名。否则大视频 / 用「发送文件」入口上传的
		// 视频 MIME 常为 application/octet-stream、category=other，会掉进下方文档转换状态机，
		// >50MB 直接 422 ConvertTooLarge → 「暂不支持预览」（大视频无法预览 bug，2026-09-24 修复）。
		if isVideoContent(cf) {
			// 视频首帧封面：按需用 ffmpeg 抽帧生成（大视频也有真实首帧，像微信）。
			// 抽帧失败不影响播放，thumbUrl 留空即可。
			thumbUrl, _ := ensureVideoThumb(c.Request.Context(), cfg, cf)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"url":         signFileRawURL(cfg, cf.FileID, "", time.Hour),
				"status":      "ready",
				"kind":        "video",
				"thumbUrl":    thumbUrl,
				"contentType": videoMimeByExt(cf.Name, cf.Mime),
				"name":        cf.Name,
				"size":        cf.Size,
			}})
			return
		}

		// document 预览转换状态机
		switch cf.PreviewStatus {
		case model.ConvFilePreviewPDFReady:
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"url":         signFileRawURL(cfg, cf.FileID, "preview", time.Hour),
				"status":      "ready",
				"kind":        "pdf",
				"contentType": "application/pdf",
				"name":        cf.Name,
				"size":        cf.Size,
			}})
			return
		case model.ConvFilePreviewPDFFailed:
			c.JSON(http.StatusOK, gin.H{"code": 409, "message": errs.ConvertFailed.Msg})
			return
		default: // none
			pdfKey, processing, convErr := service.ConvertFile(c.Request.Context(), cf.MinioKey)
			if convErr != nil {
				switch {
				case errors.Is(convErr, errs.ConvertTooLarge):
					_ = repo.UpdateConvFilePreview(c.Request.Context(), cf.FileID, model.ConvFilePreviewPDFFailed, "")
					c.JSON(http.StatusOK, gin.H{"code": 422, "message": convErr.Error()})
					return
				case errors.Is(convErr, errs.ConvertFailed):
					_ = repo.UpdateConvFilePreview(c.Request.Context(), cf.FileID, model.ConvFilePreviewPDFFailed, "")
					c.JSON(http.StatusOK, gin.H{"code": 409, "message": convErr.Error()})
					return
				default:
					c.JSON(http.StatusOK, gin.H{"code": 500, "message": "转换服务异常"})
					return
				}
			}
			if processing {
				c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"status": "processing"}})
				return
			}
			_ = repo.UpdateConvFilePreview(c.Request.Context(), cf.FileID, model.ConvFilePreviewPDFReady, pdfKey)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
				"url":         signFileRawURL(cfg, cf.FileID, "preview", time.Hour),
				"status":      "ready",
				"kind":        "pdf",
				"contentType": "application/pdf",
				"name":        cf.Name,
				"size":        cf.Size,
			}})
		}
	}
}

// isPlainText 判断文件是否为可直接展示的纯文本。
// 命中则跳过 im-convert，直接把原文件代理给客户端渲染。
func isPlainText(cf *model.ConvFile) bool {
	if cf == nil {
		return false
	}

	// MIME 主类型（去掉 ;charset=... 之类参数）
	mime := strings.ToLower(strings.TrimSpace(cf.Mime))
	mime = strings.TrimSpace(strings.SplitN(mime, ";", 2)[0])
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/x-yaml", "application/xml", "application/javascript":
		return true
	}

	// 兜底：按文件名后缀判断（部分端上传时 MIME 可能是 application/octet-stream）
	name := strings.ToLower(strings.TrimSpace(cf.Name))
	for _, ext := range []string{
		".txt", ".log", ".md", ".markdown", ".csv", ".json", ".xml", ".yml", ".yaml",
		".ini", ".conf", ".properties", ".sh", ".sql", ".js", ".ts", ".dart", ".py",
		".java", ".kt", ".go", ".c", ".h", ".cpp", ".css", ".html", ".htm",
	} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// 视频扩展名（与 videoMimeByExt 保持同步）。用于 category/mime 不可信时仍可识别视频。
var _videoExts = []string{
	".mp4", ".mov", ".avi", ".mkv", ".webm", ".flv", ".wmv", ".m4v",
	".3gp", ".mpeg", ".mpg", ".ts", ".mts", ".m2ts",
}

// hasVideoExt 文件名是否为已知视频扩展名。
func hasVideoExt(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, ext := range _videoExts {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
}

// isVideoContent 判定文件是否为视频（可在线流式播放）。
// 三重兜底：① cf.Category==video（发送端按 MIME 正确标了 video）；
// ② MIME 以 video/ 开头；③ 文件名是视频扩展名（兼容 octet-stream 之类不可信 MIME）。
// 见 FilePreviewHandler 视频分支注释（大视频无法预览 bug 修复，2026-09-24）。
func isVideoContent(cf *model.ConvFile) bool {
	if cf == nil {
		return false
	}
	if cf.Category == model.ConvFileCategoryVideo {
		return true
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cf.Mime)), "video/") {
		return true
	}
	return hasVideoExt(cf.Name)
}

// videoMimeByExt 按扩展名推导视频 MIME（用于预览响应与 raw 代理的 Content-Type）。
// 视频文件若以 application/octet-stream 之类不可信 MIME 入库，播放器拿不到正确
// Content-Type 会无法解码；这里兜底成具体 video/*，播放器才能正确选解码器。
func videoMimeByExt(name, fallback string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasSuffix(n, ".mp4"), strings.HasSuffix(n, ".m4v"):
		return "video/mp4"
	case strings.HasSuffix(n, ".mov"):
		return "video/quicktime"
	case strings.HasSuffix(n, ".avi"):
		return "video/x-msvideo"
	case strings.HasSuffix(n, ".mkv"):
		return "video/x-matroska"
	case strings.HasSuffix(n, ".webm"):
		return "video/webm"
	case strings.HasSuffix(n, ".flv"):
		return "video/x-flv"
	case strings.HasSuffix(n, ".wmv"):
		return "video/x-ms-wmv"
	case strings.HasSuffix(n, ".3gp"):
		return "video/3gpp"
	case strings.HasSuffix(n, ".mpeg"), strings.HasSuffix(n, ".mpg"), strings.HasSuffix(n, ".ts"),
		strings.HasSuffix(n, ".mts"), strings.HasSuffix(n, ".m2ts"):
		return "video/mpeg"
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(fallback)), "video/") {
			return fallback
		}
		return "video/mp4"
	}
}
