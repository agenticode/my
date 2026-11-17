package explorer

// Numbers as JavaScript has them, so that every carried function computes
// what its reference computes.

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Num is a number as JavaScript has it: a double. JSON writes it the way
// JSON.stringify does: whole numbers plain, -0 as 0, NaN and the
// infinities as null.
type Num float64

// MarshalJSON writes n as JSON.stringify writes a number.
func (n Num) MarshalJSON() ([]byte, error) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return []byte(NumString(f)), nil
}

// Int returns n as an int when it is a whole number within ±2^31; ok is
// false otherwise (NaN, an infinity, a fraction, or out of range).
func (n Num) Int() (int, bool) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < -(1<<31) || f > (1<<31)-1 {
		return 0, false
	}
	return int(f), true
}

// NumString is String(x) of JavaScript (Number::toString with radix 10).
func NumString(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case x == 0:
		return "0"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x < 0:
		return "-" + NumString(-x)
	}
	// Shortest digits that read back as x, as d.ddde±x.
	e := strconv.FormatFloat(x, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	ex, _ := strconv.Atoi(exp)
	k := len(digits)
	n := ex + 1 // x = 0.digits × 10^n
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	sign := "+"
	if n-1 < 0 {
		sign = "-"
	}
	en := n - 1
	if en < 0 {
		en = -en
	}
	if k == 1 {
		return digits + "e" + sign + strconv.Itoa(en)
	}
	return digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(en)
}

// ParseInt is parseInt(s, 10): white space, a sign, then the digits that
// follow. Without a digit the result is NaN.
func ParseInt(s string) Num {
	s = TrimStart(s)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return Num(math.NaN())
	}
	v, _ := strconv.ParseFloat(s[:end], 64) // correctly rounded, as V8 does
	if neg {
		v = -v
	}
	return Num(v)
}

// ParseFloat is parseFloat(s): the longest decimal literal at the start
// after white space, "Infinity" included. Without one the result is NaN.
func ParseFloat(s string) Num {
	s = TrimStart(s)
	i := 0
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	if strings.HasPrefix(s[i:], "Infinity") {
		if neg {
			return Num(math.Inf(-1))
		}
		return Num(math.Inf(1))
	}
	start := i
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		j := i + 1
		frac := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			frac++
		}
		if digits > 0 || frac > 0 {
			i = j
			digits += frac
		}
	}
	if digits == 0 {
		return Num(math.NaN())
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	lit := s[start:i]
	v, err := strconv.ParseFloat(lit, 64)
	if err != nil && !math.IsInf(v, 0) && v != 0 {
		return Num(math.NaN())
	}
	if neg {
		v = -v
	}
	return Num(v)
}

// ToFixed is x.toFixed(d): the exact decimal value of the double, a tie
// going to the larger digit. From 1e21 on it is String(x).
func ToFixed(x float64, d int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	if x >= 1e21 || math.IsInf(x, 0) {
		return sign + NumString(x)
	}
	r := new(big.Rat).SetFloat64(x)
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Quo(r.Num(), r.Denom()) // floor: both are positive
	s := n.String()
	if d == 0 {
		return sign + s
	}
	if len(s) <= d {
		s = strings.Repeat("0", d-len(s)+1) + s
	}
	return sign + s[:len(s)-d] + "." + s[len(s)-d:]
}

// Round is Math.round: the nearest whole number, a tie going towards
// +infinity, below zero too; -0 for -0.5 <= x < 0.
func Round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && x < 0 {
		return math.Copysign(0, -1)
	}
	return r
}

// Mod is the % of JavaScript: the remainder has the sign of the dividend.
func Mod(x, y float64) float64 { return math.Mod(x, y) }

// Or is x || alt for a number: alt when x is 0, -0 or NaN.
func Or(x, alt Num) Num {
	if x == 0 || math.IsNaN(float64(x)) {
		return alt
	}
	return x
}

// Log is Math.log as node computes it on the machine the reference was run
// on: the fdlibm algorithm of V8, built by clang, which fuses a product
// into the sum or difference it stands in (-ffp-contract=on). The fused
// steps are written as math.FMA, which rounds once on every platform, and
// every other product is rounded on its own, so that the Go compiler fuses
// nothing more. A node built for x86-64 fuses nothing and differs in the
// last bit for some inputs.
func Log(x float64) float64 {
	const (
		ln2Hi = 6.93147180369123816490e-01
		ln2Lo = 1.90821492927058770002e-10
		two54 = 1.80143985094819840000e+16
		lg1   = 6.666666666666735130e-01
		lg2   = 3.999999999940941908e-01
		lg3   = 2.857142874366239149e-01
		lg4   = 2.222219843214978396e-01
		lg5   = 1.818357216161805012e-01
		lg6   = 1.531383769920937332e-01
		lg7   = 1.479819860511658591e-01
	)
	fma := math.FMA
	bits := math.Float64bits(x)
	hx := int32(bits >> 32)
	lx := uint32(bits)
	k := int32(0)
	if hx < 0x00100000 {
		if (hx&0x7fffffff)|int32(lx) == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x = float64(x * two54)
		hx = int32(math.Float64bits(x) >> 32)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = math.Float64frombits(uint64(uint32(hx|(i^0x3ff00000)))<<32 | uint64(uint32(math.Float64bits(x))))
	k += i >> 20
	f := x - 1.0
	dk := float64(k)
	if (0x000fffff & (2 + hx)) < 3 {
		if f == 0 {
			if k == 0 {
				return 0
			}
			return fma(dk, ln2Hi, float64(dk*ln2Lo))
		}
		r := float64(float64(f*f) * fma(-0.33333333333333333, f, 0.5))
		if k == 0 {
			return f - r
		}
		return fma(dk, ln2Hi, -(fma(-dk, ln2Lo, r) - f))
	}
	s := float64(f / float64(2.0+f))
	z := float64(s * s)
	i = hx - 0x6147a
	w := float64(z * z)
	j := 0x6b851 - hx
	t1 := float64(w * fma(w, fma(w, lg6, lg4), lg2))
	t2 := float64(z * fma(w, fma(w, fma(w, lg7, lg5), lg3), lg1))
	i |= j
	r := t2 + t1
	if i > 0 {
		hfsq := float64(float64(0.5*f) * f)
		if k == 0 {
			return f - fma(-s, hfsq+r, hfsq)
		}
		return fma(dk, ln2Hi, -((hfsq - fma(s, hfsq+r, float64(dk*ln2Lo))) - f))
	}
	if k == 0 {
		return fma(-s, f-r, f)
	}
	return fma(dk, ln2Hi, -(fma(s, f-r, -float64(dk*ln2Lo)) - f))
}

// pow1024 is Math.pow(1024, n) for a whole n; NaN stays NaN.
func pow1024(n float64) float64 {
	if math.IsNaN(n) {
		return math.NaN()
	}
	if math.IsInf(n, 1) {
		return math.Inf(1)
	}
	if math.IsInf(n, -1) {
		return 0
	}
	return math.Ldexp(1, int(10*n))
}
