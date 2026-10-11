package llm

import (
	"context"
	"errors"
	"fmt"
)

var ErrUnreachable = errors.New("model service could not be reached")

// ServiceError preserves only the status, never a provider body, URL or credential.
type ServiceError struct {
	Status int
	Code   string
}

func (e *ServiceError) Error() string { return fmt.Sprintf("model service returned HTTP %d", e.Status) }
func (e *ServiceError) Is(target error) bool {
	return e.Status == 429 && target == ErrBusy || e.Code == "incomplete" && target == ErrIncomplete || e.Code == "limit" && target == ErrLimit
}

// FailureCode returns bounded diagnostic metadata safe to log without model content.
func FailureCode(err error) (string, int) {
	var service *ServiceError
	status := 0
	if errors.As(err, &service) {
		status = service.Status
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), status == 408, status == 504:
		return "timeout", status
	case errors.Is(err, context.Canceled):
		return "canceled", status
	case errors.Is(err, ErrBusy):
		return "busy", status
	case errors.Is(err, ErrIncomplete):
		return "incomplete", status
	case errors.Is(err, ErrLimit):
		return "limit", status
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrUnreachable):
		return "unavailable", status
	case service != nil:
		return "service", status
	default:
		return "error", status
	}
}
