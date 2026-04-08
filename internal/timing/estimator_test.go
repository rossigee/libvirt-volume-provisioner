package timing

import (
	"math"
	"testing"
)

func TestNewEstimator_Defaults(t *testing.T) {
	e := NewEstimator(0, 0)
	if e.downloadRate != DefaultDownloadRate {
		t.Errorf("expected default download rate %v, got %v", DefaultDownloadRate, e.downloadRate)
	}
	if e.convertRate != DefaultConvertRate {
		t.Errorf("expected default convert rate %v, got %v", DefaultConvertRate, e.convertRate)
	}
}

func TestNewEstimator_CustomRates(t *testing.T) {
	e := NewEstimator(50*1024*1024, 150*1024*1024)
	if e.downloadRate != 50*1024*1024 {
		t.Errorf("expected 50 MB/s, got %v", e.downloadRate)
	}
	if e.convertRate != 150*1024*1024 {
		t.Errorf("expected 150 MB/s, got %v", e.convertRate)
	}
}

func TestNewEstimator_NegativeRates(t *testing.T) {
	e := NewEstimator(-100, -200)
	if e.downloadRate != DefaultDownloadRate {
		t.Errorf("negative rate should use default, got %v", e.downloadRate)
	}
	if e.convertRate != DefaultConvertRate {
		t.Errorf("negative rate should use default, got %v", e.convertRate)
	}
}

func TestEstimateWeights_EqualSizesEqualRates(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)
	dl, cv := e.EstimateWeights(1000*1024*1024, 1000*1024*1024)

	if dl != 0.5 {
		t.Errorf("expected download weight 0.5, got %v", dl)
	}
	if cv != 0.5 {
		t.Errorf("expected convert weight 0.5, got %v", cv)
	}
}

func TestEstimateWeights_DifferentRates(t *testing.T) {
	e := NewEstimator(100*1024*1024, 200*1024*1024)             // download 2x slower
	dl, cv := e.EstimateWeights(1000*1024*1024, 1000*1024*1024) // same size

	if dl <= cv {
		t.Errorf("download should have higher weight (slower): dl=%v, cv=%v", dl, cv)
	}
	if math.Abs((dl+cv)-1.0) > 0.001 {
		t.Errorf("weights should sum to 1: %v", dl+cv)
	}
}

func TestEstimateWeights_ZeroSizes(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)
	dl, cv := e.EstimateWeights(0, 0)

	if dl != 0.5 {
		t.Errorf("expected 0.5 for zero sizes, got %v", dl)
	}
	if cv != 0.5 {
		t.Errorf("expected 0.5 for zero sizes, got %v", cv)
	}
}

func TestEstimateWeights_OneZeroSize(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)
	dl, cv := e.EstimateWeights(1000*1024*1024, 0)

	if dl != 1.0 {
		t.Errorf("expected download weight 1.0 when convert size is 0, got %v", dl)
	}
	if cv != 0.0 {
		t.Errorf("expected convert weight 0.0 when convert size is 0, got %v", cv)
	}
}

func TestEstimateStageProgress_Downloading(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)

	result := e.EstimateStageProgress("downloading", 50, 1000, 1000, 0.6, 0.4)
	expected := 50 * 0.6 // 30%
	if math.Abs(result-expected) > 0.001 {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestEstimateStageProgress_Converting(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)

	result := e.EstimateStageProgress("converting", 50, 1000, 1000, 0.6, 0.4)
	expected := 60 + 50*0.4 // 80%
	if math.Abs(result-expected) > 0.001 {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestEstimateStageProgress_ConvertingStart(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)

	result := e.EstimateStageProgress("converting", 0, 1000, 1000, 0.6, 0.4)
	if math.Abs(result-60.0) > 0.001 {
		t.Errorf("expected 60%% (downloadWeight*100), got %f", result)
	}
}

func TestEstimateStageProgress_ConvertingComplete(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)

	result := e.EstimateStageProgress("converting", 100, 1000, 1000, 0.6, 0.4)
	if math.Abs(result-100.0) > 0.001 {
		t.Errorf("expected 100%%, got %f", result)
	}
}

