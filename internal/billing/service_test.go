package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedNow 是全部计费用例的确定性时间基准。
var fixedNow = time.Unix(1_700_000_000, 0)

func fixedDeps(t *testing.T, prov Provisioner) (Deps, *fakeLedger, *fakeOrderRepo) {
	t.Helper()
	ledger := newFakeLedger()
	repo := newFakeOrderRepo()
	var seq int64
	deps := Deps{
		Ledger:      ledger,
		Orders:      repo,
		Provisioner: prov,
		Clock:       func() time.Time { return fixedNow },
		OrderNo: func() (string, error) {
			return fmt.Sprintf("EY%06d", atomic.AddInt64(&seq, 1)), nil
		},
	}
	return deps, ledger, repo
}

func mustMoney(t *testing.T, cents int64) Money {
	t.Helper()
	m, err := New(cents, CNY)
	requireNoError(t, err, "New money")
	return m
}

func TestNewServiceValidatesDeps(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Fatal("缺少 ledger/orders 时应报错")
	}
	_, ledger, repo := fixedDeps(t, nil)
	if _, err := NewService(Deps{Ledger: ledger}); err == nil {
		t.Fatal("缺少 order repo 时应报错")
	}
	svc, err := NewService(Deps{Ledger: ledger, Orders: repo})
	requireNoError(t, err, "NewService")
	requireTrue(t, svc != nil, "服务已构造")
	requireTrue(t, svc.clock != nil, "默认时钟已注入")
	requireTrue(t, svc.orderNo != nil, "默认单号生成器已注入")
}

func TestPurchaseHappyPath(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, err := NewService(deps)
	requireNoError(t, err, "NewService")

	ledger.balances[1] = 10000
	order, err := svc.Purchase(context.Background(), PurchaseRequest{
		UserID: 1, PlanID: 7, Image: "debian/12", Amount: mustMoney(t, 5000), Days: 30,
	})
	requireNoError(t, err, "Purchase")
	requireEqualString(t, string(order.Status), string(StatusActive), "终态 active")
	requireTrue(t, order.InstanceID > 0, "已分配实例")
	requireEqualInt64(t, ledger.Balance(1), 5000, "余额已扣减")
	requireEqualInt64(t, prov.Calls(), 1, "编排调用一次")
	requireEqualInt64(t, repo.Count(), 1, "订单已落库")
	requireEqualInt64(t, ledger.KindTotal(KindPurchase), 5000, "购买流水合计")
}

func TestPurchaseWithoutProvisioner(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	svc, err := NewService(deps)
	requireNoError(t, err, "NewService")
	ledger.balances[1] = 10000

	order, err := svc.Purchase(context.Background(), PurchaseRequest{
		UserID: 1, Amount: mustMoney(t, 3000),
	})
	requireNoError(t, err, "Purchase")
	requireEqualString(t, string(order.Status), string(StatusActive), "无编排亦到 active")
	requireEqualInt64(t, ledger.Balance(1), 7000, "余额已扣减")
}

func TestPurchaseInsufficientBalance(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 100

	_, err := svc.Purchase(context.Background(), PurchaseRequest{
		UserID: 1, Amount: mustMoney(t, 5000), Days: 30,
	})
	requireCode(t, err, ErrInsufficientBalance, CodeInsufficientBalance, "余额不足")
	requireEqualInt64(t, ledger.Balance(1), 100, "余额不变")
	requireEqualInt64(t, prov.Calls(), 0, "不应下发")
	requireEqualInt64(t, repo.Count(), 1, "订单已落库并标记失败")
}

func TestPurchaseProvisionFailureRefunds(t *testing.T) {
	prov := &fakeProvisioner{failWith: errors.New("incus: bridge missing")}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	_, err := svc.Purchase(context.Background(), PurchaseRequest{
		UserID: 1, PlanID: 7, Amount: mustMoney(t, 4000), Days: 30,
	})
	requireCode(t, err, ErrCode(CodeProvisionFailed), CodeProvisionFailed, "下发失败错误码")
	requireEqualInt64(t, ledger.Balance(1), 10000, "余额已全额退回")
	requireEqualInt64(t, ledger.KindTotal(KindRefund), 4000, "退款流水存在")

	stored, err := repo.ByNo(context.Background(), "EY000001")
	requireNoError(t, err, "ByNo")
	requireEqualString(t, string(stored.Status), string(StatusRefunded), "订单标记已退款")
	requireEqualInt64(t, stored.Refunded.Amount(), 4000, "退款额已记录")
}

