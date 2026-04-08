package timing

import (
	"math"
)

const (
	DefaultDownloadRate = 100 * 1024 * 1024 // 100 MB/s
	DefaultConvertRate  = 200 * 1024 * 1024 // 200 MB/s
)

type StageInfo struct {
	SizeBytes int64
	RateBPS   float64
}

type Estimator struct {
	downloadRate float64
	convertRate  float64
}

func NewEstimator(downloadRate, convertRate float64) *Estimator {
	if downloadRate <= 0 {
		downloadRate = DefaultDownloadRate
	}
	if convertRate <= 0 {
		convertRate = DefaultConvertRate
	}
	return &Estimator{
		downloadRate: downloadRate,
		convertRate:  convertRate,
	}
}

func (e *Estimator) EstimateWeights(downloadSize, convertSize int64) (float64, float64) {
	downloadTime := estimateDuration(downloadSize, e.downloadRate)
	convertTime := estimateDuration(convertSize, e.convertRate)

	total := downloadTime + convertTime
	if total <= 0 {
		return 0.5, 0.5
	}

	return downloadTime / total, convertTime / total
}

func (e *Estimator) EstimateStageProgress(
	stage string,
	stagePercent float64,
	downloadSize, convertSize int64,
	downloadWeight, convertWeight float64,
) float64 {
	switch stage {
	case "downloading":
		return stagePercent * downloadWeight
	case "converting":
		return downloadWeight*100 + stagePercent*convertWeight
	default:
		return stagePercent
	}
}

func estimateDuration(sizeBytes int64, rateBPS float64) float64 {
	if sizeBytes <= 0 || rateBPS <= 0 {
		return 0
	}
	return float64(sizeBytes) / rateBPS
}

type MovingAverage struct {
	values  []float64
	maxSize int
	index   int
	count   int
	sum     float64
}

func NewMovingAverage(maxSize int) *MovingAverage {
	if maxSize <= 0 {
		maxSize = 20
	}
	return &MovingAverage{
		values:  make([]float64, maxSize),
		maxSize: maxSize,
	}
}

func (m *MovingAverage) Add(value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}

	m.sum -= m.values[m.index]
	m.values[m.index] = value
	m.sum += value
	m.index = (m.index + 1) % m.maxSize
	if m.count < m.maxSize {
		m.count++
	}
}

func (m *MovingAverage) Average() float64 {
	if m.count == 0 {
		return 0
	}
	return m.sum / float64(m.count)
}

func (m *MovingAverage) Count() int {
	return m.count
}
