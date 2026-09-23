package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Ledger 是余额账本的写入端口。实现方（store 适配器）必须保证：
//   - 一次调用是一个事务：改余额 + 记流水必须同时成功或同时失败；
//   - 余额不允许变负，负余额返回 ErrInsufficientBalance；
//   - ref 非空时按 ref 幂等，重复返回 ErrDuplicateRef。
//
// 接口在调用方（billing）定义，实现方返回具体类型 —— 见《全局编码规范·结构》。
type Ledger interface {
	Apply(ctx context.Context, userID int64, delta Money, kind Kind, ref, note string) (Money, error)
}

// OrderRepo 是订单持久化端口。
type OrderRepo interface {
	// Create 落库；ref 冲突时必须返回 ErrDuplicateRef。
	Create(ctx context.Context, o *Order) error
	// Update 回写订单全量字段（状态机推进后调用）。
	Update(ctx context.Context, o *Order) error
	// ByNo 按站内单号查询；不存在返回 ErrOrderNotFound。
	ByNo(ctx context.Context, orderNo string) (*Order, error)
	// ByRef 按外部幂等键查询；不存在返回 ErrOrderNotFound。
	ByRef(ctx context.Context, ref string) (*Order, error)
}

// Provisioner 是实例编排端口。设计上刻意与 hypervisor 解耦：
// Incus / KVM(libvirt) / OpenVZ 各自实现该接口，计费层不感知具体后端。
type Provisioner interface {
	Provision(ctx context.Context, req ProvisionRequest) (instanceID int64, err error)
	Deprovision(ctx context.Context, instanceID int64) error
}

// ProvisionRequest 是下发给编排层的请求（已通过计费校验）。
type ProvisionRequest struct {
	OrderNo  string
	UserID   int64
	PlanID   int64
	Image    string
	Duration time.Duration
}

// Clock 注入时间源，便于测试确定性的到期与时限逻辑。
type Clock func() time.Time

// OrderNoFunc 生成站内单号。默认实现为 "EY" + 16 位十六进制。
type OrderNoFunc func() (string, error)

// Deps 是 Service 的构造依赖。缺失必填项时 NewService 直接失败，
// 避免半初始化对象在运行时才 panic。
type Deps struct {
	Ledger      Ledger
	Orders      OrderRepo
	Provisioner Provisioner
	Clock       Clock
	OrderNo     OrderNoFunc
}

// Service 是计费编排服务。自身无状态，可并发调用；
// 并发正确性由 Ledger 的事务性与 OrderRepo 的唯一约束保证。
type Service struct {
	ledger      Ledger
	orders      OrderRepo
	provisioner Provisioner
	clock       Clock
	orderNo     OrderNoFunc
}

// NewService 构造计费服务。
func NewService(d Deps) (*Service, error) {
	if d.Ledger == nil {
		return nil, newError(CodeInvalidRequest, "ledger is required", nil)
	}
	if d.Orders == nil {
		return nil, newError(CodeInvalidRequest, "order repo is required", nil)
	}
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	gen := d.OrderNo
	if gen == nil {
		gen = defaultOrderNo
	}
	return &Service{
		ledger:      d.Ledger,
		orders:      d.Orders,
		provisioner: d.Provisioner, // 允许为 nil：纯充值场景无需编排
		clock:       clock,
		orderNo:     gen,
	}, nil
}

// PurchaseRequest 是一次购买的入参。
type PurchaseRequest struct {
	UserID int64
	PlanID int64
	Image  string
	Amount Money
	// Days 是服务时长（天），0 表示不限（如纯额度类套餐）。
	Days int
	// Ref 是可选的外部幂等键（例如上游 WHMCS 的账单号）。
	Ref  string
	Note string
	// Currency 必须与 Amount 一致；由 Amount 自带，此处仅作显式校验冗余。
}

