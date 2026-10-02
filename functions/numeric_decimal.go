package functions

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/evaluator"
)

// maxRoundPlaces bounds $round's precision argument; beyond it every in-range
// value is unchanged or rounds to zero.
const maxRoundPlaces = 10000

// decimalFn is a decimal variant of a numeric builtin. ok is false when it
// declines (e.g. an operand or result out of range, or an invalid argument),
// in which case the float64 builtin runs and reports any error.
type decimalFn func(args []any, focus any, prec int) (result any, ok bool)

// withDecimal runs dec when decimal precision is enabled, otherwise fn.
func withDecimal(fn func([]any, any) (any, error), dec decimalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if prec := env.DecimalPrecision(); prec > 0 {
			if res, ok := dec(args, focus, prec); ok {
				return res, nil
			}
		}
		return fn(args, focus)
	}
}

func decNumber(args []any, focus any, prec int) (any, bool) {
	arg := focus
	switch len(args) {
	case 0:
	case 1:
		arg = args[0]
	default:
		return nil, false
	}
	switch v := arg.(type) {
	case json.Number:
		return decNumberString(v.String(), prec)
	case string:
		return decNumberString(strings.TrimSpace(v), prec)
	}
	return nil, false
}

func decNumberString(s string, prec int) (any, bool) {
	if len(s) > 2 && s[0] == '0' {
		var base, digitBits int
		switch s[1] {
		case 'x', 'X':
			base, digitBits = 16, 4
		case 'b', 'B':
			base, digitBits = 2, 1
		case 'o', 'O':
			base, digitBits = 8, 3
		}
		if base != 0 {
			// Bound the digits before parsing so a long string cannot become a huge integer.
			if len(s[2:])*digitBits > decimal.MaxIntegerBits {
				return nil, false
			}
			n, ok := new(big.Int).SetString(s[2:], base)
			if !ok || n.Sign() < 0 {
				return nil, false
			}
			d, ok := decimal.FromBig(n, prec)
			return d.Value(prec), ok
		}
	}
	d, ok := decimal.Parse(s, prec)
	return d.Value(prec), ok
}

// decArg returns the first argument as a Decimal when it is a json.Number;
// float64 arguments are left to the float64 builtin, which is already exact for
// $abs, $floor and $ceil and matches jsonata-js for $round.
func decArg(args []any, prec int) (decimal.Decimal, bool) {
	if len(args) == 0 {
		return decimal.Decimal{}, false
	}
	if _, ok := args[0].(json.Number); !ok {
		return decimal.Decimal{}, false
	}
	return decimal.FromValue(args[0], prec)
}

func decAbs(args []any, _ any, prec int) (any, bool) {
	d, ok := decArg(args, prec)
	return d.Abs().Value(prec), ok
}

func decFloor(args []any, _ any, prec int) (any, bool) {
	d, ok := decArg(args, prec)
	return d.Floor(prec).Value(prec), ok
}

func decCeil(args []any, _ any, prec int) (any, bool) {
	d, ok := decArg(args, prec)
	return d.Ceil(prec).Value(prec), ok
}

func decRound(args []any, _ any, prec int) (any, bool) {
	d, ok := decArg(args, prec)
	if !ok {
		return nil, false
	}
	places := 0
	if len(args) >= 2 && args[1] != nil {
		pf, ok := evaluator.ToFloat64(args[1])
		if !ok {
			return nil, false
		}
		places = int(max(min(pf, maxRoundPlaces), -maxRoundPlaces))
	}
	if d, ok = d.RoundPlaces(places, prec); !ok {
		return nil, false
	}
	return d.Value(prec), true
}

// decTotal sums the single array argument, declining when any element or the
// running total is out of range.
func decTotal(args []any, prec int) (sum decimal.Decimal, n int, ok bool) {
	if len(args) != 1 {
		return sum, 0, false
	}
	arr := toNumberArray(args[0])
	if len(arr) == 0 {
		return sum, 0, false
	}
	for _, v := range arr {
		d, ok := decimal.FromValue(v, prec)
		if !ok {
			return sum, 0, false
		}
		if sum, ok = sum.Add(d, prec); !ok {
			return sum, 0, false
		}
	}
	return sum, len(arr), true
}

func decSum(args []any, _ any, prec int) (any, bool) {
	sum, _, ok := decTotal(args, prec)
	return sum.Value(prec), ok
}

func decAverage(args []any, _ any, prec int) (any, bool) {
	sum, n, ok := decTotal(args, prec)
	if !ok {
		return nil, false
	}
	avg, ok := sum.Quo(decimal.NewInt(int64(n)), prec)
	return avg.Value(prec), ok
}

// decExtreme returns the largest (want = +1) or smallest (want = -1) element.
func decExtreme(args []any, prec, want int) (any, bool) {
	if len(args) != 1 {
		return nil, false
	}
	arr := toNumberArray(args[0])
	if len(arr) == 0 {
		return nil, false
	}
	best := arr[0]
	for _, v := range arr {
		c, ok := evaluator.DecimalCmp(v, best, prec)
		if !ok {
			return nil, false
		}
		if c == want {
			best = v
		}
	}
	return best, true
}

func decMax(args []any, _ any, prec int) (any, bool) { return decExtreme(args, prec, 1) }

func decMin(args []any, _ any, prec int) (any, bool) { return decExtreme(args, prec, -1) }
