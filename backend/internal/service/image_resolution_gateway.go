package service

import "context"

func (s *OpenAIGatewayService) SetImageResolutionGuard(m *ImageResolutionMonitor) {
	s.imageResolutionMonitor = m
}
func (s *GatewayService) SetImageResolutionGuard(m *ImageResolutionMonitor) {
	s.imageResolutionMonitor = m
}
func (s *GeminiMessagesCompatService) SetImageResolutionGuard(m *ImageResolutionMonitor) {
	s.imageResolutionMonitor = m
}
func (s *AntigravityGatewayService) SetImageResolutionGuard(m *ImageResolutionMonitor) {
	s.imageResolutionMonitor = m
}

func (m *ImageResolutionMonitor) cooling(ctx context.Context, account *Account) bool {
	if !m.enabled(ctx) || account == nil {
		return false
	}
	route, ok := ImageAccountPoolRouteFromContext(ctx)
	if !ok {
		if expectation, exists := ctx.Value(imageResolutionRequestKey{}).(ImageResolutionExpectation); exists {
			route.Model = expectation.RequestedModel
			route.SizeTier = expectation.SizeTier
		}
	}
	if route.Model == "" || route.SizeTier == "" {
		return false
	}
	cooling, err := m.guard.IsCooling(ctx, account.ID, route.Model, route.SizeTier)
	return err == nil && cooling
}
