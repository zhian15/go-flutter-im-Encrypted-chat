package store

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrateMySQL 幂等执行 migrations/*.sql
// 已执行过的文件记录在 schema_migrations 表，重复启动不会重复执行。
//
// 【单连接执行】迁移文件里用了会话状态（SET @var / PREPARE / EXECUTE，见
// 007/009/012 的 information_schema 幂等建列），GORM 连接池可能把相邻两条
// 语句分到不同连接 → 会话变量丢失 → IF(NULL=0) 静默走 'SELECT 1' 分支、
// 该建的列没建还被记成已迁移。因此全程固定用一条 raw 连接顺序执行。
func MigrateMySQL() error {
	if DB == nil {
		return fmt.Errorf("mysql not initialized")
	}
	ctx := context.Background()
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	exec := func(query string, args ...any) error {
		_, err := conn.ExecContext(ctx, query, args...)
		return err
	}

	// 建迁移记录表（幂等）
	if err := exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		filename VARCHAR(255) NOT NULL UNIQUE,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return err
	}

	entries, err := os.ReadDir("migrations")
	if err != nil {
		// 兼容 Docker（WORKDIR /app 下 migrations 存在）与本地运行
		if _, err2 := os.Stat("../migrations"); err2 == nil {
			entries, err = os.ReadDir("../migrations")
		} else {
			return fmt.Errorf("migrations dir not found: %v", err)
		}
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		var cnt int
		if err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", f).Scan(&cnt); err != nil {
			return fmt.Errorf("check %s: %w", f, err)
		}
		if cnt > 0 {
			continue // 已执行
		}
		content, err := os.ReadFile(filepath.Join("migrations", f))
		if err != nil {
			if _, err2 := os.Stat("../migrations"); err2 == nil {
				content, err = os.ReadFile(filepath.Join("../migrations", f))
			}
			if err != nil {
				return fmt.Errorf("read %s: %w", f, err)
			}
		}
		// 按分号分割逐条执行（迁移文件为简单 DDL/DML，无存储过程）
		for _, stmt := range strings.Split(string(content), ";") {
			stmt = strings.TrimSpace(stmt)
			// 尾部纯注释片段（最后一个分号后的注释行）跳过；
			// 「注释行 + 真实语句」同片段的照常执行（MySQL 解析器忽略行注释），
			// 只按 HasPrefix(-- ) 跳过会把带前缀注释的语句一起吞掉
			if stripCommentLines(stmt) == "" {
				continue
			}
			if err := exec(stmt); err != nil {
				return fmt.Errorf("exec %s: %w\nstmt: %s", f, err, stmt[:min(len(stmt), 200)])
			}
		}
		if err := exec("INSERT INTO schema_migrations (filename) VALUES (?)", f); err != nil {
			return err
		}
		log.Printf("migration applied: %s", f)
	}
	log.Println("mysql migrations up to date")
	return nil
}

// stripCommentLines 去掉以 -- 开头的注释行（忽略前导空白），返回剩余内容。
// 用于判断分号切出的片段是否只含注释（纯注释跳过；真实语句照常执行）。
func stripCommentLines(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
