package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithRetry_Success_FirstAttempt(t *testing.T) {
	cfg := Config{
		MaxAttempts: 3,
		Delays:      []time.Duration{10 * time.Millisecond},
	}

	attempts := 0
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, attempts)
}

func TestWithRetry_Success_AfterRetries(t *testing.T) {
	cfg := Config{
		MaxAttempts: 3,
		Delays:      []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
	}

	attempts := 0
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("transient error")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestWithRetry_ExhaustedAttempts(t *testing.T) {
	cfg := Config{
		MaxAttempts: 3,
		Delays:      []time.Duration{5 * time.Millisecond, 5 * time.Millisecond},
	}

	attempts := 0
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return errors.New("persistent error")
	})

	assert.Error(t, err)
	assert.Equal(t, 3, attempts)
	assert.Contains(t, err.Error(), "failed after 3 attempts")
	assert.Contains(t, err.Error(), "persistent error")
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	cfg := Config{
		MaxAttempts: 10,
		Delays:      []time.Duration{50 * time.Millisecond, 50 * time.Millisecond},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	startTime := time.Now()

	// Cancel context after first attempt
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := WithRetry(ctx, cfg, func() error {
		attempts++
		return errors.New("transient error")
	})

	elapsed := time.Since(startTime)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "retry cancelled")
	assert.Less(t, elapsed, 200*time.Millisecond) // Should cancel quickly, not wait for full retries
}

func TestWithRetry_ContextDeadline(t *testing.T) {
	cfg := Config{
		MaxAttempts: 10,
		Delays:      []time.Duration{50 * time.Millisecond, 50 * time.Millisecond},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	attempts := 0
	err := WithRetry(ctx, cfg, func() error {
		attempts++
		return errors.New("transient error")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "retry cancelled")
	assert.Equal(t, 1, attempts) // Should fail during delay
}

func TestWithRetry_DelayArray_ReuseLastDelay(t *testing.T) {
	cfg := Config{
		MaxAttempts: 5,
		Delays:      []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}, // Only 2 delays
	}

	attempts := 0
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return errors.New("error")
	})

	assert.Error(t, err)
	assert.Equal(t, 5, attempts)
}

func TestWithRetry_EmptyMaxAttempts_DefaultsToOne(t *testing.T) {
	cfg := Config{
		MaxAttempts: 0, // Invalid
		Delays:      []time.Duration{},
	}

	attempts := 0
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return errors.New("error")
	})

	assert.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestWithRetry_MeasureBackoffTiming(t *testing.T) {
	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	cfg := Config{
		MaxAttempts: 3,
		Delays:      delays,
	}

	attempts := 0
	startTime := time.Now()

	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return errors.New("error")
	})

	elapsed := time.Since(startTime)

	// Expected: attempt 1 (instant) -> delay 10ms -> attempt 2 -> delay 20ms -> attempt 3
	// Total: ~30ms minimum (plus execution time)
	assert.Error(t, err)
	assert.Equal(t, 3, attempts)
	assert.GreaterOrEqual(t, elapsed, 25*time.Millisecond) // Account for timing variance
}

func TestWithRetry_SingleAttempt(t *testing.T) {
	cfg := Config{
		MaxAttempts: 1,
		Delays:      []time.Duration{},
	}

	attempts := 0
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return errors.New("error")
	})

	assert.Error(t, err)
	assert.Equal(t, 1, attempts)
}
