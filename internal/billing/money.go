package billing

import (
	"strconv"
	"strings"
)

// Currency 是 ISO-4217 币种代码。计费域只认已登记的币种，避免出现
// "元 / 分" 混用（旧代码 handlers_billing.go 的 fmtMoney 把币种硬编码为人民币）。
type Currency string

const (
	// CNY 人民币，最小单位「分」。
	CNY Currency = "CNY"
	// USD 美元，最小单位「cent」。
	USD Currency = "USD"
)

// maxAmountCents 单笔金额上限，1e13 分 = 1000 亿元，防止溢出与误输入。
// 旧代码用魔法数字 100_000_00（100 万元）散落在 handlers_billing.go:418、
// handlers_pay.go:105 两处；现集中为一个具名常量并由 ParseMoney 统一执行。
const maxAmountCents int64 = 1e13

// minorUnits 返回币种小数位数。当前登记币种均为 2 位。
func (c Currency) minorUnits() int {
	switch c {
	case CNY, USD:
		return 2
	default:
		return 2
	}
}

// Valid 报告币种是否已登记。
func (c Currency) Valid() bool {
	switch c {
	case CNY, USD:
		return true
	default:
		return false
	}
}

// Symbol 返回展示用符号，仅用于界面，不参与计算。
func (c Currency) Symbol() string {
	switch c {
	case CNY:
		return "¥"
	case USD:
		return "$"
	default:
		return ""
	}
}

// Money 是一笔以最小货币单位表示的金额。零值不可用，必须经 New 或 Parse 构造，
// 以强制携带币种（旧代码 int64 裸传，币种全靠上下文，容易串味）。
type Money struct {
	amount   int64
	currency Currency
}

// New 构造金额。amount 为最小单位（分）。
func New(amount int64, c Currency) (Money, error) {
	if !c.Valid() {
		return Money{}, newError(CodeUnknownCurrency, "unsupported currency "+string(c), nil)
	}
	if amount > maxAmountCents || amount < -maxAmountCents {
		return Money{}, newError(CodeAmountOutOfRange, "amount exceeds "+strconv.FormatInt(maxAmountCents, 10)+" minor units", nil)
	}
	return Money{amount: amount, currency: c}, nil
}

// Zero 返回某币种的零值金额。
func Zero(c Currency) Money { return Money{amount: 0, currency: c} }

// Parse 把十进制字符串（"12.5"、"-3"、"0.05"）解析为金额，全程整数运算，
// 不经过 float64，因此不会出现 0.1+0.2 类误差。
func Parse(s string, c Currency) (Money, error) {
	if !c.Valid() {
		return Money{}, newError(CodeUnknownCurrency, "unsupported currency "+string(c), nil)
	}
	s = strings.TrimSpace(s)
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return Money{}, newError(CodeInvalidAmount, "empty amount", nil)
	}
	whole, frac, hasDot := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if hasDot && frac == "" {
		return Money{}, newError(CodeInvalidAmount, "trailing decimal point", nil)
	}
	units := c.minorUnits()
	if len(frac) > units {
		return Money{}, newError(CodeInvalidAmount, "at most "+strconv.Itoa(units)+" decimal places", nil)
	}
	frac += strings.Repeat("0", units-len(frac))

	cents, err := decimalToInt64(whole + frac)
	if err != nil {
		return Money{}, err
	}
	if negative {
		cents = -cents
	}
	return New(cents, c)
}

// decimalToInt64 把纯数字串解析为 int64，并在溢出前用 maxAmountCents 截断。
func decimalToInt64(digits string) (int64, error) {
	var v int64
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, newError(CodeInvalidAmount, "not a decimal number", nil)
		}
		if v > (maxAmountCents-int64(r-'0'))/10 {
			return 0, newError(CodeAmountOutOfRange, "amount too large", nil)
		}
		v = v*10 + int64(r-'0')
	}
	return v, nil
}

// Amount 返回最小单位整数（分）。
func (m Money) Amount() int64 { return m.amount }

// Currency 返回币种。
func (m Money) Currency() Currency { return m.currency }

// IsZero 报告是否为零。
func (m Money) IsZero() bool { return m.amount == 0 }

// IsNegative 报告是否为负。
func (m Money) IsNegative() bool { return m.amount < 0 }

// String 渲染为小数字符串（不带币种符号），例如 -12.50。
func (m Money) String() string {
	neg := ""
	amount := m.amount
	if amount < 0 {
		neg, amount = "-", -amount
	}
	scale := int64(1)
	for i := 0; i < m.currency.minorUnits(); i++ {
		scale *= 10
	}
	if scale == 1 {
		return neg + strconv.FormatInt(amount, 10)
	}
	return neg + strconv.FormatInt(amount/scale, 10) + "." +
		padLeft(strconv.FormatInt(amount%scale, 10), m.currency.minorUnits())
}

// Display 渲染为带符号的展示串，仅用于界面。
func (m Money) Display() string { return m.currency.Symbol() + m.String() }

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

// sameCurrency 校验两笔金额币种一致。
func sameCurrency(a, b Money) error {
	if a.currency != b.currency {
		return newError(CodeCurrencyMismatch,
			"cannot combine "+string(a.currency)+" with "+string(b.currency), nil)
	}
	return nil
}

// Add 相加，币种不一致时报 EYVES-104。
func (m Money) Add(other Money) (Money, error) {
	if err := sameCurrency(m, other); err != nil {
		return Money{}, err
	}
	return New(m.amount+other.amount, m.currency)
}

// Sub 相减，币种不一致时报 EYVES-104。
func (m Money) Sub(other Money) (Money, error) {
	if err := sameCurrency(m, other); err != nil {
		return Money{}, err
	}
	return New(m.amount-other.amount, m.currency)
}

// Neg 取负。
func (m Money) Neg() Money { return Money{amount: -m.amount, currency: m.currency} }

// Abs 取绝对值。
func (m Money) Abs() Money {
	if m.amount < 0 {
		return m.Neg()
	}
	return m
}

// Mul 乘以一个非负整数倍数（用于时长/数量计价）。
func (m Money) Mul(factor int64) (Money, error) {
	if factor < 0 {
		return Money{}, newError(CodeNegativeAmount, "multiplier must not be negative", nil)
	}
	return New(m.amount*factor, m.currency)
}

// Cmp 比较：a<b 返回 -1，a==b 返回 0，a>b 返回 1。币种不一致报错。
func (m Money) Cmp(other Money) (int, error) {
	if err := sameCurrency(m, other); err != nil {
		return 0, err
	}
	switch {
	case m.amount < other.amount:
		return -1, nil
	case m.amount > other.amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// LessThan 便捷比较，币种不一致时按「不小于」处理并附带错误。
func (m Money) LessThan(other Money) (bool, error) {
	c, err := m.Cmp(other)
	if err != nil {
		return false, err
	}
	return c < 0, nil
}