func TestPurchaseIdempotentReplayByRef(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000
	req := PurchaseRequest{UserID: 1, Amount: mustMoney(t, 2000), Days: 30, Ref: "whmcs-invoice-88"}

	first, err := svc.Purchase(context.Background(), req)
	requireNoError(t, err, "首次 Purchase")
	second, err := svc.Purchase(context.Background(), req)
	requireNoError(t, err, "重放 Purchase")
	requireEqualString(t, first.OrderNo, second.OrderNo, "返回同一订单")
	requireEqualInt64(t, ledger.Balance(1), 8000, "只扣款一次")
	requireEqualInt64(t, prov.Calls(), 1, "只下发一次")
	requireEqualInt64(t, repo.Count(), 1, "只有一张订单")
}

func TestPurchaseConcurrentSameRef(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 1_000_000

	const goroutines = 50
	amount := mustMoney(t, 1000)
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = svc.Purchase(context.Background(), PurchaseRequest{
				UserID: 1, Amount: amount, Days: 30, Ref: "same-ref",
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		requireNoError(t, err, fmt.Sprintf("第 %d 个并发请求", i))
	}
	requireEqualInt64(t, repo.Count(), 1, "并发同 ref 只生成一张订单")
	requireEqualInt64(t, ledger.KindTotal(KindPurchase), 1000, "并发同 ref 只扣款一次")
	requireEqualInt64(t, ledger.Balance(1), 999_000, "余额只减一次")
	requireEqualInt64(t, prov.Calls(), 1, "并发同 ref 只下发一次")
}

func TestPurchaseConcurrentDistinctRefs(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 1_000_000

	const goroutines = 20
	amount := mustMoney(t, 1000)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Purchase(context.Background(), PurchaseRequest{
				UserID: 1, Amount: amount, Days: 30,
				Ref: fmt.Sprintf("ref-%d", idx),
			})
			if err != nil {
				t.Errorf("并发独立请求 %d 失败: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
	requireEqualInt64(t, repo.Count(), goroutines, "每笔独立订单都落库")
	requireEqualInt64(t, ledger.KindTotal(KindPurchase), goroutines*1000, "扣款总额正确")
	requireEqualInt64(t, prov.Calls(), goroutines, "每笔都下发")
}

func TestPurchaseRejectsInvalidInput(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	cases := []struct {
		name    string
		req     PurchaseRequest
		wantErr Code
	}{
		{"缺用户", PurchaseRequest{Amount: mustMoney(t, 100)}, CodeInvalidRequest},
		{"零金额", PurchaseRequest{UserID: 1, Amount: Zero(CNY)}, CodeNegativeAmount},
		{"负金额", PurchaseRequest{UserID: 1, Amount: mustMoney(t, -100)}, CodeNegativeAmount},
		{"负天数", PurchaseRequest{UserID: 1, Amount: mustMoney(t, 100), Days: -1}, CodeInvalidRequest},
		{"缺币种", PurchaseRequest{UserID: 1, Amount: Money{amount: 100}}, CodeUnknownCurrency},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Purchase(context.Background(), tc.req)
			requireCode(t, err, nil, tc.wantErr, "入参校验")
		})
	}
}

func TestPurchaseOrderNoGeneratorError(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	deps.OrderNo = func() (string, error) { return "", errors.New("entropy exhausted") }
	svc, err := NewService(deps)
	requireNoError(t, err, "NewService")
	ledger.balances[1] = 10000

	_, err = svc.Purchase(context.Background(), PurchaseRequest{UserID: 1, Amount: mustMoney(t, 100)})
	requireCode(t, err, nil, CodeInvalidRequest, "单号生成失败")
}

func TestRefundFull(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, _ := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	order, err := svc.Purchase(context.Background(), PurchaseRequest{UserID: 1, Amount: mustMoney(t, 3000), Days: 30})
	requireNoError(t, err, "Purchase")

	refunded, err := svc.Refund(context.Background(), order.OrderNo, mustMoney(t, 3000), "用户申请退款")
	requireNoError(t, err, "Refund")
	requireEqualString(t, string(refunded.Status), string(StatusRefunded), "全额退款终态")
	requireEqualInt64(t, ledger.Balance(1), 10000, "余额已恢复")
}

func TestRefundPartialThenFull(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	order, err := svc.Purchase(context.Background(), PurchaseRequest{UserID: 1, Amount: mustMoney(t, 3000), Days: 30})
	requireNoError(t, err, "Purchase")

	partial, err := svc.Refund(context.Background(), order.OrderNo, mustMoney(t, 1000), "部分退款")
	requireNoError(t, err, "部分退款")
	requireEqualString(t, string(partial.Status), string(StatusPartiallyRefunded), "部分退款状态")
	requireEqualInt64(t, ledger.Balance(1), 8000, "余额部分恢复")

	rest, err := svc.Refund(context.Background(), order.OrderNo, mustMoney(t, 2000), "剩余退款")
	requireNoError(t, err, "剩余退款")
	requireEqualString(t, string(rest.Status), string(StatusRefunded), "补齐后为全额退款")
	requireEqualInt64(t, ledger.Balance(1), 10000, "余额完全恢复")
}

func TestRefundRejectsBadRequests(t *testing.T) {
	prov := &fakeProvisioner{}
	deps, ledger, repo := fixedDeps(t, prov)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	order, err := svc.Purchase(context.Background(), PurchaseRequest{UserID: 1, Amount: mustMoney(t, 3000), Days: 30})
	requireNoError(t, err, "Purchase")

	_, err = svc.Refund(context.Background(), "NO-SUCH-ORDER", mustMoney(t, 100), "")
	requireCode(t, err, ErrOrderNotFound, CodeOrderNotFound, "订单不存在")

	_, err = svc.Refund(context.Background(), order.OrderNo, Zero(CNY), "")
	requireCode(t, err, nil, CodeNegativeAmount, "零退款额")

	_, err = svc.Refund(context.Background(), order.OrderNo, mustMoney(t, 9999), "")
	requireCode(t, err, nil, CodeAmountOutOfRange, "超额退款")

	// pending 订单不可退款。
	pending := &Order{
		OrderNo: "EYPENDING", UserID: 1, Kind: KindPurchase,
		Amount: mustMoney(t, 100), Refunded: Zero(CNY), Status: StatusPending,
	}
	requireNoError(t, repo.Create(context.Background(), pending), "创建 pending 订单")
	_, err = svc.Refund(context.Background(), "EYPENDING", mustMoney(t, 100), "")
	requireCode(t, err, ErrOrderNotRefundable, CodeOrderNotRefundable, "pending 不可退")
}

func TestRefundAfterWindowExceeded(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	// 时钟向前推进，越过 RefundableUntil。
	now := fixedNow
	deps.Clock = func() time.Time { return now }
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	order, err := svc.Purchase(context.Background(), PurchaseRequest{UserID: 1, Amount: mustMoney(t, 2000), Days: 7})
	requireNoError(t, err, "Purchase")

	now = fixedNow.AddDate(0, 0, 30) // 超出服务期，且越过 7 天窗口
	_, err = svc.Refund(context.Background(), order.OrderNo, mustMoney(t, 2000), "超期退款")
	requireCode(t, err, ErrOrderNotRefundable, CodeOrderNotRefundable, "超期不可退")
}

func TestRefundLedgerFailureRollsBackStatus(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	svc, _ := NewService(deps)
	ledger.balances[1] = 10000

	order, err := svc.Purchase(context.Background(), PurchaseRequest{UserID: 1, Amount: mustMoney(t, 3000), Days: 30})
	requireNoError(t, err, "Purchase")

	// 让账本在退款时失败，验证订单状态回滚到退款前。
	svc.ledger = &refundFailingLedger{inner: ledger}
	_, err = svc.Refund(context.Background(), order.OrderNo, mustMoney(t, 1000), "账本故障")
	requireCode(t, err, ErrCode(CodeAccountNotFound), CodeAccountNotFound, "账本失败向上传递")

	stored, err := svc.orders.ByNo(context.Background(), order.OrderNo)
	requireNoError(t, err, "ByNo")
	requireEqualString(t, string(stored.Status), string(StatusActive), "状态已回滚到 active")
}

// refundFailingLedger 包装真实 fakeLedger，仅让退款调用失败，用于验证回滚路径。
type refundFailingLedger struct{ inner *fakeLedger }

func (l *refundFailingLedger) Apply(ctx context.Context, userID int64, delta Money, kind Kind, ref, note string) (Money, error) {
	if kind == KindRefund {
		return Money{}, newError(CodeAccountNotFound, "ledger unavailable", errors.New("connection reset"))
	}
	return l.inner.Apply(ctx, userID, delta, kind, ref, note)
}

// TestDefaultOrderNo 校验默认单号生成器的前缀、长度与唯一性。
func TestDefaultOrderNo(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		no, err := defaultOrderNo()
		requireNoError(t, err, "defaultOrderNo")
		requireEqualInt64(t, int64(len(no)), 18, "EY + 16 位十六进制")
		requireEqualString(t, no[:2], "EY", "前缀")
		requireFalse(t, seen[no], "200 次生成不应碰撞")
		seen[no] = true
	}
}

