package model

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// 群文件预览状态机
const (
	ConvFilePreviewNone      = "none"       // 未转换
	ConvFilePreviewPDFReady  = "pdf_ready"  // 已生成预览 PDF
	ConvFilePreviewPDFFailed = "pdf_failed" // 转换失败（含 >50MB 直接失败）
)

// 文件分类
const (
	ConvFileCategoryImage    = "image"
	ConvFileCategoryDocument = "document"
	ConvFileCategoryVideo    = "video"
	ConvFileCategoryOther    = "other"
)

// ConvFile 群文件云盘文档（MongoDB collection: conv_file）。
// 字段严格对应架构：conv_id, msg_id, file_id, name, size, mime,
// category[image|document|video|other], uploader_id, minio_bucket, minio_key,
// preview_status[none|pdf_ready|pdf_failed], preview_pdf_key, ref_count, created_at, deleted。
// 说明：当前无商户维度，故不保存 merchant_id。
type ConvFile struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	ConvID        int64              `bson:"conv_id" json:"convId,string"`
	MsgID         int64              `bson:"msg_id" json:"msgId,string"`
	FileID        string             `bson:"file_id" json:"fileId"` // 全局唯一文件 ID
	Name          string             `bson:"name" json:"name"`
	Size          int64              `bson:"size" json:"size"`
	Mime          string             `bson:"mime" json:"mime"`
	Category      string             `bson:"category" json:"category"` // image|document|video|other
	UploaderID    int64              `bson:"uploader_id" json:"uploaderId,string"`
	MinioBucket   string             `bson:"minio_bucket" json:"minioBucket"`
	MinioKey      string             `bson:"minio_key" json:"minioKey"`
	PreviewStatus string             `bson:"preview_status" json:"previewStatus"`
	PreviewPDFKey string             `bson:"preview_pdf_key" json:"previewPdfKey"`
	ThumbBucket   string             `bson:"thumb_bucket" json:"thumbBucket"` // 视频首帧封面（ffmpeg 抽帧生成，存 MinIO）
	ThumbKey      string             `bson:"thumb_key" json:"thumbKey"`       // 封面对象 key，空=尚未生成
	RefCount      int                `bson:"ref_count" json:"refCount"`
	CreatedAt     time.Time          `bson:"created_at" json:"createdAt"`
	Deleted       bool               `bson:"deleted" json:"deleted"`
}

// CollectionName MongoDB 集合名
func (ConvFile) CollectionName() string { return "conv_file" }

// FileCategory 根据 MIME 计算分类。
func FileCategory(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return ConvFileCategoryImage
	case strings.HasPrefix(mime, "video/"):
		return ConvFileCategoryVideo
	case strings.HasPrefix(mime, "application/pdf"),
		strings.HasPrefix(mime, "application/msword"),
		strings.HasPrefix(mime, "application/vnd.ms-"),
		strings.HasPrefix(mime, "application/vnd.openxmlformats-officedocument"),
		strings.HasPrefix(mime, "text/"),
		strings.Contains(mime, "officedocument"),
		strings.Contains(mime, "document"):
		return ConvFileCategoryDocument
	default:
		return ConvFileCategoryOther
	}
}
