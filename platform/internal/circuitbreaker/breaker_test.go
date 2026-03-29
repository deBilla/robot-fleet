package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestBreaker_ClosedState(t *testing.T) {
	cb := New(Config{FailureThreshold: 3, Timeout: 100 * time.Millisecond})

	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if cb.GetState() != StateClosed {
		t.Errorf("expected closed state")
	}
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	cb := New(Config{FailureThreshold: 3, Timeout: 100 * time.Millisecond})

	for i := range 3 {
		_ = cb.Execute(func() error { return errTest })
		_ = i
	}

	if cb.GetState() != StateOpen {
		t.Errorf("expected open state after %d failures", 3)
	}

	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestBreaker_HalfOpenRecovery(t *testing.T) {
	cb := New(Config{FailureThreshold: 2, SuccessThreshold: 1, Timeout: 50 * time.Millisecond})

	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	if cb.GetState() != StateOpen {
		t.Fatalf("expected open state")
	}

	time.Sleep(60 * time.Millisecond)

	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected success in half-open, got %v", err)
	}

	if cb.GetState() != StateClosed {
		t.Errorf("expected closed state after recovery")
	}
}

func TestBreaker_HalfOpenFailsReopen(t *testing.T) {
	cb := New(Config{FailureThreshold: 2, Timeout: 50 * time.Millisecond})

	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	time.Sleep(60 * time.Millisecond)

	err := cb.Execute(func() error { return errTest })
	if err != errTest {
		t.Fatalf("expected test error, got %v", err)
	}

	if cb.GetState() != StateOpen {
		t.Errorf("expected re-open after half-open failure")
	}
}
