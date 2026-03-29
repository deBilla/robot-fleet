package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal operation
	StateOpen                  // Failing, reject requests immediately
	StateHalfOpen              // Testing if service recovered
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

// Breaker implements a simple circuit breaker pattern.
type Breaker struct {
	mu               sync.Mutex
	state            State
	failureCount     int
	successCount     int
	failureThreshold int
	successThreshold int
	timeout          time.Duration
	lastFailure      time.Time
}

// Config holds circuit breaker configuration.
type Config struct {
	FailureThreshold int           // failures before opening (default 5)
	SuccessThreshold int           // successes in half-open before closing (default 2)
	Timeout          time.Duration // how long to stay open before half-open (default 30s)
}

// New creates a circuit breaker with the given configuration.
func New(cfg Config) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Breaker{
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		timeout:          cfg.Timeout,
	}
}

// Execute runs the given function through the circuit breaker.
// If the circuit is open, it returns ErrCircuitOpen without calling fn.
func (b *Breaker) Execute(fn func() error) error {
	b.mu.Lock()
	switch b.state {
	case StateOpen:
		if time.Since(b.lastFailure) > b.timeout {
			b.state = StateHalfOpen
			b.successCount = 0
		} else {
			b.mu.Unlock()
			return ErrCircuitOpen
		}
	}
	b.mu.Unlock()

	err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.failureCount++
		b.lastFailure = time.Now()
		if b.state == StateHalfOpen || b.failureCount >= b.failureThreshold {
			b.state = StateOpen
		}
		return err
	}

	if b.state == StateHalfOpen {
		b.successCount++
		if b.successCount >= b.successThreshold {
			b.state = StateClosed
			b.failureCount = 0
		}
	} else {
		b.failureCount = 0
	}

	return nil
}

// State returns the current state of the circuit breaker.
func (b *Breaker) GetState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
