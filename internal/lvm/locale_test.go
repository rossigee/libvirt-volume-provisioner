package lvm

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeNumericOutput covers the output shapes LVM tools produce that
// fmt.Sscanf's %d cannot read unaided.
//
// The fullwidth cases are the ones that actually broke volume creation: under a
// th_TH locale vgs emits fullwidth digits, and %d only accepts ASCII, so
// parsing failed with "expected integer" on every attempt.
func TestNormalizeNumericOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		format string
		wantA  int
		wantB  int // unused when format has a single verb
	}{
		{
			name:   "C locale plain",
			input:  "  1   102399\n",
			format: "%d %d",
			wantA:  1,
			wantB:  102399,
		},
		{
			name:   "th_TH fullwidth digits",
			input:  "  １   １０２３９９\n",
			format: "%d %d",
			wantA:  1,
			wantB:  102399,
		},
		{
			name:   "wrapped across lines at 80 columns",
			input:  "  1\n  102399\n",
			format: "%d %d",
			wantA:  1,
			wantB:  102399,
		},
		{
			name:   "leading blank line",
			input:  "\n  1   102399\n",
			format: "%d %d",
			wantA:  1,
			wantB:  102399,
		},
		{
			name:   "single value free_count",
			input:  "  ５１２３\n",
			format: "%d",
			wantA:  5123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := normalizeNumericOutput(tt.input)

			var a, b int
			var n int
			var err error
			if tt.format == "%d" {
				n, err = fmt.Sscanf(normalized, tt.format, &a)
			} else {
				n, err = fmt.Sscanf(normalized, tt.format, &a, &b)
			}
			require.NoError(t, err, "normalized output %q must be parseable", normalized)
			assert.Equal(t, tt.wantA, a)
			if tt.format != "%d" {
				assert.Equal(t, 2, n)
				assert.Equal(t, tt.wantB, b)
			}
		})
	}
}

// TestNormalizeNumericOutputPreservesFieldOrder guards against the
// normalisation dropping or reordering fields, which would silently produce a
// wrong extent count rather than a parse error.
func TestNormalizeNumericOutputPreservesFieldOrder(t *testing.T) {
	normalized := normalizeNumericOutput(" ２  ３  ４\n")
	assert.Equal(t, "2 3 4", normalized)

	var a, b, c int
	n, err := fmt.Sscanf(normalized, "%d %d %d", &a, &b, &c)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, 2, a)
	assert.Equal(t, 3, b)
	assert.Equal(t, 4, c)
}

// TestLvmCmdForcesCLocale verifies the child process always runs under the C
// locale. Without this, LVM output is localised and the numeric parse above
// cannot be relied on regardless of how defensive the parsing is.
func TestLvmCmdForcesCLocale(t *testing.T) {
	cmd := lvmCmd(t.Context(), "vgs", "--noheadings", "-o", "free_count", "data")

	joined := ""
	for _, e := range cmd.Env {
		joined += e + " "
	}
	assert.Contains(t, joined, "LC_ALL=C")
	assert.Contains(t, joined, "LANG=C")
}
