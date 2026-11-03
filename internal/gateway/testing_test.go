package gateway

import "github.com/wsy/aiosight/internal/metrics"

func newTestRegistry() *metrics.Registry { return metrics.New() }
