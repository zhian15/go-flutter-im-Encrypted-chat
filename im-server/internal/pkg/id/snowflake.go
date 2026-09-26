package id

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// ParseNodeID 从节点标识解析节点号，如 node-a → 0, node-3 → 3
func ParseNodeID(node string) (int64, error) {
	if i := strings.LastIndex(node, "-"); i >= 0 {
		if n, err := strconv.ParseInt(node[i+1:], 10, 64); err == nil {
			return n, nil
		}
	}
	return 0, nil
}

// Snowflake 雪花 ID：全局唯一、趋势递增，保证消息时序
// 41bit 时间戳 | 10bit 节点 | 12bit 序列
type Snowflake struct {
	mu        sync.Mutex
	epoch     int64
	nodeBits  uint
	nodeID    int64
	step      int64
	lastStamp int64
}

var (
	sf     *Snowflake
	sfOnce sync.Once
)

func New(nodeID int64) *Snowflake {
	return &Snowflake{
		epoch:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
		nodeBits: 10,
		nodeID:   nodeID & ((1 << 10) - 1),
		step:     0,
	}
}

func Init(nodeID int64) {
	sfOnce.Do(func() { sf = New(nodeID) })
}

func Next() int64 {
	if sf == nil {
		Init(0)
	}
	sf.mu.Lock()
	defer sf.mu.Unlock()

	now := time.Now().UnixMilli() - sf.epoch
	if now == sf.lastStamp {
		sf.step = (sf.step + 1) & ((1 << 12) - 1)
		if sf.step == 0 {
			for now <= sf.lastStamp {
				time.Sleep(time.Millisecond)
				now = time.Now().UnixMilli() - sf.epoch
			}
		}
	} else {
		sf.step = 0
	}
	sf.lastStamp = now
	return (now << (sf.nodeBits + 12)) | (sf.nodeID << 12) | sf.step
}
