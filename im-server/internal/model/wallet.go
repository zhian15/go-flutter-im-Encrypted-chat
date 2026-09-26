package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ========== 充值/提现：枚举常量 ==========
const (
	PayMethodWeChat = 1 // 充值：微信扫码
	PayMethodAliPay = 2 // 充值：支付宝
	PayMethodBank   = 3 // 充值：银行卡转账

	// 订单状态：充值 / 提现 通用
	OrderStatusPending  = 1 // 待审核
	OrderStatusApproved = 2 // 已通过
	OrderStatusRejected = 3 // 已拒绝
)

// ========== 通用 JSON 列类型（存 withdraw_account_snapshot） ==========

// JSONBlob 实现 driver.Valuer / sql.Scanner，便于 GORM 存/取任意 JSON 到 JSON 列
type JSONBlob map[string]any

func (j JSONBlob) Value() (driver.Value, error) {
	if j == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (j *JSONBlob) Scan(value any) error {
	if j == nil {
		return fmt.Errorf("JSONBlob.Scan: nil pointer")
	}
	var b []byte
	switch v := value.(type) {
	case nil:
		*j = JSONBlob{}
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("JSONBlob.Scan: unsupported type %T", value)
	}
	if len(b) == 0 {
		*j = JSONBlob{}
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*j = m
	return nil
}

// 钱包交易类型
const (
	WalletTxRecharge = "recharge" // 充值
	WalletTxWithdraw = "withdraw" // 提现
	WalletTxAdjust   = "adjust"   // 手工调整（后台加/减余额）
	WalletTxRedOut   = "red_out"  // 发红包支出
	WalletTxRedIn    = "red_in"   // 领取红包收入
	WalletTxTrOut    = "tr_out"   // 转账支出
	WalletTxTrIn     = "tr_in"    // 转账收款
	// 冻结 / 解冻：发红包或转账时钱先从 balance 挪到 frozen，并不离开用户账上
	WalletTxFreeze   = "freeze"   // 冻结（发出红包/转账）：balance -X，frozen +X
	WalletTxSettle   = "settle"   // 结算（对方领取）：发送方 frozen -X；收款方 balance +X、type=red_in/tr_in
	WalletTxUnfreeze = "unfreeze" // 解冻退回（24h 未领完）：frozen -R，balance +R
	// 补偿退款：已扣款但消息落库失败时原路退回（与 tr_out / red_out 配对对账）
	WalletTxRedOutRefund = "red_out_refund" // 发红包失败退回
	WalletTxTrOutRefund  = "tr_out_refund"  // 转账失败退回
)

// WalletTransaction 钱包流水（充值/提现/手工调整/红包/转账收支）
//
// 对账恒等式（B-22）：
//
//	Σ(amount)       == Σ(user.balance) - 初始余额
//	Σ(frozen_delta) == Σ(user.frozen)
//	Σ(user.frozen)  == Σ(money_packet 未结算的 total - claimed)
//
// 任何一条不成立都说明有资金凭空产生或蒸发，/admin/wallet/reconcile 会直接报出来。
type WalletTransaction struct {
	ID          int64     `gorm:"primaryKey" json:"id,string"`
	UserID      int64     `gorm:"index" json:"userId,string"`            // 归属用户
	Type        string    `gorm:"size:16;index" json:"type"`             // recharge/withdraw/adjust/freeze/settle/unfreeze/red_in/tr_in/...
	Amount      float64   `gorm:"type:decimal(12,2)" json:"amount"`      // 可用余额变动（正=入账 负=支出）
	FrozenDelta float64   `gorm:"type:decimal(12,2)" json:"frozenDelta"` // 冻结金额变动（正=冻结 负=解冻）
	Balance     float64   `gorm:"type:decimal(12,2)" json:"balance"`     // 变动后可用余额
	Frozen      float64   `gorm:"type:decimal(12,2)" json:"frozen"`      // 变动后冻结金额
	Title       string    `gorm:"size:64" json:"title"`                  // 展示标题（如"手工调整""领取红包"）
	Remark      string    `gorm:"size:255" json:"remark"`                // 备注（后台调整原因等）
	RefID       string    `gorm:"size:64" json:"refId"`                  // 关联对象（消息ID 等）
	Operator    int64     `gorm:"default:0" json:"operatorId,string"`    // 操作人（后台调整时为管理员 ID）
	CreatedAt   time.Time `json:"createdAt"`
}

func (WalletTransaction) TableName() string { return "wallet_transaction" }

// ========== 充值订单 ==========
type RechargeOrder struct {
	ID               int64      `gorm:"primaryKey" json:"id,string"`
	UserID           int64      `gorm:"index;not null" json:"userId,string"`
	Amount           float64    `gorm:"type:decimal(12,2);not null" json:"amount"`
	PayMethod        int        `gorm:"not null;default:1" json:"payMethod"`
	ReceiveQrcodeURL string     `gorm:"size:512" json:"receiveQrcodeUrl"`
	ProofImage       string     `gorm:"size:512" json:"proofImage"`
	PayTxNo          string     `gorm:"size:128" json:"payTxNo"`
	Status           int        `gorm:"index;not null;default:1" json:"status"`
	RejectReason     string     `gorm:"size:255" json:"rejectReason"`
	ReviewerID       int64      `gorm:"not null;default:0" json:"reviewerId,string"`
	ReviewedAt       *time.Time `json:"reviewedAt"`
	Remark           string     `gorm:"size:255" json:"remark"`
	CreatedAt        time.Time  `json:"createdAt"`
}

func (RechargeOrder) TableName() string { return "recharge_order" }

// ========== 用户提现绑定信息 ==========
type WithdrawAccount struct {
	ID          int64 `gorm:"primaryKey" json:"id,string"`
	UserID      int64 `gorm:"uniqueIndex;not null" json:"userId,string"`
	AccountType int   `gorm:"not null;default:1" json:"accountType"` // 1WeChat 2AliPay 3Bank
	// 微信
	WechatQrcodeURL string `gorm:"size:512" json:"wechatQrcodeUrl"`
	WechatName      string `gorm:"size:64" json:"wechatName"`
	// 支付宝
	AlipayQrcodeURL string `gorm:"size:512" json:"alipayQrcodeUrl"`
	AlipayAccount   string `gorm:"size:128" json:"alipayAccount"`
	AlipayName      string `gorm:"size:64" json:"alipayName"`
	// 银行卡
	BankCardNo      string    `gorm:"size:64" json:"bankCardNo"`
	BankName        string    `gorm:"size:128" json:"bankName"`
	BankAccountName string    `gorm:"size:64" json:"bankAccountName"`
	UpdatedAt       time.Time `json:"updatedAt"`
	CreatedAt       time.Time `json:"createdAt"`
}

func (WithdrawAccount) TableName() string { return "withdraw_account" }

// ========== 提现订单 ==========
type WithdrawOrder struct {
	ID              int64      `gorm:"primaryKey" json:"id,string"`
	UserID          int64      `gorm:"index;not null" json:"userId,string"`
	Amount          float64    `gorm:"type:decimal(12,2);not null" json:"amount"`
	Fee             float64    `gorm:"type:decimal(12,2);not null;default:0" json:"fee"`
	ActualAmount    float64    `gorm:"type:decimal(12,2);not null" json:"actualAmount"`
	WithdrawType    int        `gorm:"not null;default:1" json:"withdrawType"` // 1WeChat 2AliPay 3Bank
	AccountSnapshot JSONBlob   `gorm:"type:json" json:"accountSnapshot"`
	Status          int        `gorm:"index;not null;default:1" json:"status"`
	RejectReason    string     `gorm:"size:255" json:"rejectReason"`
	ReviewerID      int64      `gorm:"not null;default:0" json:"reviewerId,string"`
	ReviewedAt      *time.Time `json:"reviewedAt"`
	Remark          string     `gorm:"size:255" json:"remark"`
	CreatedAt       time.Time  `json:"createdAt"`
}

func (WithdrawOrder) TableName() string { return "withdraw_order" }
