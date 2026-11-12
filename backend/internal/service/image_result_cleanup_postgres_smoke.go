//go:build postgres

package service

import (
	"context"
	"time"
)

// SetImageResultCleanupPostgresSmokeSample is only compiled by the explicit
// postgres integration-test build tag. Normal server builds have no such entry
// point. It supplies a resource sample and advances only the guard's sampling
// interval; production hysteresis, job leases, deadlines and rate limiting stay
// unchanged. It must only be called while this test worker is idle.
func SetImageResultCleanupPostgresSmokeSample(s *ImageResultCleanupService, cpu, memory float64) {
	s.load.mu.Lock()
	defer s.load.mu.Unlock()
	s.load.sample = func(context.Context) (*opsCollectedSystemStats, error) {
		return &opsCollectedSystemStats{cpuUsagePercent: &cpu, memoryUsagePercent: &memory}, nil
	}
	s.load.last = time.Time{}
}