// Purchase 执行「下单 → 扣款 → 下发」流程。
//
// 关键不变量：扣款成功后任何失败都必须退款，且退款失败必须向上报错，
// 不允许静默吞掉（旧代码 handlers_billing.go:200 与 :310 用 `_, _ =` 丢弃了退款错误，
// 会导致用户余额被吞且无人知晓）。
func (s *Service) Purchase(ctx context.Context, req PurchaseRequest) (*Order, error) {
	if err := validatePurchase(req); err != nil {
		return nil, err
	}
	if req.Ref != "" {
		if existing, err := s.orders.ByRef(ctx, req.Ref); err == nil {
			return existing, nil // 幂等重放
		}
	}

	order, err := s.newOrder(ctx, req)
	if err != nil {
		return nil, err
	}
	if _, err := s.ledger.Apply(ctx, req.UserID, req.Amount.Neg(), KindPurchase, order.OrderNo, req.Note); err != nil {
		return nil, s.failPending(ctx, order, err)
	}
	if err := order.Transition(StatusPaid, s.clock()); err != nil {
		return nil, err
	}

	if err := s.provision(ctx, order, req); err != nil {
		return nil, err
	}
	// 终态由 Purchase 统一推进：无编排需求时 paid→active，
	// 有编排需求时 provision 已推进到 provisioned，再 provisioned→active。
	if err := order.Transition(StatusActive, s.clock()); err != nil {
		return nil, err
	}
	if err := s.orders.Update(ctx, order); err != nil {
		return nil, err
	}
	return order, nil
}

// newOrder 生成并落库一张 pending 订单。ref 冲突时返回已存在订单。
func (s *Service) newOrder(ctx context.Context, req PurchaseRequest) (*Order, error) {
	orderNo, err := s.orderNo()
	if err != nil {
		return nil, newError(CodeInvalidRequest, "generate order number", err)
	}
	order := &Order{
		OrderNo:         orderNo,
		UserID:          req.UserID,
		Kind:            KindPurchase,
		PlanID:          req.PlanID,
		Amount:          req.Amount,
		Refunded:        Zero(req.Amount.Currency()),
		Status:          StatusPending,
		Ref:             req.Ref,
		Note:            req.Note,
		CreatedAt:       s.clock(),
		UpdatedAt:       s.clock(),
		RefundableUntil: s.refundableUntil(req.Days),
	}
	if err := order.Validate(); err != nil {
		return nil, err
	}
	if err := s.orders.Create(ctx, order); err != nil {
		if isDup(err) && req.Ref != "" {
			if existing, e := s.orders.ByRef(ctx, req.Ref); e == nil {
				return existing, nil
			}
		}
		return nil, err
	}
	return order, nil
}

// provision 下发资源；失败则退款并返回 EYVES-401。
// 成功时只推进到 provisioned，终态 active 由调用方推进。
func (s *Service) provision(ctx context.Context, order *Order, req PurchaseRequest) error {
	if s.provisioner == nil {
		// 无编排需求（纯额度/充值类）：保持 paid，由调用方推进 active。
		return nil
	}
	instanceID, err := s.provisioner.Provision(ctx, ProvisionRequest{
		OrderNo: order.OrderNo, UserID: req.UserID, PlanID: req.PlanID,
		Image: req.Image, Duration: daysDuration(req.Days),
	})
	if err != nil {
		return s.refundAfterFailure(ctx, order, newError(CodeProvisionFailed, "provision instance", err))
	}
	order.InstanceID = instanceID
	return order.Transition(StatusProvisioned, s.clock())
}

// refundAfterFailure 在下发失败后补偿退款。退款本身失败时向上抛错，
// 绝不静默，使运维可通过错误码 EYVES-203/201 定位。
func (s *Service) refundAfterFailure(ctx context.Context, order *Order, cause error) error {
	if err := order.Transition(StatusFailed, s.clock()); err != nil {
		return err
	}
	if _, err := s.ledger.Apply(ctx, order.UserID, order.Amount,
		KindRefund, "refund:failed:"+order.OrderNo, "下发失败自动退款"); err != nil {
		return newError(CodeProvisionFailed,
			"provision failed and refund also failed, manual intervention required: "+order.OrderNo, err)
	}
	order.Refunded = order.Amount
	if err := order.Transition(StatusRefunding, s.clock()); err != nil {
		return err
	}
	if err := order.Transition(StatusRefunded, s.clock()); err != nil {
		return err
	}
	if err := s.orders.Update(ctx, order); err != nil {
		return err
	}
	return cause
}

