package calc

import (
	"math/big"
	"strconv"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func decimalAmount(value float64) *big.Rat {
	amount, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	if !ok {
		panic("calc: non-finite share after clamping")
	}
	return amount
}

func addAmount(values map[int64]*big.Rat, id int64, amount *big.Rat) {
	if values[id] == nil {
		values[id] = new(big.Rat)
	}
	values[id].Add(values[id], amount)
}

func monthlyAmount(b store.Booking, amount *big.Rat) *big.Rat {
	out := new(big.Rat).Set(amount)
	switch b.Frequency {
	case store.FreqWeekly:
		out.Mul(out, big.NewRat(52, 12))
	case store.FreqQuarterly:
		out.Quo(out, big.NewRat(3, 1))
	case store.FreqYearly:
		out.Quo(out, big.NewRat(12, 1))
	}
	if b.Frequency.Recurring() && b.Interval > 1 {
		out.Quo(out, big.NewRat(int64(b.Interval), 1))
	}
	return out
}

func roundAmount(amount *big.Rat) int64 {
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(amount.Num(), amount.Denom(), remainder)
	if remainder.Abs(remainder).Lsh(remainder, 1).Cmp(amount.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(int64(amount.Sign())))
	}
	return whole.Int64()
}
