package llm

import "fmt"

type APIError struct {
	Provider   string
	StatusCode int
	Status     string
	Body       string
}

func (err APIError) Error() string {
	if err.Body != "" {
		return fmt.Sprintf("%s: unexpected status %s: %s", err.Provider, err.Status, err.Body)
	}
	return fmt.Sprintf("%s: unexpected status %s", err.Provider, err.Status)
}

func (err APIError) Retryable() bool {
	switch err.StatusCode {
	case 408, 409, 425, 429:
		return true
	case 400, 401, 403, 404, 422:
		return false
	default:
		return err.StatusCode >= 500
	}
}
