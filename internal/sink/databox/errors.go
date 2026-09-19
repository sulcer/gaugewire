package databox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sulcer/gaugewire/internal/sink"
)

// envelope is the documented error body.
type envelope struct {
	RequestID string `json:"requestId"`
	Errors    []struct {
		Code    *string `json:"code"`
		Message string  `json:"message"`
	} `json:"errors"`
}

// classify turns an error response into a retryable or permanent sink error.
// The HTTP status decides the class; the envelope's first code names it.
func classify(status int, body []byte) error {
	var env envelope
	_ = json.Unmarshal(body, &env)
	code, message := "", ""
	if len(env.Errors) > 0 {
		if env.Errors[0].Code != nil {
			code = *env.Errors[0].Code
		}
		message = env.Errors[0].Message
	}
	class, fallback := classOf(status)
	if code == "" {
		code = fallback
	}
	text := fmt.Sprintf("databox: HTTP %d %s", status, code)
	if message != "" {
		text += ": " + message
	}
	if env.RequestID != "" {
		text += " (request " + env.RequestID + ")"
	}
	if class == sink.Permanent {
		return sink.NewPermanent(code, errors.New(text))
	}
	return sink.NewRetryable(code, errors.New(text))
}

func classOf(status int) (sink.Class, string) {
	switch {
	case status == http.StatusUnauthorized:
		return sink.Permanent, "invalid_api_key"
	case status == http.StatusForbidden:
		return sink.Permanent, "forbidden"
	case status == http.StatusRequestTimeout:
		return sink.Retryable, "timeout"
	case status == http.StatusTooManyRequests:
		return sink.Retryable, "rate_limited"
	case status >= 500:
		return sink.Retryable, "server_error"
	default:
		return sink.Permanent, "invalid_request"
	}
}