// TestIsDup 校验幂等冲突判定只认 CodeDuplicateRef。
func TestIsDup(t *testing.T) {
	requireTrue(t, isDup(ErrDuplicateRef), "重复 ref 应判为幂等冲突")
	requireTrue(t, isDup(newError(CodeDuplicateRef, "wrapped", nil)), "包装后仍可判定")
	requireFalse(t, isDup(ErrInsufficientBalance), "余额不足不是幂等冲突")
	requireFalse(t, isDup(nil), "nil 不是幂等冲突")
}

// TestDaysDuration 校验天数换算。
func TestDaysDuration(t *testing.T) {
	requireEqualInt64(t, int64(daysDuration(30)), int64(30*24*time.Hour), "30 天")
	requireEqualInt64(t, int64(daysDuration(0)), 0, "0 天表示不限")
}

// TestRefundRefOf 校验退款幂等键随已退额单调变化。
func TestRefundRefOf(t *testing.T) {
	requireEqualString(t, refundRefOf("EY1", 0), "refund:EY1:0", "首次退款键")
	requireEqualString(t, refundRefOf("EY1", 1000), "refund:EY1:1000", "第二次退款键")
	requireFalse(t, refundRefOf("EY1", 0) == refundRefOf("EY1", 1000), "两次退款键必须不同")
}

