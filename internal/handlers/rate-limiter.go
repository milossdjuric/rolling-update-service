package handlers

import "time"

type LeakyBucketRateLimiter struct {
	tokens chan struct{}
	ticker *time.Ticker
	quit   chan struct{}
}

func NewLeakyBucketRateLimiter(rate int, capacity int) *LeakyBucketRateLimiter {
	rateLimiter := &LeakyBucketRateLimiter{
		tokens: make(chan struct{}, capacity),
		ticker: time.NewTicker(time.Second / time.Duration(rate)),
		quit:   make(chan struct{}),
	}

	go func() {
		for {
			select {
			case <-rateLimiter.ticker.C:
				select {
				case rateLimiter.tokens <- struct{}{}:
				default:
					// if bucket full ignore
				}
			case <-rateLimiter.quit:
				return
			}
		}
	}()

	return rateLimiter
}

// checks if bucket has any tokens available
func (b *LeakyBucketRateLimiter) Allow() bool {
	select {
	case <-b.tokens:
		return true
	default:
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
