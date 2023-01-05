package api

import (
	"context"
)

type MockBlockSimulationRateLimiter struct {
}

func (m *MockBlockSimulationRateLimiter) send(context context.Context, payload *BuilderBlockValidationRequest, isHighPrio bool) error {
	return nil
}

func (m *MockBlockSimulationRateLimiter) currentCounter() int64 {
	return 0
}
