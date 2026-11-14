package engine

import "math"

// poissonTail returns P(X >= k) for X ~ Poisson(lambda).
func poissonTail(k int, lambda float64) float64 {
	switch {
	case k <= 0:
		return 1
	case lambda <= 0:
		return 0
	case float64(k) <= lambda:
		// The tail holds at least about half of the mass; callers only
		// compare against small probabilities.
		return 1
	}
	// Sum the terms from k upward. They fall off faster than a geometric
	// series with ratio lambda/(k+1) < 1.
	lg, _ := math.Lgamma(float64(k) + 1)
	term := math.Exp(-lambda + float64(k)*math.Log(lambda) - lg)
	sum := term
	for i := k + 1; i < k+400; i++ {
		term *= lambda / float64(i)
		sum += term
		if term < sum*1e-15 {
			break
		}
	}
	return math.Min(sum, 1)
}

// binomTail returns P(X >= k) for X ~ Binomial(n, p).
func binomTail(k, n int, p float64) float64 {
	switch {
	case k <= 0:
		return 1
	case k > n || p <= 0:
		return 0
	case p >= 1 || float64(k) <= float64(n)*p:
		// At or below the mean; callers only compare against small
		// probabilities.
		return 1
	}
	// Sum the terms from k upward; beyond the mean they fall off.
	lgN, _ := math.Lgamma(float64(n) + 1)
	lgK, _ := math.Lgamma(float64(k) + 1)
	lgNK, _ := math.Lgamma(float64(n-k) + 1)
	q := 1 - p
	term := math.Exp(lgN - lgK - lgNK + float64(k)*math.Log(p) + float64(n-k)*math.Log(q))
	sum := term
	for i := k; i < n; i++ {
		term *= float64(n-i) / float64(i+1) * p / q
		sum += term
		if term < sum*1e-15 {
			break
		}
	}
	return math.Min(sum, 1)
}

// alignChance bounds the probability that, with n items placed at random
// into bins, some bin holds k or more. It is the test behind every "these
// errors share a row" decision: among hundreds of errors scattered over a
// bank some column holds several of them by accident, and that is not a
// line.
func alignChance(k, n int, bins float64) float64 {
	if bins < 1 {
		bins = 1
	}
	return math.Min(1, bins*binomTail(k, n, 1/bins))
}

// crowdChance bounds the probability that chance puts k or more of the
// background items into one of the bins, when the background items fall
// into the bins at random. Without background the answer is zero: nothing
// else is there that the group could be an accident of.
func crowdChance(k, background int, bins float64) float64 {
	if bins < 1 {
		bins = 1
	}
	if background <= 0 {
		return 0
	}
	return math.Min(1, bins*poissonTail(k, float64(background)/bins))
}

// ceilPow2 returns the smallest power of two that is at least n.
func ceilPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}
