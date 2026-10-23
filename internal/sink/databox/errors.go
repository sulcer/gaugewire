package databox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sulcer/gaugewire/internal/sink"
)

// envelope is the documented response body: the payload under data on
// success, the errors on failure.
type envelope struct {
	RequestID string          `json:"requestId"`
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	Errors    []struct {
		Code    *string `json:"code"`
		Message string  `json:"message"`
	} `json:"errors"`
}

// classify turns an error response into a retryable or permanent sink error.
// An error status decides the class and the envelope's first code names it;
// an error inside a 2xx is classed by its code, the only signal it carries.
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
	if status < 400 {
		class = classOfCode(code)
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
	case status < 400:
		// An error inside a 2xx that carries no code is a malformed response.
		return sink.Retryable, "invalid_response"
	case status == http.StatusUnauthorized:
		return sink.Permanent, "invalid_api_key"
	case status == http.StatusForbidden:
		return sink.Permanent, "forbidden"
	case status == http.StatusNotFound:
		return sink.Permanent, "not_found"
	case status == http.StatusRequestEntityTooLarge:
		return sink.Permanent, "request_too_large"
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

// classOfCode retries the documented codes that name a transient condition;
// any other code names something a resend cannot fix.
func classOfCode(code string) sink.Class {
	switch code {
	case "rate_limited", "internal_error", "service_unavailable", "processing_failed", "invalid_response":
		return sink.Retryable
	default:
		return sink.Permanent
	}
}
