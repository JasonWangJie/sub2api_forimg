package service

import (
	"context"
	"sync"
	"time"
)

// This sampler does not require Ops monitoring to be enabled or query the DB.
// It reuses the existing cgroup-aware CPU and memory implementation.
type imageResultCleanupLoadGuard struct {
	mu         sync.Mutex
	sample     func(context.Context) (*opsCollectedSystemStats, error)
	last       time.Time
	paused     bool
	lowSamples int
	allowed    bool
	reason     string
}

func newImageResultCleanupLoadGuard() *imageResultCleanupLoadGuard {
	collector := &OpsMetricsCollector{}
	return &imageResultCleanupLoadGuard{sample: collector.collectSystemStats}
}

func (g *imageResultCleanupLoadGuard) allow(ctx context.Context, now time.Time) (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.last.IsZero() && now.Sub(g.last) < 5*time.Second {
		return g.allowed, g.reason
	}
	g.last = now
	sampleCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	stats, err := g.sample(sampleCtx)
	if err != nil || stats == nil || stats.cpuUsagePercent == nil || stats.memoryUsagePercent == nil {
		g.paused, g.allowed, g.lowSamples, g.reason = true, false, 0, "资源指标不可用，60 秒后重试"
		return false, g.reason
	}
	cpu, mem := *stats.cpuUsagePercent, *stats.memoryUsagePercent
	if cpu >= 80 || mem >= 85 {
		g.paused, g.allowed, g.lowSamples, g.reason = true, false, 0, "服务器负载偏高，60 秒后复查"
		return false, g.reason
	}
	if g.paused {
		if cpu < 70 && mem < 75 {
			g.lowSamples++
		} else {
			g.lowSamples = 0
		}
		if g.lowSamples < 2 {
			g.allowed = false
			g.reason = "等待负载连续两次恢复正常"
			return false, g.reason
		}
	}
	g.paused, g.allowed, g.reason = false, true, ""
	return true, ""
}
