// Package retry provides configurable retry logic with exponential backoff for transient failures.
package retry

import (
	"context"
	"fmt"
	"time"
)

// Config holds retry configuration
type Config struct {
	MaxAttempts int
	Delays      []time.Duration
}

// WithRetry executes fn with exponential backoff retry logic.
// It will attempt the function up to MaxAttempts times, with delays between attempts.
// If MaxAttempts is exceeded, the last error is returned wrapped with context.
func WithRetry(ctx context.Context, cfg Config, fn func() error) error {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// Apply delay before retry (not before first attempt)
		if attempt > 0 {
			// Get delay for this attempt
			delayIndex := attempt - 1
			if delayIndex >= len(cfg.Delays) {
				delayIndex = len(cfg.Delays) - 1 // Use last delay if we run out
			}
			delay := cfg.Delays[delayIndex]

			// Wait for delay or context cancellation
			select {
			case <-time.After(delay):
				// Delay complete, continue to next attempt
			case <-ctx.Done():
				// Context cancelled
				return fmt.Errorf("retry cancelled: %w", ctx.Err())
			}
		}

		// Try the operation
		err := fn()
		if err == nil {
			return nil // Success!
		}
		lastErr = err
	}

	return fmt.Errorf("failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
}
