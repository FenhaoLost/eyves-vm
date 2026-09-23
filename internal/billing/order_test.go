package billing

import (
	"testing"
	"time"
)

// newTestOrder 构造一张 pending 订单，用于状态机与校验用例。
func newTestOrder(t *testing.T) *Order {
	t.Helper()
	amount, err := New(5000, CNY)
	requireNoError(t, err, "New amount")
	return &Order{
		OrderNo:  "EYTEST0001",
		UserID:   42,
		Kind:     KindPurchase,
		PlanID:   7,
		Amount:   amount,
		Refunded: Zero(CNY),
		Status:   StatusPending,
	}
}

func TestOrderTransitions(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name string
		from Status
		to   Status
		want bool
	}{
		{"pending→paid", StatusPending, StatusPaid, true},
		{"pending→cancelled", StatusPending, StatusCancelled, true},
		{"pending→expired", StatusPending, StatusExpired, true},
		{"pending→failed", StatusPending, StatusFailed, true},
		{"pending→active 非法", StatusPending, StatusActive, false},
		{"pending 自环", StatusPending, StatusPending, false},
		{"paid→provisioned", StatusPaid, StatusProvisioned, true},
		{"paid→active（无编排）", StatusPaid, StatusActive, true},
		{"paid→refunding", StatusPaid, StatusRefunding, true},
		{"paid→expired 非法", StatusPaid, StatusExpired, false},
		{"provisioned→active", StatusProvisioned, StatusActive, true},
		{"active→expired", StatusActive, StatusExpired, true},
		{"active→refunding", StatusActive, StatusRefunding, true},
		{"failed→refunding", StatusFailed, StatusRefunding, true},
		{"refunding→refunded", StatusRefunding, StatusRefunded, true},
		{"refunding→partially", StatusRefunding, StatusPartiallyRefunded, true},
		{"refunding→active 回滚", StatusRefunding, StatusActive, true},
		{"partially→refunding", StatusPartiallyRefunded, StatusRefunding, true},
		{"refunded 是终态", StatusRefunded, StatusRefunding, false},
		{"cancelled 是终态", StatusCancelled, StatusPaid, false},
		{"expired 是终态", StatusExpired, StatusPaid, false},
		{"未知状态", Status("bogus"), StatusPaid, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newTestOrder(t)
			o.Status = tc.from
			requireEqualInt64(t, boolToInt64(o.CanTransition(tc.to)), boolToInt64(tc.want), "CanTransition")
			err := o.Transition(tc.to, now)
			if tc.want {
				requireNoError(t, err, "Transition")
				requireEqualString(t, string(o.Status), string(tc.to), "状态已变更")
				requireEqualInt64(t, o.UpdatedAt.Unix(), now.Unix(), "UpdatedAt 已刷新")
			} else {
				requireCode(t, err, nil, CodeIllegalTransition, "非法流转")
				requireEqualString(t, string(o.Status), string(tc.from), "状态未变更")
			}
		})
	}
}

func TestTransitionSetsPaidAtOnce(t *testing.T) {
	first := time.Unix(1, 0)
	second := time.Unix(2, 0)
	o := newTestOrder(t)
	requireNoError(t, o.Transition(StatusPaid, first), "pending→paid")
	requireEqualInt64(t, o.PaidAt.Unix(), first.Unix(), "PaidAt 首次写入")
	requireNoError(t, o.Transition(StatusProvisioned, second), "paid→provisioned")
	requireEqualInt64(t, o.PaidAt.Unix(), first.Unix(), "PaidAt 不被覆盖")
}

func TestOutstanding(t *testing.T) {
	o := newTestOrder(t)
	got, err := o.Outstanding()
	requireNoError(t, err, "Outstanding")
	requireEqualInt64(t, got.Amount(), 5000, "未退款即全额")

	partial, err := New(2000, CNY)
	requireNoError(t, err, "New partial")
	o.Refunded = partial
	got, err = o.Outstanding()
	requireNoError(t, err, "Outstanding partial")
	requireEqualInt64(t, got.Amount(), 3000, "部分退款后余额")
}

func TestIsRefundable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	cases := []struct {
		name       string
		status     Status
		until      time.Time
		refundable bool
	}{
		{"paid 且未过期", StatusPaid, future, true},
		{"provisioned 且未过期", StatusProvisioned, future, true},
		{"active 且未过期", StatusActive, future, true},
		{"failed 且未过期", StatusFailed, future, true},
		{"partially_refunded 可退余款", StatusPartiallyRefunded, future, true},
		{"paid 但已过时限", StatusPaid, past, false},
		{"pending 不可退", StatusPending, future, false},
		{"refunding 不可重复退", StatusRefunding, future, false},
		{"refunded 不可退", StatusRefunded, future, false},
		{"cancelled 不可退", StatusCancelled, future, false},
		{"无时限（零值）可退", StatusActive, time.Time{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newTestOrder(t)
			o.Status = tc.status
			o.RefundableUntil = tc.until
			requireEqualInt64(t, boolToInt64(o.IsRefundable(now)), boolToInt64(tc.refundable), "IsRefundable")
		})
	}
}

func TestOrderValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Order)
		wantErr Code
	}{
		{"合法订单", func(*Order) {}, ""},
		{"缺单号", func(o *Order) { o.OrderNo = "" }, CodeInvalidRequest},
		{"缺用户", func(o *Order) { o.UserID = 0 }, CodeInvalidRequest},
		{"未知类型", func(o *Order) { o.Kind = "gift" }, CodeInvalidRequest},
		{"缺币种", func(o *Order) { o.Amount = Money{} }, CodeUnknownCurrency},
		{"负金额", func(o *Order) { o.Amount = Money{amount: -1, currency: CNY} }, CodeNegativeAmount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newTestOrder(t)
			tc.mutate(o)
			err := o.Validate()
			if tc.wantErr == "" {
				requireNoError(t, err, "Validate")
				return
			}
			requireCode(t, err, nil, tc.wantErr, "Validate 错误码")
		})
	}
}

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{KindRecharge, KindPurchase, KindRenew, KindUpgrade, KindRefund} {
		requireTrue(t, k.Valid(), "已登记类型 "+string(k))
	}
	requireFalse(t, Kind("gift").Valid(), "未登记类型")
}

// boolToInt64 把布尔转为 0/1，便于在 requireEqualInt64 中做表驱动断言。
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
