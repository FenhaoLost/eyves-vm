// Package billing 是 Eyves VM 的计费域。
//
// 设计约束（对应《全局编码规范》）：
//  1. 金额一律以最小货币单位（CNY 分 / USD cents）的 int64 表示，禁止 float64。
//  2. 对外错误必须是结构化错误，Code 形如 EYVES-XXX，调用方用 errors.Is/As 判定，
//     禁止对错误做字符串比较。
//  3. 本包不 import store / panel，持久化与实例编排通过接口注入，
//     便于单测、便于替换后端（SQLite → PostgreSQL）。
package billing

import (
	"errors"
	"fmt"
)

// Code 是稳定的错误码，格式 EYVES-XXX。分段约定：
//
//	1xx  入参 / 金额校验
//	2xx  余额与账本
//	3xx  订单与状态机
//	4xx  外部依赖编排（hypervisor / 支付网关）
type Code string

// 错误码表。新增错误码必须在此登记，禁止在调用点内联字面量。
const (
	CodeInvalidAmount    Code = "EYVES-101" // 金额格式非法
	CodeAmountOutOfRange Code = "EYVES-102" // 金额超出允许区间
	CodeNegativeAmount   Code = "EYVES-103" // 需要正数处传入了 <= 0
	CodeCurrencyMismatch Code = "EYVES-104" // 币种不一致
	CodeUnknownCurrency  Code = "EYVES-105" // 未登记的币种

	CodeInsufficientBalance Code = "EYVES-201" // 余额不足
	CodeDuplicateRef        Code = "EYVES-202" // 幂等 ref 重复（视为成功重放）
	CodeAccountNotFound     Code = "EYVES-203" // 账户不存在

	CodeOrderNotFound        Code = "EYVES-301" // 订单不存在
	CodeIllegalTransition    Code = "EYVES-302" // 非法状态流转
	CodeOrderNotRefundable   Code = "EYVES-303" // 订单当前状态不可退款
	CodeRefundWindowExceeded Code = "EYVES-304" // 超出可退款时限

	CodeProvisionFailed Code = "EYVES-401" // 实例编排失败
	CodeInvalidRequest  Code = "EYVES-402" // 领域层入参缺失
)

// Error 是计费域的结构化错误。它同时携带稳定的 Code 与可展示消息，
// 并可通过 Unwrap 保留底层 cause 供 errors.Is/As 断言。
type Error struct {
	Code    Code
	Message string
	Cause   error
}

// Error 实现 error。
func (e *Error) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
}

// Unwrap 暴露底层原因，使 errors.Is(err, ErrInsufficientBalance) 之类的判定成立。
func (e *Error) Unwrap() error { return e.Cause }

// newError 构造结构化错误。message 面向运维日志，禁止包含密码、token 等敏感信息。
func newError(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// ErrXxx 是可被 errors.Is 匹配的哨兵错误。领域层的稳定契约在这里集中定义，
// 上层（handler / store 适配器）只依赖这些哨兵，不依赖具体实现。
var (
	ErrInsufficientBalance = newError(CodeInsufficientBalance, "balance would go negative", nil)
	ErrDuplicateRef        = newError(CodeDuplicateRef, "duplicate idempotency ref", nil)
	ErrAccountNotFound     = newError(CodeAccountNotFound, "account not found", nil)
	ErrOrderNotFound       = newError(CodeOrderNotFound, "order not found", nil)
	ErrOrderNotRefundable  = newError(CodeOrderNotRefundable, "order is not refundable in its current state", nil)
	ErrCurrencyMismatch    = newError(CodeCurrencyMismatch, "currency mismatch", nil)
)

// CodeOf 提取 err 的稳定错误码；非本域错误返回空字符串。
// 上层据此把领域错误映射为 HTTP 状态码，而不是解析错误文本。
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Is 让 *Error 支持按错误码匹配，便于表驱动测试写 errors.Is(err, billing.ErrCode(CodeOrderNotFound))。
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e.Code == t.Code
}

// ErrCode 返回一个只带错误码的探针错误，用于 errors.Is 断言。
func ErrCode(c Code) error { return &Error{Code: c} }
