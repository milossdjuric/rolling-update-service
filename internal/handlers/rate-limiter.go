package handlers

import "time"

type LeakyBucketRateLimiter struct {
	tokens chan struct{}
	ticker *time.Ticker
	quit   chan struct{}
}

// NewLeakyBucketRateLimiter creates a leaky bucket rate limiter.
func NewLeakyBucketRateLimiter(rate int, capacity int) *LeakyBucketRateLimiter {
	rateLimiter := &LeakyBucketRateLimiter{
		tokens: make(chan struct{}, capacity),
		ticker: time.NewTicker(time.Second / time.Duration(rate)),
		quit:   make(chan struct{}),
	}

	// Goroutine to refill the bucket
	go func() {
		for {
			select {
			case <-rateLimiter.ticker.C:
				select {
				case rateLimiter.tokens <- struct{}{}:
					// Token added successfully
				default:
					// Bucket full, skip adding
				}
			case <-rateLimiter.quit:
				return
			}
		}
	}()

	return rateLimiter
}

// Allow removes a token from the bucket if available, or returns false.
func (b *LeakyBucketRateLimiter) Allow() bool {
	select {
	case <-b.tokens: // Consume a token
		return true
	default: // No tokens available
		return false
	}
}

func (b *LeakyBucketRateLimiter) Stop() {
	b.ticker.Stop()
	close(b.quit)
}

func (u *UpdateServiceGrpcHandler) StopRateLimiter() {
	u.rateLimiter.Stop()
}
