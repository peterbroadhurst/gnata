package evaluator

import (
	"cmp"
	"encoding/json"
	"strings"

	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/parser"
)

// evalNumber returns a number literal. With decimal precision enabled it is a
// json.Number, unless out of range. A nonzero literal in exponent form is kept
// as written, so $string(1e21) gives "1e+21" as in JavaScript; other literals
// are rounded and canonicalised (1.50 becomes 1.5).
func evalNumber(node *parser.Node, env *Environment) any {
	prec := env.DecimalPrecision()
	if prec == 0 {
		return node.NumVal
	}
	if decimal.IsCanonical(node.Value, prec) {
		return json.Number(node.Value)
	}
	if d, ok := decimal.Parse(node.Value, prec); ok {
		if d.Sign() != 0 && strings.ContainsAny(node.Value, "eE") {
			return json.Number(node.Value)
		}
		return d.Value(prec)
	}
	return node.NumVal
}

// DecimalCmp compares two numbers in decimal. ok is false when either is not a
// number in range, in which case callers fall back to float64 comparison.
func DecimalCmp(a, b any, prec int) (int, bool) {
	if af, ok := a.(float64); ok {
		if bf, ok := b.(float64); ok {
			return cmp.Compare(af, bf), true
		}
	}
	x, ok := decimal.FromValue(a, prec)
	if !ok {
		return 0, false
	}
	y, ok := decimal.FromValue(b, prec)
	if !ok {
		return 0, false
	}
	return x.Cmp(y), true
}

// decimalCompare applies a comparison operator in decimal when either operand
// is a json.Number. ok is false otherwise.
func decimalCompare(left, right any, op string, prec int) (result, ok bool) {
	_, ln := left.(json.Number)
	_, rn := right.(json.Number)
	if !ln && !rn {
		return false, false
	}
	c, ok := DecimalCmp(left, right, prec)
	if !ok {
		return false, false
	}
	switch op {
	case "=":
		return c == 0, true
	case "!=":
		return c != 0, true
	case "<":
		return c < 0, true
	case "<=":
		return c <= 0, true
	case ">":
		return c > 0, true
	case ">=":
		return c >= 0, true
	}
	return false, false
}

// DecimalArith applies an arithmetic operator in decimal, rounding half to even
// to prec significant digits. ok is false when an operand or the result is out
// of range, the divisor is zero, or the operation has no decimal form (e.g. a
// fractional exponent), in which case callers fall back to float64 arithmetic,
// which also reports any error.
func DecimalArith(l, r any, op string, prec int) (any, bool) {
	x, ok := decimal.FromValue(l, prec)
	if !ok {
		return nil, false
	}
	y, ok := decimal.FromValue(r, prec)
	if !ok {
		return nil, false
	}
	var z decimal.Decimal
	switch op {
	case "+":
		z, ok = x.Add(y, prec)
	case "-":
		z, ok = x.Sub(y, prec)
	case "*":
		z, ok = x.Mul(y, prec)
	case "/":
		z, ok = x.Quo(y, prec)
	case "%":
		z, ok = x.Rem(y, prec)
	case "**":
		var n int64
		if n, ok = y.Int64(); ok {
			z, ok = x.Pow(n, prec)
		}
	default:
		ok = false
	}
	if !ok {
		return nil, false
	}
	return z.Value(prec), true
}
