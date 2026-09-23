package billing

import (
	"errors"
	"testing"
)

// 轻量断言助手：本项目不引入 testify（避免为单包新增依赖），
// 用标准库实现 require 语义，失败即终止当前用例。

func requireNoError(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 期望无错误，实际得到 %v", ctx, err)
	}
}

func requireTrue(t *testing.T, cond bool, ctx string) {
	t.Helper()
	if !cond {
		t.Fatalf("%s: 期望为真", ctx)
	}
}

func requireFalse(t *testing.T, cond bool, ctx string) {
	t.Helper()
	if cond {
		t.Fatalf("%s: 期望为假", ctx)
	}
}

func requireEqualInt64(t *testing.T, got, want int64, ctx string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: 期望 %d，实际 %d", ctx, want, got)
	}
}

func requireEqualString(t *testing.T, got, want, ctx string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: 期望 %q，实际 %q", ctx, want, got)
	}
}

// requireCode 断言 err 携带的稳定错误码等于 want。用 errors.Is 而非字符串比较。
func requireCode(t *testing.T, err error, sentinel error, want Code, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望错误码 %s，实际无错误", ctx, want)
	}
	if sentinel != nil && !errors.Is(err, sentinel) {
		t.Fatalf("%s: errors.Is 未匹配哨兵错误；err=%v", ctx, err)
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("%s: 期望错误码 %s，实际 %s (err=%v)", ctx, want, got, err)
	}
}
