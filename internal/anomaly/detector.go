// Package anomaly 用 EWMA + Z-Score 做在线异常检测。
// 相比固定阈值，它能自适应指标的基线漂移，是"故障预测"里最常用的轻量方案。
package anomaly

import (
	"math"
	"sync"
)

// Result 是一次检测的结论。
type Result struct {
	Score   float64 // 偏离基线的标准差倍数
	Mean    float64 // 当前基线均值
	Std     float64 // 当前基线标准差
	Anomaly bool    // 是否判定为异常
}

// Detector 为每条时间序列维护一份 EWMA 统计量。
type Detector struct {
	alpha      float64 // 平滑系数，越大越灵敏
	threshold  float64 // 判定阈值（标准差倍数）
	minSamples int     // 预热样本数，避免冷启动误报

	mu     sync.Mutex
	series map[string]*state
}

type state struct {
	mean float64
	v    float64 // 方差
	n    int
}

// New 创建检测器。
func New(alpha, threshold float64, minSamples int) *Detector {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3
	}
	if threshold <= 0 {
		threshold = 3
	}
	if minSamples <= 0 {
		minSamples = 10
	}
	return &Detector{
		alpha:      alpha,
		threshold:  threshold,
		minSamples: minSamples,
		series:     make(map[string]*state),
	}
}

// Observe 送入一个采样点并返回检测结果。
func (d *Detector) Observe(key string, value float64) Result {
	d.mu.Lock()
	defer d.mu.Unlock()

	s, ok := d.series[key]
	if !ok {
		s = &state{mean: value}
		d.series[key] = s
	}

	// 先计算当前点相对旧基线的偏离度，再更新基线，
	// 这样异常点不会立刻把基线"带偏"。
	prevMean, prevStd := s.mean, math.Sqrt(s.v)
	score := 0.0
	if prevStd > 1e-9 {
		score = math.Abs(value-prevMean) / prevStd
	}

	s.n++
	s.mean = d.alpha*value + (1-d.alpha)*s.mean
	dev := value - s.mean
	s.v = d.alpha*dev*dev + (1-d.alpha)*s.v

	return Result{
		Score:   score,
		Mean:    prevMean,
		Std:     prevStd,
		Anomaly: s.n >= d.minSamples && score >= d.threshold,
	}
}

// Reset 丢弃某条序列的历史。
func (d *Detector) Reset(key string) {
	d.mu.Lock()
	delete(d.series, key)
	d.mu.Unlock()
}