func TestEstimateStageProgress_OtherStage(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)

	result := e.EstimateStageProgress("initializing", 75, 1000, 1000, 0.6, 0.4)
	if result != 75 {
		t.Errorf("expected 75 for non-mapped stage, got %f", result)
	}
}

func TestEstimateStageProgress_CacheHit(t *testing.T) {
	e := NewEstimator(100*1024*1024, 100*1024*1024)

	// With cache hit, downloadWeight=0, convertWeight=1
	result := e.EstimateStageProgress("converting", 50, 0, 1000, 0, 1)
	if math.Abs(result-50.0) > 0.001 {
		t.Errorf("expected 50%% for cache hit, got %f", result)
	}
}

func TestEstimateDuration(t *testing.T) {
	tests := []struct {
		name      string
		sizeBytes int64
		rateBPS   float64
		expected  float64
	}{
		{"1GB at 100MB/s", 1024 * 1024 * 1024, 100 * 1024 * 1024, 10.24}, // 1GiB / 100MiB/s
		{"100MB at 200MB/s", 100 * 1024 * 1024, 200 * 1024 * 1024, 0.5},
		{"zero size", 0, 100 * 1024 * 1024, 0},
		{"zero rate", 1024 * 1024 * 1024, 0, 0},
		{"negative size", -100, 100 * 1024 * 1024, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := estimateDuration(tt.sizeBytes, tt.rateBPS)
			if math.Abs(result-tt.expected) > 0.001 {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMovingAverage(t *testing.T) {
	m := NewMovingAverage(3)

	m.Add(10)
	if m.Average() != 10 {
		t.Errorf("expected 10, got %v", m.Average())
	}
	if m.Count() != 1 {
		t.Errorf("expected count 1, got %v", m.Count())
	}

	m.Add(20)
	if m.Average() != 15 {
		t.Errorf("expected 15, got %v", m.Average())
	}

	m.Add(30)
	if m.Average() != 20 {
		t.Errorf("expected 20, got %v", m.Average())
	}

	// Adding more values should evict oldest
	m.Add(40)
	if m.Average() != 30 {
		t.Errorf("expected 30 (20+30+40)/3, got %v", m.Average())
	}
	if m.Count() != 3 {
		t.Errorf("expected count still 3, got %v", m.Count())
	}
}

func TestMovingAverage_Empty(t *testing.T) {
	m := NewMovingAverage(5)
	if m.Average() != 0 {
		t.Errorf("expected 0 for empty, got %v", m.Average())
	}
	if m.Count() != 0 {
		t.Errorf("expected count 0, got %v", m.Count())
	}
}

func TestMovingAverage_InvalidValues(t *testing.T) {
	m := NewMovingAverage(5)

	m.Add(math.NaN())
	m.Add(math.Inf(1))
	m.Add(math.Inf(-1))
	m.Add(10)

	if m.Average() != 10 {
		t.Errorf("expected 10, got %v", m.Average())
	}
}

func TestMovingAverage_ZeroMaxSize(t *testing.T) {
	m := NewMovingAverage(0)
	m.Add(10)
	if m.Count() != 1 {
		t.Errorf("expected default size 1, got %v", m.Count())
	}
}

func TestEstimateWeights_LargeSizeDifference(t *testing.T) {
	e := NewEstimator(100*1024*1024, 200*1024*1024)
	// 1GB download at 100MB/s = 10s
	// 10GB convert at 200MB/s = 50s
	dl, cv := e.EstimateWeights(1024*1024*1024, 10*1024*1024*1024)

	// download ~16.7%, convert ~83.3%
	if dl > 0.3 || dl < 0.1 {
		t.Errorf("download weight seems wrong: %f", dl)
	}
	if cv > 0.9 || cv < 0.7 {
		t.Errorf("convert weight seems wrong: %f", cv)
	}
}
