package sim

type rng uint64

// SplitMix64.
func (r *rng) next() uint64 {
	*r += 0x9E3779B97F4A7C15
	z := uint64(*r)
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *rng) float64() float64 {
	return float64(r.next()>>11) / float64(1<<53)
}

func (r *rng) intN(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}
