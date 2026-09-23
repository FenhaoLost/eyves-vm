package billing

import (
	"errors"
	"testing"
)

func TestParseMoney(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    int64
		wantErr Code
	}{
		{"整数", "12", 1200, ""},
		{"一位小数", "12.5", 1250, ""},
		{"两位小数", "0.05", 5, ""},
		{"省略整数部分", ".5", 50, ""},
		{"负数", "-3", -300, ""},
		{"负零", "-0", 0, ""},
		{"零", "0", 0, ""},
		{"前后空格", "  9.99  ", 999, ""},
		{"空串", "", 0, CodeInvalidAmount},
		{"纯小数点", ".", 0, CodeInvalidAmount},
		{"三位小数", "1.234", 0, CodeInvalidAmount},
		{"尾部小数点", "12.", 0, CodeInvalidAmount},
		{"非数字", "abc", 0, CodeInvalidAmount},
		{"科学计数法", "1e3", 0, CodeInvalidAmount},
		{"中文数字", "十二", 0, CodeInvalidAmount},
		{"金额过大", "999999999999999999999", 0, CodeAmountOutOfRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse(tc.input, CNY)
			if tc.wantErr != "" {
				requireCode(t, err, ErrCode(tc.wantErr), tc.wantErr, "Parse 错误码")
				return
			}
			requireNoError(t, err, "Parse")
			requireEqualInt64(t, m.Amount(), tc.want, "Parse 金额")
			requireEqualString(t, string(m.Currency()), string(CNY), "Parse 币种")
		})
	}
}

func TestParseRejectsUnknownCurrency(t *testing.T) {
	_, err := Parse("1", Currency("JPY"))
	requireCode(t, err, ErrCode(CodeUnknownCurrency), CodeUnknownCurrency, "未登记币种")
}

func TestMoneyString(t *testing.T) {
	cases := []struct {
		cents int64
		cur   Currency
		want  string
	}{
		{1250, CNY, "12.50"},
		{-300, CNY, "-3.00"},
		{5, CNY, "0.05"},
		{0, CNY, "0.00"},
		{100000, USD, "1000.00"},
	}
	for _, tc := range cases {
		m, err := New(tc.cents, tc.cur)
		requireNoError(t, err, "New")
		requireEqualString(t, m.String(), tc.want, "String")
	}
}

func TestMoneyDisplay(t *testing.T) {
	m, err := New(1250, CNY)
	requireNoError(t, err, "New")
	requireEqualString(t, m.Display(), "¥12.50", "CNY Display")
	u, err := New(100, USD)
	requireNoError(t, err, "New")
	requireEqualString(t, u.Display(), "$1.00", "USD Display")
	requireEqualString(t, Currency("JPY").Symbol(), "", "未登记币种符号为空")
}

func TestNewRejectsOutOfRangeAndUnknownCurrency(t *testing.T) {
	_, err := New(maxAmountCents+1, CNY)
	requireCode(t, err, ErrCode(CodeAmountOutOfRange), CodeAmountOutOfRange, "上溢")
	_, err = New(-maxAmountCents-1, CNY)
	requireCode(t, err, ErrCode(CodeAmountOutOfRange), CodeAmountOutOfRange, "下溢")
	_, err = New(1, Currency("EUR"))
	requireCode(t, err, ErrCode(CodeUnknownCurrency), CodeUnknownCurrency, "未登记币种")
	requireTrue(t, CNY.Valid(), "CNY 应合法")
	requireTrue(t, USD.Valid(), "USD 应合法")
}

func TestMoneyArithmetic(t *testing.T) {
	a, _ := New(1000, CNY)
	b, _ := New(250, CNY)

	sum, err := a.Add(b)
	requireNoError(t, err, "Add")
	requireEqualInt64(t, sum.Amount(), 1250, "Add")

	diff, err := a.Sub(b)
	requireNoError(t, err, "Sub")
	requireEqualInt64(t, diff.Amount(), 750, "Sub")

	triple, err := b.Mul(3)
	requireNoError(t, err, "Mul")
	requireEqualInt64(t, triple.Amount(), 750, "Mul")

	requireEqualInt64(t, diff.Neg().Amount(), -750, "Neg")
	requireEqualInt64(t, diff.Neg().Abs().Amount(), 750, "Abs")
	requireTrue(t, Zero(CNY).IsZero(), "Zero 应为零")
	requireTrue(t, diff.Neg().IsNegative(), "Neg 应为负")
	requireFalse(t, diff.IsNegative(), "正值不应为负")
}

func TestMoneyCurrencyMismatch(t *testing.T) {
	cny, _ := New(100, CNY)
	usd, _ := New(100, USD)
	_, err := cny.Add(usd)
	requireCode(t, err, ErrCurrencyMismatch, CodeCurrencyMismatch, "Add 币种不一致")
	_, err = cny.Sub(usd)
	requireCode(t, err, ErrCurrencyMismatch, CodeCurrencyMismatch, "Sub 币种不一致")
	_, err = cny.Cmp(usd)
	requireCode(t, err, ErrCurrencyMismatch, CodeCurrencyMismatch, "Cmp 币种不一致")
	_, err = cny.LessThan(usd)
	requireCode(t, err, ErrCurrencyMismatch, CodeCurrencyMismatch, "LessThan 币种不一致")
}

func TestMoneyMulNegative(t *testing.T) {
	a, _ := New(100, CNY)
	_, err := a.Mul(-1)
	requireCode(t, err, ErrCode(CodeNegativeAmount), CodeNegativeAmount, "负倍数")
}

func TestMoneyCmpAndLessThan(t *testing.T) {
	small, _ := New(100, CNY)
	big, _ := New(200, CNY)
	same, _ := New(100, CNY)

	c, err := small.Cmp(big)
	requireNoError(t, err, "Cmp")
	requireEqualInt64(t, int64(c), -1, "small<big")

	c, err = big.Cmp(small)
	requireNoError(t, err, "Cmp")
	requireEqualInt64(t, int64(c), 1, "big>small")

	c, err = small.Cmp(same)
	requireNoError(t, err, "Cmp")
	requireEqualInt64(t, int64(c), 0, "equal")

	lt, err := small.LessThan(big)
	requireNoError(t, err, "LessThan")
	requireTrue(t, lt, "small 应小于 big")
}

func TestMinorUnits(t *testing.T) {
	requireEqualInt64(t, int64(CNY.minorUnits()), 2, "CNY 两位小数")
	requireEqualInt64(t, int64(USD.minorUnits()), 2, "USD 两位小数")
	requireEqualInt64(t, int64(Currency("JPY").minorUnits()), 2, "未登记币种默认两位")
}

func TestErrorFormatting(t *testing.T) {
	base := ErrInsufficientBalance
	requireEqualString(t, base.Error(), "EYVES-201: balance would go negative", "无 cause 的错误串")

	wrapped := newError(CodeProvisionFailed, "provision instance", base)
	requireTrue(t, len(wrapped.Error()) > 0, "有 cause 的错误串非空")
	requireTrue(t, errors.Is(wrapped, base), "Unwrap 应保留 cause")
	requireTrue(t, errors.Is(wrapped, ErrCode(CodeProvisionFailed)), "按错误码匹配")
	requireFalse(t, errors.Is(wrapped, ErrOrderNotFound), "不应匹配无关错误码")
	requireEqualString(t, string(CodeOf(nil)), "", "nil 无错误码")
	requireEqualString(t, string(CodeOf(errStub{})), "", "非本域错误无错误码")
}

// errStub 是一个与本域无关的错误，用于验证 CodeOf 的空返回。
type errStub struct{}

func (errStub) Error() string { return "stub" }
