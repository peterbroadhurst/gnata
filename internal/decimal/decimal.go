// Package decimal implements the decimal floating-point arithmetic used by
// WithDecimalPrecision: a coefficient and a decimal exponent, rounded half to
// even to a fixed number of significant digits. Coefficients that fit in an
// int64 are kept in one, so common values avoid big.Int.
//
// The model is the General Decimal Arithmetic Specification
// (https://speleotrove.com/decimal/decarith.html), the basis of IEEE 754-2008
// decimal floating point, with the context precision set by the caller and
// rounding round-half-even. Add, subtract, multiply, divide, remainder and
// compare were tested against the results of a reference implementation of
// the specification (Python's decimal module) on random operands, including
// exact ties and cancellation, at the same precision, rounding and exponent
// limits. It differs from the specification in that:
//   - there are no special values (infinities, NaNs or negative zero) and no
//     subnormals: results below the exponent range are zero, and overflow,
//     division by zero and invalid operations are reported as ok=false;
//   - the exponent range follows float64: the adjusted exponent (that of the
//     most significant digit, e.g. 2 for 123) is limited to -324 to 308;
//   - integer powers use guard digits rather than being correctly rounded;
//   - String formats as JavaScript does (1e+21) rather than to-scientific-string.
//
// All work is bounded by the precision and the length of the input: parsing
// keeps at most prec+1 significant digits and caps the exponent, and every
// result is rounded and checked against the exponent range before it is
// returned.
package decimal

