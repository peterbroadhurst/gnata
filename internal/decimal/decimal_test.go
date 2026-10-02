package decimal_test

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/recolabs/gnata/internal/decimal"
)

const p16 = decimal.MinPrecision

// str formats a result for comparison, with "!" for ok=false.
func str(d decimal.Decimal, ok bool, prec int) string {
	if !ok {
		return "!"
	}
	return d.String(prec)
}

func parse(t *testing.T, s string, prec int) decimal.Decimal {
	t.Helper()
	d, ok := decimal.Parse(s, prec)
	if !ok {
		t.Fatalf("Parse(%q) failed", s)
	}
	return d
}

func TestParse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0", "0"},
		{"-0", "0"},
		{"+2", "2"},
		{"001", "1"},
		{"1.50", "1.5"},
		{".5", "0.5"},
		{"5.", "5"},
		{"1e3", "1000"},
		{"1E-3", "0.001"},
		{"0.0000001", "0.0000001"},
		{"1e-17", "1e-17"},
		{"1.5e-20", "1.5e-20"},
		{"1e15", "1000000000000000"},
		{"1e16", "1e+16"},
		{"0.1234567890123456789", "0.1234567890123457"},
		{"12345678901234565", "1.234567890123456e+16"},
		{"12345678901234575", "1.234567890123458e+16"},
		{"1234567890123456500001", "1.234567890123457e+21"},
		{"1234567890123456500000", "1.234567890123456e+21"},
		{"1.23456789012345650000000001", "1.234567890123457"},
		{"99999999999999999", "1e+17"},
		{"9999999999999999.5", "1e+16"},
		{"1e308", "1e+308"},
		{"1e-324", "1e-324"},
		{"9e-325", "0"},
		{"1e-400", "0"},
		{"1e-999999999999", "0"},
		{"1e309", "!"},
		{"1e999999999999", "!"},
		{"", "!"},
		{"-", "!"},
		{".", "!"},
		{"e5", "!"},
		{"1e", "!"},
		{"1e+", "!"},
		{"1.2.3", "!"},
		{"1_000", "!"},
		{"0x10", "!"},
		{"inf", "!"},
		{"nan", "!"},
	} {
		d, ok := decimal.Parse(c.in, p16)
		if got := str(d, ok, p16); got != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestArith(t *testing.T) {
	const u256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	for _, c := range []struct {
		prec           int
		x, op, y, want string
	}{
		{16, "0.1", "+", "0.2", "0.3"},
		{16, "5", "+", "0", "5"},
		{16, "0", "+", "5", "5"},
		{16, "5", "-", "5", "0"},
		{16, "9999999999999999", "+", "1", "1e+16"},
		{16, "1e100", "+", "1", "1e+100"},
		{16, "1", "+", "1e100", "1e+100"},
		{16, "-1e100", "+", "1", "-1e+100"},
		{16, "1e308", "*", "10", "!"},
		{16, "1e-200", "*", "1e-200", "0"},
		{16, "1.5", "*", "-2", "-3"},
		{16, "1", "/", "3", "0.3333333333333333"},
		{16, "2", "/", "3", "0.6666666666666667"},
		{16, "10", "/", "4", "2.5"},
		{16, "0", "/", "5", "0"},
		{16, "1", "/", "0", "!"},
		{16, "1e-300", "/", "1e100", "0"},
		{16, "-7", "%", "3", "-1"},
		{16, "7", "%", "-3", "1"},
		{16, "5.5", "%", "2", "1.5"},
		{16, "0", "%", "3", "0"},
		{16, "1e15", "%", "7", "6"},
		{16, "1e20", "%", "3", "!"},
		{16, "1", "%", "0", "!"},
		{16, "2", "**", "10", "1024"},
		{16, "2", "**", "-2", "0.25"},
		{16, "-2", "**", "3", "-8"},
		{16, "1.1", "**", "2", "1.21"},
		{16, "7", "**", "0", "1"},
		{16, "2", "**", "64", "1.844674407370955e+19"},
		{16, "0", "**", "-1", "!"},
		{16, "10", "**", "400", "!"},
		{16, "10", "**", "513", "!"},
		{16, "1", "**", "-9223372036854775808", "!"},
		{20, "9223372036854775807", "+", "1", "9223372036854775808"},
		{20, "-9223372036854775807", "-", "1", "-9223372036854775808"},
		{20, "4611686018427387904", "*", "2", "9223372036854775808"},
		{20, "99999999999999999999", "*", "3", "3e+20"},
		{20, "1", "-", "12345678901234567891", "-12345678901234567890"},
		{78, u256, "+", "1", "115792089237316195423570985008687907853269984665640564039457584007913129639936"},
		{78, u256, "*", u256, "1.34078079299425970995740249982058461274793658205923933777235614437217640300733e+154"},
		{78, "1", "/", "7", "0.142857142857142857142857142857142857142857142857142857142857142857142857142857"},
	} {
		x, y := parse(t, c.x, c.prec), parse(t, c.y, c.prec)
		var z decimal.Decimal
		var ok bool
		switch c.op {
		case "+":
			z, ok = x.Add(y, c.prec)
		case "-":
			z, ok = x.Sub(y, c.prec)
		case "*":
			z, ok = x.Mul(y, c.prec)
		case "/":
			z, ok = x.Quo(y, c.prec)
		case "%":
			z, ok = x.Rem(y, c.prec)
		case "**":
			n, _ := strconv.ParseInt(c.y, 10, 64)
			z, ok = x.Pow(n, c.prec)
		}
		if got := str(z, ok, c.prec); got != c.want {
			t.Errorf("%s %s %s = %s, want %s", c.x, c.op, c.y, got, c.want)
		}
	}
}

func TestCmp(t *testing.T) {
	for _, c := range []struct {
		x, y string
		want int
	}{
		{"1", "2", -1},
		{"2", "1", 1},
		{"0.1", "0.10", 0},
		{"123", "1.23e2", 0},
		{"0", "-0", 0},
		{"-1", "-2", 1},
		{"1e100", "1", 1},
		{"-1e100", "1", -1},
		{"1e-5", "1e-6", 1},
		{"1e-6", "1e-5", -1},
		{"1e-5", "-1", 1},
		{"9.5", "9.000000000000000001", 1},
		{"100000000000000000000000000001", "100000000000000000000000000002", -1},
	} {
		if got := parse(t, c.x, 78).Cmp(parse(t, c.y, 78)); got != c.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", c.x, c.y, got, c.want)
		}
	}
}

