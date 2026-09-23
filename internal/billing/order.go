package billing

import (
	"fmt"
	"time"
)

// Kind 是订单业务类型。旧库只有 recharge_orders（充值），
// 购买 / 续费 / 升级 / 退款没有订单凭证，只留下一条 transactions 流水，
// 导致无法退款、无法对账、无法追溯。新模型把四类业务统一为订单。
type Kind string

const (
	KindRecharge Kind = "recharge" // 充值
	KindPurchase Kind = "purchase" // 新购
	KindRenew    Kind = "renew"    // 续费
	KindUpgrade  Kind = "upgrade"  // 升级补差价
	KindRefund   Kind = "refund"   // 退款
)

// Valid 报告订单类型是否已登记。
func (k Kind) Valid() bool {
	switch k {
	case KindRecharge, KindPurchase, KindRenew, KindUpgrade, KindRefund:
		return true
	default:
		return false
	}
}

// Status 是订单状态。字面量与旧库 recharge_orders.status 保持一致
// （pending / paid），其余为新增，保证旧数据可直接映射。
type Status string

const (
	StatusPending           Status = "pending"            // 已创建，未支付
	StatusPaid              Status = "paid"               // 已入账
	StatusProvisioned       Status = "provisioned"        // 资源已下发
	StatusActive            Status = "active"             // 服务生效中
	StatusFailed            Status = "failed"             // 下发失败（待退款）
	StatusCancelled         Status = "cancelled"          // 用户/管理员取消
	StatusExpired           Status = "expired"            // 超时未支付
	StatusRefunding         Status = "refunding"          // 退款处理中
	StatusRefunded          Status = "refunded"           // 已全额退款
	StatusPartiallyRefunded Status = "partially_refunded" // 已部分退款
)

// terminalStatus 判断是否为终态（不可再流转）。
func (s Status) terminal() bool {
	switch s {
	case StatusRefunded, StatusCancelled, StatusExpired:
		return true
	default:
		return false
	}
}

// transitions 是订单状态机唯一的事实来源。任何状态变更都必须命中此表，
// 禁止在 handler 里直接给 status 字段赋值（旧代码正是这么做的）。
var transitions = map[Status][]Status{
	StatusPending:           {StatusPaid, StatusCancelled, StatusExpired, StatusFailed},
	StatusPaid:              {StatusProvisioned, StatusActive, StatusFailed, StatusRefunding},
	StatusProvisioned:       {StatusActive, StatusRefunding, StatusFailed},
	StatusActive:            {StatusExpired, StatusRefunding},
	StatusFailed:            {StatusRefunding, StatusCancelled},
	StatusRefunding:         {StatusRefunded, StatusPartiallyRefunded, StatusActive},
	StatusPartiallyRefunded: {StatusRefunding},
}

// Order 是一笔计费订单。金额与退款额均为 Money，币种随订单固化。
type Order struct {
	ID         int64
	OrderNo    string
	UserID     int64
	Kind       Kind
	PlanID     int64
	InstanceID int64
	Amount     Money
	Refunded   Money
	Status     Status
	// Ref 是外部幂等键（网关流水号 / 上游系统单号）。
	Ref       string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
	PaidAt    time.Time
	// RefundableUntil 是订单进入 refunding 之前的截止时间；零值表示不限期。
	RefundableUntil time.Time
}

// CanTransition 报告从当前状态是否允许流转到 to。
func (o *Order) CanTransition(to Status) bool {
	if o.Status == to {
		return false
	}
	for _, allowed := range transitions[o.Status] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Transition 执行状态流转；非法流转返回 EYVES-302。
func (o *Order) Transition(to Status, now time.Time) error {
	if !o.CanTransition(to) {
		return newError(CodeIllegalTransition,
			fmt.Sprintf("cannot move order %s from %s to %s", o.OrderNo, o.Status, to), nil)
	}
	o.Status = to
	o.UpdatedAt = now
	if to == StatusPaid && o.PaidAt.IsZero() {
		o.PaidAt = now
	}
	return nil
}

// Outstanding 返回尚未退款的金额（Order.Amount - Order.Refunded）。
func (o *Order) Outstanding() (Money, error) {
	return o.Amount.Sub(o.Refunded)
}

// IsRefundable 报告订单在当前状态与时限内是否可以退款。
// now 传入便于测试；RefundableUntil 为零值时不限时。
func (o *Order) IsRefundable(now time.Time) bool {
	if o.Status.terminal() || o.Status == StatusRefunding {
		return false
	}
	if !o.RefundableUntil.IsZero() && now.After(o.RefundableUntil) {
		return false
	}
	switch o.Status {
	case StatusPaid, StatusProvisioned, StatusActive, StatusFailed, StatusPartiallyRefunded:
		return true
	default:
		return false
	}
}

// Validate 校验订单的必要字段，供持久化前调用。
func (o *Order) Validate() error {
	if o.OrderNo == "" {
		return newError(CodeInvalidRequest, "order number is required", nil)
	}
	if !o.Kind.Valid() {
		return newError(CodeInvalidRequest, "unknown order kind "+string(o.Kind), nil)
	}
	if o.UserID <= 0 {
		return newError(CodeInvalidRequest, "user id is required", nil)
	}
	if !o.Amount.Currency().Valid() {
		return newError(CodeUnknownCurrency, "order amount currency is not set", nil)
	}
	if o.Amount.IsNegative() {
		return newError(CodeNegativeAmount, "order amount must not be negative", nil)
	}
	return nil
}