// TestServiceConcurrentRefundsOfDistinctOrders 验证并发退款不同订单时的资金守恒。
//
// 说明：对「同一订单」的并发退款不在本层保证（需要 OrderRepo 提供基于 version 的
// 乐观并发控制，见自审清单 P1-1）。本用例只覆盖跨订单并发，该场景由
// 账本 ref（refund:<orderNo>）与订单唯一键共同保证正确。
func TestServiceConcurrentRefundsOfDistinctOrders(t *testing.T) {
	deps, ledger, _ := fixedDeps(t, nil)
	svc, _ := NewService(deps)
	ledger.balances[1] = 1_000_000

	const orders = 10
	const each = 1000
	orderNumbers := make([]string, orders)
	for i := 0; i < orders; i++ {
		o, err := svc.Purchase(context.Background(), PurchaseRequest{
			UserID: 1, Amount: mustMoney(t, each), Days: 30, Ref: fmt.Sprintf("buy-%d", i),
		})
		requireNoError(t, err, "Purchase")
		orderNumbers[i] = o.OrderNo
	}
	requireEqualInt64(t, ledger.Balance(1), 1_000_000-orders*each, "购买后余额")

	var wg sync.WaitGroup
	for _, orderNo := range orderNumbers {
		wg.Add(1)
		go func(no string) {
			defer wg.Done()
			if _, err := svc.Refund(context.Background(), no, mustMoney(t, each), "并发退款"); err != nil {
				t.Errorf("退款 %s 失败: %v", no, err)
			}
		}(orderNo)
	}
	wg.Wait()

	requireEqualInt64(t, ledger.Balance(1), 1_000_000, "全部退款后余额复原")
	requireEqualInt64(t, ledger.KindTotal(KindRefund), orders*each, "退款流水合计正确")
}
