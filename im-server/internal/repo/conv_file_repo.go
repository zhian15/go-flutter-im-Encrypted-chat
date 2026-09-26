// Package repo 群文件云盘与白名单的数据访问层（MongoDB / MySQL）。
package repo

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/yourcompany/im-server/internal/model"
	"github.com/yourcompany/im-server/internal/store"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var convFileColl = func() *mongo.Collection {
	return store.Mongo.Collection(model.ConvFile{}.CollectionName())
}

var convFileIdxOnce sync.Once

// ensureConvFileIndexes 幂等创建索引：{conv_id:1,created_at:-1}、{file_id:1}(唯一)。
func ensureConvFileIndexes(ctx context.Context) {
	convFileIdxOnce.Do(func() {
		coll := convFileColl()
		_, _ = coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
			{Keys: bson.D{{Key: "conv_id", Value: 1}, {Key: "created_at", Value: -1}}},
			{Keys: bson.D{{Key: "file_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		})
	})
}

// ConvFileQuery 群文件列表查询条件。
type ConvFileQuery struct {
	ConvID   int64
	Category string // image|document|video|all
	Keyword  string
	Sort     string // time|size（默认 time）
	Order    string // asc|desc（默认 desc）
	Page     int
	PageSize int
}

// ConvFileListResult 列表结果。
type ConvFileListResult struct {
	List  []model.ConvFile
	Total int64
}

// InsertConvFile 写入一条群文件记录（落库 type=3 文件消息时调用）。
// 按 file_id 去重：已存在则跳过，保证幂等（消息重试安全）。
func InsertConvFile(ctx context.Context, cf *model.ConvFile) error {
	ensureConvFileIndexes(ctx)
	if cf.FileID == "" {
		return fmt.Errorf("conv_file: empty file_id")
	}
	count, err := convFileColl().CountDocuments(ctx, bson.M{"file_id": cf.FileID})
	if err == nil && count > 0 {
		return nil // 已存在，幂等跳过
	}
	if cf.CreatedAt.IsZero() {
		cf.CreatedAt = time.Now()
	}
	if cf.PreviewStatus == "" {
		cf.PreviewStatus = model.ConvFilePreviewNone
	}
	if cf.RefCount == 0 {
		cf.RefCount = 1
	}
	_, err = convFileColl().InsertOne(ctx, cf)
	return err
}

// ListConvFiles 按条件分页/筛选/搜索/排序查询群文件。
func ListConvFiles(ctx context.Context, q ConvFileQuery) (ConvFileListResult, error) {
	ensureConvFileIndexes(ctx)
	filter := bson.M{"conv_id": q.ConvID, "deleted": bson.M{"$ne": true}}
	if q.Category != "" && q.Category != "all" {
		filter["category"] = q.Category
	}
	if q.Keyword != "" {
		filter["name"] = bson.M{"$regex": regexp.QuoteMeta(q.Keyword), "$options": "i"}
	}

	total, err := convFileColl().CountDocuments(ctx, filter)
	if err != nil {
		return ConvFileListResult{}, err
	}

	sortField := "created_at"
	if q.Sort == "size" {
		sortField = "size"
	}
	sortDir := -1
	if q.Order == "asc" {
		sortDir = 1
	}
	skip := int64((q.Page - 1) * q.PageSize)
	if skip < 0 {
		skip = 0
	}
	opts := options.Find().
		SetSort(bson.D{{Key: sortField, Value: sortDir}}).
		SetSkip(skip).
		SetLimit(int64(q.PageSize))

	cur, err := convFileColl().Find(ctx, filter, opts)
	if err != nil {
		return ConvFileListResult{}, err
	}
	var list []model.ConvFile
	if err := cur.All(ctx, &list); err != nil {
		return ConvFileListResult{}, err
	}
	if list == nil {
		list = []model.ConvFile{}
	}
	return ConvFileListResult{List: list, Total: total}, nil
}

// GetConvFileByFileID 按 file_id 查询单条记录。
func GetConvFileByFileID(ctx context.Context, fileID string) (*model.ConvFile, error) {
	var cf model.ConvFile
	err := convFileColl().FindOne(ctx, bson.M{"file_id": fileID}).Decode(&cf)
	if err != nil {
		return nil, err
	}
	return &cf, nil
}

// UpdateConvFilePreview 回写预览状态与 PDF key。
func UpdateConvFilePreview(ctx context.Context, fileID, status, pdfKey string) error {
	upd := bson.M{"$set": bson.M{"preview_status": status}}
	if pdfKey != "" {
		upd["$set"].(bson.M)["preview_pdf_key"] = pdfKey
	}
	_, err := convFileColl().UpdateOne(ctx, bson.M{"file_id": fileID}, upd)
	return err
}

// UpdateConvFileThumb 回写视频首帧封面存储位置（ffmpeg 抽帧生成后调用）。
func UpdateConvFileThumb(ctx context.Context, fileID, bucket, key string) error {
	_, err := convFileColl().UpdateOne(ctx, bson.M{"file_id": fileID},
		bson.M{"$set": bson.M{"thumb_bucket": bucket, "thumb_key": key}})
	return err
}

// SoftDeleteConvFile 软删（置 deleted=true）。保留记录以便引用计数/审计。
func SoftDeleteConvFile(ctx context.Context, convID int64, fileID string) error {
	_, err := convFileColl().UpdateOne(ctx,
		bson.M{"conv_id": convID, "file_id": fileID},
		bson.M{"$set": bson.M{"deleted": true}},
	)
	return err
}