// failPending 把一张尚未扣款的订单标记为失败并返回原因。
// 若状态回写失败，优先返回回写错误（订单表与内存态已不一致，必须暴露）。
func (s *Service) failPending(ctx context.Context, order *Order, cause error) error {
	if err := order.Transition(StatusFailed, s.clock()); err != nil {
		return err
	}
	if err := s.orders.Update(ctx, order); err != nil {
		return err
	}
	return cause
}

// Refund 对订单发起退款。amount 必须 > 0 且不超过未退金额。
func (s *Service) Refund(ctx context.Context, orderNo string, amount Money, reason string) (*Order, error) {
	order, err := s.orders.ByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	if !order.IsRefundable(now) {
		return nil, newError(CodeOrderNotRefundable, "order "+orderNo+" is not refundable", nil)
	}
	if amount.IsNegative() || amount.IsZero() {
		return nil, newError(CodeNegativeAmount, "refund amount must be positive", nil)
	}
	outstanding, err := order.Outstanding()
	if err != nil {
		return nil, err
	}
	if ok, err := moreThan(amount, outstanding); err != nil {
		return nil, err
	} else if ok {
		return nil, newError(CodeAmountOutOfRange, "refund exceeds outstanding amount", nil)
	}

	prev := order.Status
	// 退款幂等键必须逐次唯一，否则第二笔退款会被账本判为重复。
	// 用「本次退款前的已退额」作为序号，天然单调且无需额外计数状态。
	refundRef := refundRefOf(order.OrderNo, order.Refunded.Amount())
	if err := order.Transition(StatusRefunding, now); err != nil {
		return nil, err
	}
	if err := s.orders.Update(ctx, order); err != nil {
		return nil, err
	}
	if _, err := s.ledger.Apply(ctx, order.UserID, amount, KindRefund,
		refundRef, reason); err != nil {
		// 账本失败：回滚到原状态，保证订单表与账本一致。
		order.Status = prev
		if rollbackErr := s.orders.Update(ctx, order); rollbackErr != nil {
			return nil, newError(CodeAccountNotFound,
				"refund failed and status rollback also failed, manual intervention required: "+order.OrderNo,
				errors.Join(err, rollbackErr))
		}
		return nil, err
	}
	order.Refunded, err = order.Refunded.Add(amount)
	if err != nil {
		return nil, err
	}
	target := StatusPartiallyRefunded
	settled, err := order.Refunded.Cmp(order.Amount)
	if err != nil {
		return nil, err
	}
	if settled == 0 {
		target = StatusRefunded
	}
	if err := order.Transition(target, now); err != nil {
		return nil, err
	}
	if err := s.orders.Update(ctx, order); err != nil {
		return nil, err
	}
	return order, nil
}

// validatePurchase 校验购买入参。
func validatePurchase(req PurchaseRequest) error {
	if req.UserID <= 0 {
		return newError(CodeInvalidRequest, "user id is required", nil)
	}
	if !req.Amount.Currency().Valid() {
		return newError(CodeUnknownCurrency, "amount currency is required", nil)
	}
	if req.Amount.IsNegative() || req.Amount.IsZero() {
		return newError(CodeNegativeAmount, "purchase amount must be positive", nil)
	}
	if req.Days < 0 {
		return newError(CodeInvalidRequest, "days must not be negative", nil)
	}
	return nil
}

// refundableUntil 计算可退款截止时间：服务期内均可退，服务期外给 7 天窗口。
func (s *Service) refundableUntil(days int) time.Time {
	if days > 0 {
		return s.clock().AddDate(0, 0, days)
	}
	return s.clock().AddDate(0, 0, 7)
}

// daysDuration 把天数转为 Duration；0 表示不限。
func daysDuration(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}

// moreThan 报告 a > b，币种不一致时报错。
func moreThan(a, b Money) (bool, error) {
	c, err := a.Cmp(b)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

// refundRefOf 生成逐次唯一的退款幂等键：refund:<单号>:<退款前已退额>。
func refundRefOf(orderNo string, refundedBefore int64) string {
	return "refund:" + orderNo + ":" + strconv.FormatInt(refundedBefore, 10)
}

// isDup 判定是否为幂等键冲突。
func isDup(err error) bool {
	return CodeOf(err) == CodeDuplicateRef
}

// defaultOrderNo 生成站内单号：EY + 8 字节随机十六进制。
func defaultOrderNo() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "EY" + hex.EncodeToString(b[:]), nil
}
