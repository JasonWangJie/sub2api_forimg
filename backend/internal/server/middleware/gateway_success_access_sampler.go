package middleware

import "sync/atomic"

const gatewaySuccessAccessLogSampleEvery uint64 = 20

// gatewaySuccessAccessSampler keeps successful gateway access logging cheap and
// bounded without retaining per-client or per-path state.
type gatewaySuccessAccessSampler struct {
	every    uint64
	sequence atomic.Uint64
}

func newGatewaySuccessAccessSampler(every uint64) *gatewaySuccessAccessSampler {
	return &gatewaySuccessAccessSampler{every: every}
}

func (s *gatewaySuccessAccessSampler) allow() bool {
	if s == nil || s.every <= 1 {
		return true
	}
	n := s.sequence.Add(1)
	return (n-1)%s.every == 0
}

func (s *gatewaySuccessAccessSampler) sampleEvery() uint64 {
	if s == nil || s.every == 0 {
		return 1
	}
	return s.every
}

var globalGatewaySuccessAccessSampler = newGatewaySuccessAccessSampler(gatewaySuccessAccessLogSampleEvery)