func TestIntegers(t *testing.T) {
	for _, c := range []struct{ in, floor, ceil string }{
		{"2", "2", "2"},
		{"1.5", "1", "2"},
		{"-1.5", "-2", "-1"},
		{"0.1", "0", "1"},
		{"-0.1", "-1", "0"},
		{"1e-20", "0", "1"},
		{"1e20", "1e+20", "1e+20"},
	} {
		d := parse(t, c.in, p16)
		if got := d.Floor(p16).String(p16); got != c.floor {
			t.Errorf("Floor(%s) = %s, want %s", c.in, got, c.floor)
		}
		if got := d.Ceil(p16).String(p16); got != c.ceil {
			t.Errorf("Ceil(%s) = %s, want %s", c.in, got, c.ceil)
		}
	}
	for _, c := range []struct {
		in     string
		places int
		want   string
	}{
		{"2.5", 0, "2"},
		{"3.5", 0, "4"},
		{"-2.5", 0, "-2"},
		{"1.25", 1, "1.2"},
		{"1.35", 1, "1.4"},
		{"1.5", 5, "1.5"},
		{"0.004", 2, "0"},
		{"1250", -2, "1200"},
		{"1350", -2, "1400"},
		{"5", -1, "0"},
		{"15", -1, "20"},
		{"1", -5, "0"},
	} {
		d, ok := parse(t, c.in, p16).RoundPlaces(c.places, p16)
		if got := str(d, ok, p16); got != c.want {
			t.Errorf("RoundPlaces(%s, %d) = %s, want %s", c.in, c.places, got, c.want)
		}
	}
	for _, c := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"42", 42, true},
		{"-42", -42, true},
		{"4.2e1", 42, true},
		{"1e18", 1e18, true},
		{"9223372036854775807", math.MaxInt64, true},
		{"1.5", 0, false},
		{"1e19", 0, false},
		{"9223372036854775808", 0, false},
	} {
		got, ok := parse(t, c.in, 20).Int64()
		if ok != c.ok || ok && got != c.want {
			t.Errorf("Int64(%s) = %d, %v, want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFixed(t *testing.T) {
	for _, c := range []struct {
		in     string
		places int
		want   string
	}{
		{"1.5", 3, "1.500"},
		{"0.125", 2, "0.12"},
		{"0.135", 2, "0.14"},
		{"-2.5", 0, "2"},
		{"123", 0, "123"},
		{"0", 2, "0.00"},
		{"0.004", 2, "0.00"},
		{"0.006", 2, "0.01"},
		{"1e-5", 7, "0.0000100"},
		{"1e20", 1, "100000000000000000000.0"},
		{"12345678901234567.89", 2, "12345678901234570.00"},
	} {
		got, ok := parse(t, c.in, p16).Fixed(c.places, p16)
		if !ok || got != c.want {
			t.Errorf("Fixed(%s, %d) = %s, %v, want %s", c.in, c.places, got, ok, c.want)
		}
	}
	if got, ok := parse(t, strings.Repeat("9", 309)+".5", 1000).Fixed(0, 1000); ok {
		t.Errorf("Fixed(1e309 - 0.5, 0) = %s, want overflow", got)
	}
	for _, c := range []struct {
		in     string
		k, adj int
		want   string
	}{
		{"123", -2, 2, "1.23"},
		{"0.05", 2, -2, "5"},
		{"0", 400, 0, "0"},
		{"1e308", 1, 308, "!"},
		{"1e-324", -1, -324, "!"},
	} {
		d := parse(t, c.in, p16)
		if got := d.Adjusted(); got != c.adj {
			t.Errorf("Adjusted(%s) = %d, want %d", c.in, got, c.adj)
		}
		s, ok := d.Shift(c.k)
		if got := str(s, ok, p16); got != c.want {
			t.Errorf("Shift(%s, %d) = %s, want %s", c.in, c.k, got, c.want)
		}
	}
}

func TestConvert(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{0.1, "0.1"},
		{-2.0, "-2"},
		{1e21, "1e+21"},
		{9007199254740993.0, "9007199254740992"},
		{math.Inf(1), "!"},
		{math.NaN(), "!"},
		{json.Number("1.50"), "1.5"},
		{json.Number("x"), "!"},
		{"1", "!"},
		{1, "!"},
	} {
		d, ok := decimal.FromValue(c.in, p16)
		if got := str(d, ok, p16); got != c.want {
			t.Errorf("FromValue(%v) = %s, want %s", c.in, got, c.want)
		}
	}
	n, _ := new(big.Int).SetString("123456789012345678", 10)
	if d, ok := decimal.FromBig(n, p16); str(d, ok, p16) != "1.234567890123457e+17" {
		t.Errorf("FromBig = %s", str(d, ok, p16))
	}
	if got := decimal.NewInt(-5).Abs().Neg().Value(p16); got != "-5" {
		t.Errorf("NewInt(-5).Abs().Neg() = %s", got)
	}
	if got := decimal.NewInt(5).Abs().Value(p16); got != "5" {
		t.Errorf("NewInt(5).Abs() = %s", got)
	}
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"0", true},
		{"-12", true},
		{"1.5", true},
		{"0.5", true},
		{"1234567890123456", true},
		{"-0", false},
		{"1.50", false},
		{"01", false},
		{".5", false},
		{"1.", false},
		{"1e3", false},
		{"12345678901234567", false},
		{"", false},
		{"-", false},
	} {
		if got := decimal.IsCanonical(c.in, p16); got != c.want {
			t.Errorf("IsCanonical(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
