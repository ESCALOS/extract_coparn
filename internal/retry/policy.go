package retry

import (
	"math/rand"
	"time"
)

func NextDelay(nextAttempt int, jitterPct int) time.Duration {
	var base time.Duration
	switch {
	case nextAttempt <= 3:
		base = time.Minute
	case nextAttempt <= 6:
		base = 5 * time.Minute
	default:
		base = 15 * time.Minute
	}

	if jitterPct <= 0 {
		return base
	}
	jitterRange := float64(base) * float64(jitterPct) / 100.0
	delta := (rand.Float64()*2 - 1) * jitterRange
	return time.Duration(float64(base) + delta)
}