import (
	"cmp"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// MinPrecision and MaxPrecision bound the significant digits accepted by WithDecimalPrecision.
const (
	MinPrecision = 16
	MaxPrecision = 1000
)

// MaxIntegerBits bounds integers parsed from hex, binary and octal. It is above
// the float64 range, so anything longer always overflows.
const MaxIntegerBits = 1028

// Adjusted exponents are limited to the range of float64: above maxExp is an
// overflow, below minExp rounds to zero.
const (
	maxExp = 308
	minExp = -324
)

// maxParseExp caps the magnitude of a parsed exponent. Anything larger already
// overflows or underflows, and the cap keeps arithmetic on it within an int.
const maxParseExp = 1_000_000_000

// i64Digits is the most decimal digits that always fit in an int64.
const i64Digits = 18

// pow10i holds the powers of ten that fit in an int64, 10^0 to 10^18.
var pow10i = func() (p [19]int64) {
	p[0] = 1
	for i := 1; i < len(p); i++ {
		p[i] = p[i-1] * 10
	}
	return p
}()

// Decimal is coefficient × 10^exp, held in coeffBig if it is non-nil,
// otherwise coeffI64; coeffI64 is never math.MinInt64, so its absolute value
// fits in an int64.
type Decimal struct {
	coeffI64 int64
	coeffBig *big.Int
	exp      int
}

// NewInt returns n as a Decimal.
func NewInt(n int64) Decimal {
	return fromBig(big.NewInt(n), 0)
}

func fromBig(c *big.Int, exp int) Decimal {
	if c.IsInt64() && c.Int64() != math.MinInt64 {
		return Decimal{coeffI64: c.Int64(), exp: exp}
	}
	return Decimal{coeffBig: c, exp: exp}
}

func (d Decimal) coeff() *big.Int {
	if d.coeffBig != nil {
		return d.coeffBig
	}
	return big.NewInt(d.coeffI64)
}

func pow10(n int) *big.Int {
	if n < len(pow10i) {
		return big.NewInt(pow10i[n])
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// shift returns the coefficient of d multiplied by 10^k.
func shift(d Decimal, k int) *big.Int {
	return new(big.Int).Mul(d.coeff(), pow10(k))
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int {
	if d.coeffBig != nil {
		return d.coeffBig.Sign()
	}
	return cmp.Compare(d.coeffI64, 0)
}

func (d Decimal) digits() int {
	if d.coeffBig == nil {
		a, n := abs64(d.coeffI64), 1
		for n < len(pow10i) && a >= pow10i[n] {
			n++
		}
		return n
	}
	// log₁₀ 2 ≈ 0.30103, so the estimate from the bit length is exact or one too
	// many, as in https://graphics.stanford.edu/~seander/bithacks.html#IntegerLog10.
	n := d.coeffBig.BitLen()*30103/100000 + 1
	if d.coeffBig.CmpAbs(pow10(n-1)) < 0 {
		n--
	}
	return n
}

// adjusted returns the exponent of the most significant digit, e.g. 2 for 123.
func (d Decimal) adjusted() int {
	return d.exp + d.digits() - 1
}

// round rounds d half to even to prec significant digits and applies the
// exponent range. sticky reports that nonzero digits were already discarded
// below the coefficient, so an apparent tie rounds up. ok is false on overflow.
func (d Decimal) round(prec int, sticky bool) (Decimal, bool) {
	if d.Sign() == 0 {
		return Decimal{}, true
	}
	if k := d.digits() - prec; k > 0 {
		d = d.drop(k, sticky)
		if d.digits() > prec { // a carry such as 999 → 1000; the dropped digit is 0
			d = d.drop(1, false)
		}
	}
	switch adj := d.adjusted(); {
	case adj > maxExp:
		return Decimal{}, false
	case adj < minExp:
		return Decimal{}, true
	}
	return d, true
}

// drop removes the k least significant digits of d, rounding half to even.
// k may be at most the number of digits.
func (d Decimal) drop(k int, sticky bool) Decimal {
	if d.coeffBig == nil && k < len(pow10i) {
		a, p := abs64(d.coeffI64), pow10i[k]
		q, r := a/p, a%p
		if r > p-r || (r == p-r && (sticky || q&1 == 1)) {
			q++
		}
		if d.coeffI64 < 0 {
			q = -q
		}
		return Decimal{coeffI64: q, exp: d.exp + k}
	}
	c, p := d.coeff(), pow10(k)
	q, r := new(big.Int).QuoRem(c, p, new(big.Int))
	if h := r.Abs(r).Lsh(r, 1).Cmp(p); h > 0 || (h == 0 && (sticky || q.Bit(0) == 1)) {
		q.Add(q, big.NewInt(int64(c.Sign())))
	}
	return fromBig(q, d.exp+k)
}

// Parse parses a decimal number in the syntax of strconv.ParseFloat (without
// hex, inf, nan or underscores) and rounds it to prec digits. ok is false for
// invalid syntax or overflow. Digits beyond prec+1 only record whether any are
// nonzero, and the exponent is capped, so the work is linear in len(s) and the
// result bounded.
func Parse[S string | []byte](s S, prec int) (Decimal, bool) {
	i, neg := 0, false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var (
		coeffI64              int64
		buf                   []byte // kept digits once there are more than i64Digits
		kept, scale           int
		sticky, digit, dotted bool
	)
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' && !dotted {
			dotted = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		digit = true
		switch {
		case kept == 0 && c == '0':
			if dotted {
				scale--
			}
		case kept <= prec:
			if dotted {
				scale--
			}
			kept++
			switch {
			case kept <= i64Digits:
				coeffI64 = coeffI64*10 + int64(c-'0')
			case buf == nil:
				buf = append(strconv.AppendInt(make([]byte, 0, prec+1), coeffI64, 10), c)
			default:
				buf = append(buf, c)
			}
		default:
			sticky = sticky || c != '0'
			if !dotted {
				scale++
			}
		}
	}
	if !digit {
		return Decimal{}, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		e, ok := parseExp(s[i+1:])
		if !ok {
			return Decimal{}, false
		}
		scale += e
	} else if i != len(s) {
		return Decimal{}, false
	}
	d := Decimal{coeffI64: coeffI64, exp: scale}
	if buf != nil {
		c, _ := new(big.Int).SetString(string(buf), 10)
		d = fromBig(c, scale)
	}
	if neg {
		d = d.Neg()
	}
	return d.round(prec, sticky)
}

// parseExp parses a signed exponent, capping its magnitude at maxParseExp.
func parseExp[S string | []byte](s S) (int, bool) {
	i, neg := 0, false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	e, start := 0, i
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		if e < maxParseExp {
			e = e*10 + int(s[i]-'0')
		}
	}
	if i == start || i != len(s) {
		return 0, false
	}
	if neg {
		return -e, true
	}
	return e, true
}

// FromValue converts a float64 or json.Number to a Decimal rounded to prec
// digits. A float64 converts via its shortest decimal form, so 0.1 stays 0.1.
func FromValue(v any, prec int) (Decimal, bool) {
	switch n := v.(type) {
	case json.Number:
		return Parse(string(n), prec)
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return Decimal{}, false
		}
		if n == math.Trunc(n) && math.Abs(n) <= 1<<53 {
			return Decimal{coeffI64: int64(n)}, true
		}
		var buf [32]byte
		return Parse(strconv.AppendFloat(buf[:0], n, 'g', -1, 64), prec)
	}
	return Decimal{}, false
}

// FromBig returns the integer n rounded to prec digits.
func FromBig(n *big.Int, prec int) (Decimal, bool) {
	return fromBig(n, 0).round(prec, false)
}

// IsCanonical reports whether s is already in the form String returns for
// prec, so that it can be used as is. It lets common literals skip a parse and
// format on every evaluation.
func IsCanonical(s string, prec int) bool {
	neg := s != "" && s[0] == '-'
	if neg {
		s = s[1:]
	}
	intPart, frac, dotted := s, "", false
	for i := range len(s) {
		if s[i] == '.' {
			intPart, frac, dotted = s[:i], s[i+1:], true
			break
		}
	}
	n := len(intPart) + len(frac)
	switch {
	case intPart == "" || !allDigits(intPart) || !allDigits(frac):
		return false
	case dotted && (frac == "" || frac[len(frac)-1] == '0'):
		return false
	case intPart[0] == '0' && (len(intPart) > 1 || !dotted):
		return s == "0" && !neg
	case intPart == "0":
		n--
	}
	return n <= prec
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String returns d without trailing zeros, in plain digits when
// -prec <= adjusted exponent < prec and otherwise in exponent form as in
// JavaScript (1.5e+80).
func (d Decimal) String(prec int) string {
	if d.Sign() == 0 {
		return "0"
	}
	var db [24]byte
	var ds []byte
	if d.coeffBig == nil {
		ds = strconv.AppendInt(db[:0], abs64(d.coeffI64), 10)
	} else {
		ds = new(big.Int).Abs(d.coeffBig).Append(db[:0], 10)
	}
	exp := d.exp
	for len(ds) > 1 && ds[len(ds)-1] == '0' {
		ds = ds[:len(ds)-1]
		exp++
	}
	n := len(ds)
	adj := exp + n - 1
	var ob [48]byte
	out := ob[:0]
	if d.Sign() < 0 {
		out = append(out, '-')
	}
	switch {
	case adj >= prec || adj < -prec:
		out = append(out, ds[0])
		if n > 1 {
			out = append(append(out, '.'), ds[1:]...)
		}
		out = append(out, 'e')
		if adj >= 0 {
			out = append(out, '+')
		}
		out = strconv.AppendInt(out, int64(adj), 10)
	case exp >= 0:
		out = append(out, ds...)
		for range exp {
			out = append(out, '0')
		}
	case adj >= 0:
		out = append(append(append(out, ds[:n+exp]...), '.'), ds[n+exp:]...)
	default:
		out = append(out, '0', '.')
		for range -adj - 1 {
			out = append(out, '0')
		}
		out = append(out, ds...)
	}
	return string(out)
}

// Value returns d as a canonical json.Number.
func (d Decimal) Value(prec int) json.Number {
	return json.Number(d.String(prec))
}

// Neg returns -d.
func (d Decimal) Neg() Decimal {
	if d.coeffBig != nil {
		return fromBig(new(big.Int).Neg(d.coeffBig), d.exp)
	}
	return Decimal{coeffI64: -d.coeffI64, exp: d.exp}
}

// Abs returns |d|.
func (d Decimal) Abs() Decimal {
	if d.Sign() < 0 {
		return d.Neg()
	}
	return d
}

// Cmp compares x and y, returning -1, 0 or +1.
func (x Decimal) Cmp(y Decimal) int {
	xs, ys := x.Sign(), y.Sign()
	if xs != ys || xs == 0 {
		return cmp.Compare(xs, ys)
	}
	if xa, ya := x.adjusted(), y.adjusted(); xa != ya {
		if xa > ya {
			return xs
		}
		return -xs
	}
	// Equal adjusted exponents, so the shift is at most the precision.
	e := min(x.exp, y.exp)
	if x.coeffBig == nil && y.coeffBig == nil {
		a, aok := scaleI64(x.coeffI64, x.exp-e)
		b, bok := scaleI64(y.coeffI64, y.exp-e)
		if aok && bok {
			return cmp.Compare(a, b)
		}
	}
	return shift(x, x.exp-e).Cmp(shift(y, y.exp-e))
}

func scaleI64(v int64, k int) (int64, bool) {
	if k >= len(pow10i) || abs64(v) > math.MaxInt64/pow10i[k] {
		return 0, false
	}
	return v * pow10i[k], true
}

// Add returns x + y rounded to prec digits. ok is false on overflow.
func (x Decimal) Add(y Decimal, prec int) (Decimal, bool) {
	switch {
	case y.Sign() == 0:
		return x, true
	case x.Sign() == 0:
		return y, true
	}
	// An operand more than prec+2 digits below the other is under half a unit in
	// the last place of the result, so it cannot change it; this also bounds the
	// shift below.
	switch xa, ya := x.adjusted(), y.adjusted(); {
	case ya < xa-prec-2:
		return x, true
	case xa < ya-prec-2:
		return y, true
	}
	e := min(x.exp, y.exp)
	if x.coeffBig == nil && y.coeffBig == nil {
		a, aok := scaleI64(x.coeffI64, x.exp-e)
		b, bok := scaleI64(y.coeffI64, y.exp-e)
		// Overflow is only possible when the signs match and the sum's differs.
		if s := a + b; aok && bok && ((a < 0) != (b < 0) || (a < 0) == (s < 0)) && s != math.MinInt64 {
			return Decimal{coeffI64: s, exp: e}.round(prec, false)
		}
	}
	return fromBig(new(big.Int).Add(shift(x, x.exp-e), shift(y, y.exp-e)), e).round(prec, false)
}

// Sub returns x - y rounded to prec digits. ok is false on overflow.
func (x Decimal) Sub(y Decimal, prec int) (Decimal, bool) {
	return x.Add(y.Neg(), prec)
}

// Mul returns x × y rounded to prec digits. ok is false on overflow.
func (x Decimal) Mul(y Decimal, prec int) (Decimal, bool) {
	e := x.exp + y.exp
	if x.coeffBig == nil && y.coeffBig == nil && (x.coeffI64 == 0 || abs64(y.coeffI64) <= math.MaxInt64/abs64(x.coeffI64)) {
		return Decimal{coeffI64: x.coeffI64 * y.coeffI64, exp: e}.round(prec, false)
	}
	return fromBig(new(big.Int).Mul(x.coeff(), y.coeff()), e).round(prec, false)
}

// Quo returns x / y rounded to prec digits. ok is false for a zero divisor or on overflow.
func (x Decimal) Quo(y Decimal, prec int) (Decimal, bool) {
	if y.Sign() == 0 {
		return Decimal{}, false
	}
	if x.Sign() == 0 {
		return Decimal{}, true
	}
	// Scale x so the quotient has at least prec+1 digits; the remainder then only decides sticky.
	k := max(prec+1+y.digits()-x.digits(), 0)
	q, r := new(big.Int).QuoRem(shift(x, k), y.coeff(), new(big.Int))
	return fromBig(q, x.exp-k-y.exp).round(prec, r.Sign() != 0)
}

// Rem returns the remainder of x / y truncated toward zero, so its sign
// follows x. ok is false for a zero divisor, or when x is more than prec
// digits larger in magnitude than y.
func (x Decimal) Rem(y Decimal, prec int) (Decimal, bool) {
	if y.Sign() == 0 || x.adjusted()-y.adjusted() > prec {
		return Decimal{}, false
	}
	if x.Sign() == 0 {
		return Decimal{}, true
	}
	e := min(x.exp, y.exp)
	return fromBig(new(big.Int).Rem(shift(x, x.exp-e), shift(y, y.exp-e)), e).round(prec, false)
}

// Pow returns x**n by repeated squaring with guard digits, so a non-negative
// power whose result fits in prec digits is exact. ok is false on overflow, or
// for 0 to a negative power.
func (x Decimal) Pow(n int64, prec int) (Decimal, bool) {
	if n == math.MinInt64 {
		return Decimal{}, false
	}
	wp := prec + 20 // guard digits for at most 63 squarings
	z, b := Decimal{coeffI64: 1}, x
	var ok bool
	for m := abs64(n); m > 0; m >>= 1 {
		if m&1 == 1 {
			if z, ok = z.Mul(b, wp); !ok {
				return Decimal{}, false
			}
		}
		if m > 1 {
			if b, ok = b.Mul(b, wp); !ok {
				return Decimal{}, false
			}
		}
	}
	if n < 0 {
		return Decimal{coeffI64: 1}.Quo(z, prec)
	}
	return z.round(prec, false)
}

// Int64 returns d as an int64 when it is an integer in range.
func (d Decimal) Int64() (int64, bool) {
	c := d.coeff()
	switch {
	case d.Sign() == 0:
		return 0, true
	case d.exp > i64Digits:
		return 0, false
	case d.exp >= 0:
		c = shift(d, d.exp)
	default:
		var r *big.Int
		if c, r = new(big.Int).QuoRem(c, pow10(-d.exp), new(big.Int)); r.Sign() != 0 {
			return 0, false
		}
	}
	return c.Int64(), c.IsInt64()
}

// Floor returns the greatest integer not above d.
func (d Decimal) Floor(prec int) Decimal {
	return d.toInt(-1, prec)
}

// Ceil returns the least integer not below d.
func (d Decimal) Ceil(prec int) Decimal {
	return d.toInt(1, prec)
}

// toInt rounds d to an integer toward -∞ (dir -1) or +∞ (dir +1).
func (d Decimal) toInt(dir, prec int) Decimal {
	if d.exp >= 0 || d.Sign() == 0 {
		return d
	}
	q, r := new(big.Int), new(big.Int).Set(d.coeff())
	if k := -d.exp; k <= d.digits() {
		q.QuoRem(r, pow10(k), r)
	}
	if r.Sign() == dir {
		q.Add(q, big.NewInt(int64(dir)))
	}
	z, _ := fromBig(q, 0).round(prec, false)
	return z
}

// RoundPlaces rounds d half to even to the given number of decimal places,
// which may be negative. places must be bounded by the caller.
func (d Decimal) RoundPlaces(places, prec int) (Decimal, bool) {
	switch k := -places - d.exp; {
	case k <= 0:
		return d, true
	case k > d.digits():
		return Decimal{}, true
	default:
		return d.drop(k, false).round(prec, false)
	}
}

// Adjusted returns the exponent of the most significant digit of d, e.g. 2 for
// 123, and 0 for zero.
func (d Decimal) Adjusted() int {
	if d.Sign() == 0 {
		return 0
	}
	return d.adjusted()
}

// Shift returns d × 10^k exactly. ok is false if the result is outside the
// exponent range.
func (d Decimal) Shift(k int) (Decimal, bool) {
	if d.Sign() == 0 {
		return Decimal{}, true
	}
	d.exp += k
	if adj := d.adjusted(); adj > maxExp || adj < minExp {
		return Decimal{}, false
	}
	return d, true
}

// Fixed returns |d| rounded half to even to places decimal places, in plain
// digits with exactly places fraction digits, as strconv.FormatFloat does with
// 'f'. places must be non-negative and bounded by the caller.
func (d Decimal) Fixed(places, prec int) (string, bool) {
	r, ok := d.Abs().RoundPlaces(places, prec)
	if !ok {
		return "", false
	}
	ds := r.coeff().String() + strings.Repeat("0", max(r.exp, 0))
	frac := max(-r.exp, 0)
	if len(ds) <= frac {
		ds = strings.Repeat("0", frac-len(ds)+1) + ds
	}
	if places == 0 {
		return ds, true
	}
	return ds[:len(ds)-frac] + "." + ds[len(ds)-frac:] + strings.Repeat("0", places-frac), true
}
