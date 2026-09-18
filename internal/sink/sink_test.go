package sink

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	type classified struct {
		class Class
		code  string
	}
	base := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want classified
	}{
		{"permanent keeps its code", NewPermanent("invalid_api_key", base), classified{Permanent, "invalid_api_key"}},
		{"retryable keeps its code", NewRetryable("rate_limited", base), classified{Retryable, "rate_limited"}},
		{"wrapped sink error is still classified", fmt.Errorf("sink x: %w", NewPermanent("forbidden", base)), classified{Permanent, "forbidden"}},
		{"unknown error is retryable without a code", base, classified{Retryable, ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			class, code := Classify(tc.err)
			got := classified{class, code}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSinkErrorUnwrapsAndPrints(t *testing.T) {
	t.Parallel()
	base := errors.New("401 unauthorized")
	err := NewPermanent("invalid_api_key", base)
	if !errors.Is(err, base) || err.Error() != "permanent (invalid_api_key): 401 unauthorized" {
		t.Fatalf("got %q, Is(base)=%v", err.Error(), errors.Is(err, base))
	}
}
