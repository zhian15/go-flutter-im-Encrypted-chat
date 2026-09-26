package handler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yourcompany/im-server/internal/config"
	"github.com/yourcompany/im-server/internal/middleware"
	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/repo"
	"github.com/yourcompany/im-server/internal/service"
)

// FileThumbHandler GET /api/v1/files/:fileId/thumb
// 返回视频首帧封面图签名直链（raw 代理 kind=thumb）。服务端按需用 ffmpeg 抽帧生成
// （大视频也能有真实首帧，像微信），生成后持久化到 cf.ThumbKey，后续直接返回缓存链接。
// ffmpeg 缺失 / 抽帧失败 / 非视频 → 优雅返回空 thumbUrl（客户端回落深色占位），不报错。
//
// 错误：404 文件不存在 / 403 非会话成员。
func FileThumbHandler(cfg *config.Config) gin.HandlerFunc {
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
		if !isVideoContent(cf) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"thumbUrl": ""}})
			return
		}
		url, err := ensureVideoThumb(c.Request.Context(), cfg, cf)
		if err != nil {
			log.Printf("[video-thumb] gen failed file=%s err=%v", fileID, err)
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"thumbUrl": ""}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"thumbUrl": url}})
	}
}

// ensureVideoThumb 返回视频首帧封面签名直链；首次调用抽帧生成并持久化，之后走缓存。
// 抽帧：ffmpeg 从存储对象流（stdin，MinIO/阿里云 OSS 按当前驱动）在 ~1s 处取首帧
// → 临时 JPEG → 回传存储（thumbs/<fileId>.jpg）。
// 为控制带宽，源流最多读取前 64MB（足以覆盖绝大多数视频前 1s 关键帧）；超出的大视频抽帧失败则降级。
func ensureVideoThumb(ctx context.Context, cfg *config.Config, cf *model.ConvFile) (string, error) {
	if cf.ThumbKey != "" {
		return signFileRawURL(cfg, cf.FileID, "thumb", time.Hour), nil
	}

	// 限制最多读取前 64MB，避免为大视频拉取整文件。
	// 两段式抽帧（2026-09-24 修复「PC 上传的视频无封面」）：
	//   尝试 1：输入侧 seek（-ss 在 -i 前）——faststart mp4/mkv 秒出；
	//   尝试 2：输出侧 seek（-ss 在 -i 后）——抖音/微信下载的 mp4 moov 在文件尾，
	//     管道输入无法 seek，输入侧必失败；顺序解码到 1s 处即可（文件 ≤64MB 时
	//     完整可读，moov 也在读取范围内）。
	tmp, err := os.CreateTemp("", "vthumb-*.jpg")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	bin := os.Getenv("FFMPEG_BIN")
	if bin == "" {
		bin = "ffmpeg"
	}
	ctx2, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	extract := func(seekBeforeInput bool) error {
		obj, err := service.StorageGetObject(ctx, cfg, cf.MinioBucket, cf.MinioKey)
		if err != nil {
			return err
		}
		defer obj.Close()
		args := []string{"-y", "-loglevel", "error"}
		if seekBeforeInput {
			args = append(args, "-ss", "1")
		}
		args = append(args, "-i", "pipe:0")
		if !seekBeforeInput {
			args = append(args, "-ss", "1")
		}
		args = append(args, "-frames:v", "1", "-q:v", "2", "-f", "image2", tmpName)
		cmd := exec.CommandContext(ctx2, bin, args...)
		cmd.Stdin = io.LimitReader(obj, 64*1024*1024)
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			return fmt.Errorf("ffmpeg: %v: %s", runErr, string(out))
		}
		return nil
	}

	if err := extract(true); err != nil {
		log.Printf("[video-thumb] input-seek failed file=%s err=%v，retry output-seek", cf.FileID, err)
		if err := extract(false); err != nil {
			return "", fmt.Errorf("ffmpeg both seeks failed: %v", err)
		}
	}

	f, err := os.Open(tmpName)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return "", fmt.Errorf("empty thumb")
	}

	thumbKey := "thumbs/" + cf.FileID + ".jpg"
	if err := service.StoragePutFile(ctx, cfg, cf.MinioBucket, thumbKey, f, st.Size(), "image/jpeg"); err != nil {
		return "", err
	}
	// 持久化（失败仅告警，下次仍会重试生成，不影响本次返回）。
	if err := repo.UpdateConvFileThumb(ctx, cf.FileID, cf.MinioBucket, thumbKey); err != nil {
		log.Printf("[video-thumb] persist failed file=%s err=%v", cf.FileID, err)
	}
	return signFileRawURL(cfg, cf.FileID, "thumb", time.Hour), nil
}
