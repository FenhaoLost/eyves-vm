package billing

import (
	"context"
	"sync"
	"sync/atomic"
)

// 本文件提供三个线程安全的测试替身（fake），用于在无数据库、无 hypervisor
// 的条件下验证计费编排的并发正确性。它们只实现 billing 定义的接口，
// 不依赖任何具体存储类型 —— 符合「mock 使用接口，禁止 mock 具体类型」。

// ledgerEntry 是内存账本的一条流水，用于断言金额与幂等 ref。
type ledgerEntry struct {
	UserID  int64
	Delta   int64
	Balance int64
	Kind    Kind
	Ref     string
}

// fakeLedger 是 Ledger 的内存实现。
// 关键：Apply 全程持锁，模拟真实事务的原子性，使并发测试具有意义。
type fakeLedger struct {
	mu       sync.Mutex
	balances map[int64]int64
	refs     map[string]bool
	entries  []ledgerEntry
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{balances: map[int64]int64{}, refs: map[string]bool{}}
}

func (l *fakeLedger) Apply(_ context.Context, userID int64, delta Money, kind Kind, ref, note string) (Money, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ref != "" {
		if l.refs[ref] {
			return Money{}, ErrDuplicateRef
		}
	}
	next := l.balances[userID] + delta.Amount()
	if next < 0 {
		return Money{}, ErrInsufficientBalance
	}
	l.balances[userID] = next
	if ref != "" {
		l.refs[ref] = true
	}
	l.entries = append(l.entries, ledgerEntry{UserID: userID, Delta: delta.Amount(), Balance: next, Kind: kind, Ref: ref})
	return New(next, delta.Currency())
}

// Balance 返回某用户当前余额（分）。
func (l *fakeLedger) Balance(userID int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balances[userID]
}

// KindTotal 汇总某类流水的绝对值总和，用于断言「只扣了一次款」。
func (l *fakeLedger) KindTotal(kind Kind) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var sum int64
	for _, e := range l.entries {
		if e.Kind == kind {
			if e.Delta < 0 {
				sum -= e.Delta
			} else {
				sum += e.Delta
			}
		}
	}
	return sum
}

// fakeOrderRepo 是 OrderRepo 的内存实现，Create 对 orderNo 与 ref 都做唯一性检查。
type fakeOrderRepo struct {
	mu    sync.Mutex
	byNo  map[string]*Order
	byRef map[string]*Order
	seq   int64
}

func newFakeOrderRepo() *fakeOrderRepo {
	return &fakeOrderRepo{byNo: map[string]*Order{}, byRef: map[string]*Order{}}
}

// cloneOrder 复制一份订单，避免测试与并发读写共享同一指针。
func cloneOrder(o *Order) *Order {
	c := *o
	return &c
}

func (r *fakeOrderRepo) Create(_ context.Context, o *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byNo[o.OrderNo]; exists {
		return ErrDuplicateRef
	}
	if o.Ref != "" {
		if _, exists := r.byRef[o.Ref]; exists {
			return ErrDuplicateRef
		}
		r.byRef[o.Ref] = cloneOrder(o)
	}
	r.seq++
	o.ID = r.seq
	r.byNo[o.OrderNo] = cloneOrder(o)
	return nil
}

func (r *fakeOrderRepo) Update(_ context.Context, o *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byNo[o.OrderNo]; !exists {
		return ErrOrderNotFound
	}
	r.byNo[o.OrderNo] = cloneOrder(o)
	if o.Ref != "" {
		r.byRef[o.Ref] = cloneOrder(o)
	}
	return nil
}

func (r *fakeOrderRepo) ByNo(_ context.Context, orderNo string) (*Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.byNo[orderNo]
	if !ok {
		return nil, ErrOrderNotFound
	}
	return cloneOrder(o), nil
}

func (r *fakeOrderRepo) ByRef(_ context.Context, ref string) (*Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.byRef[ref]
	if !ok {
		return nil, ErrOrderNotFound
	}
	return cloneOrder(o), nil
}

// Count 返回当前订单总数。
func (r *fakeOrderRepo) Count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.byNo))
}

// fakeProvisioner 是可编程失败/延迟的编排替身。
type fakeProvisioner struct {
	calls     int64
	deproys   int64
	failWith  error
	instanceN int64
}

func (p *fakeProvisioner) Provision(_ context.Context, _ ProvisionRequest) (int64, error) {
	atomic.AddInt64(&p.calls, 1)
	if p.failWith != nil {
		return 0, p.failWith
	}
	return atomic.AddInt64(&p.instanceN, 1), nil
}

func (p *fakeProvisioner) Deprovision(_ context.Context, _ int64) error {
	atomic.AddInt64(&p.deproys, 1)
	return nil
}

// Calls 返回 Provision 被调用的次数。
func (p *fakeProvisioner) Calls() int64 { return atomic.LoadInt64(&p.calls) }
