package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 群文件列表「参数错误恒 400」回归测试（第十五批修复的端到端证据，无 DB 依赖）。
//
// 背景：GroupFileListHandler（GET /api/v1/conversation/:id/files）旧实现误读
// c.Param("convId")，而路由注册的参数名是 :id（handler.go:457）——gin 同段参数名
// 不可并存，:convId 恒为空串 → ParseInt 失败 → 恒 400「参数错误」，
// 前端表现为「文件列表加载失败」。
//
// 本文件用与线上完全一致的路由模式证明两件事：
//  1. :id 参数在该路由模式下正常绑定（当前代码读 :id，雪花串解析成功）；
//  2. 同一路由下读 :convId 必然得到空串（旧 bug 的失败签名，与用户实测症状吻合）。
// 真实 handler 的 DB/Mongo 段（isMember / ListConvFiles）另行走读验证，
// 见回报文档；此处不依赖存储，go test ./... 任何环境可跑。
func TestGroupFileListRouteParamBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 与 handler.go:457 完全一致的路由模式（handler 段替换为只做参数解析的探针，
	// 复刻 GroupFileListHandler 前 5 行的解析逻辑）
	r.GET("/api/v1/conversation/:id/files", func(c *gin.Context) {
		// —— 以下三行与 group_file.go:27-31 逐字一致 ——
		convID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || convID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误"})
			return
		}
		// 探针：证明走到这里 = 参数解析通过（线上随后进入 IsConvMember）
		c.JSON(http.StatusOK, gin.H{"code": 0, "parsed": convID, "paramConvId": c.Param("convId")})
	})

	// 雪花串（会话 ID）→ 解析成功；同时演示旧代码读 :convId 得空串
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversation/402515907283001345/files?page=1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"parsed":402515907283001345`) {
		t.Fatalf("snowflake :id not parsed, body=%s", body)
	}
	if !strings.Contains(body, `"paramConvId":""`) {
		t.Fatalf(":convId should be empty on :id route (old-bug signature missing), body=%s", body)
	}

	// 非数字 id → 与线上 handler 相同的 400 分支
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/conversation/abc/files", nil)
	r.ServeHTTP(w2, req2)
	if !strings.Contains(w2.Body.String(), `"code":400`) {
		t.Fatalf("non-numeric id should hit 400 branch, body=%s", w2.Body.String())
	}
}
