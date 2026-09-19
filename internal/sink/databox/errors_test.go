package databox

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/sink"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	envelope := `{"requestId":"req-1","status":"error","errors":[{"code":"schema_mismatch","message":"column x","field":"x","type":"validation"}]}`
	type classified struct {
		class sink.Class
		code  string
	}
	cases := map[string]struct {
		status int
		body   string
		want   classified
	}{
		"401 with envelope":      {401, `{"errors":[{"code":"invalid_api_key","message":"bad"}]}`, classified{sink.Permanent, "invalid_api_key"}},
		"401 without body":       {401, ``, classified{sink.Permanent, "invalid_api_key"}},
		"403":                    {403, ``, classified{sink.Permanent, "forbidden"}},
		"400 with envelope code": {400, envelope, classified{sink.Permanent, "schema_mismatch"}},
		"404 default code":       {404, ``, classified{sink.Permanent, "invalid_request"}},
		"413":                    {413, ``, classified{sink.Permanent, "invalid_request"}},
		"422":                    {422, ``, classified{sink.Permanent, "invalid_request"}},
		"408":                    {408, ``, classified{sink.Retryable, "timeout"}},
		"429 with envelope":      {429, `{"errors":[{"code":"rate_limited","message":"slow down"}]}`, classified{sink.Retryable, "rate_limited"}},
		"429 with html body":     {429, `<html>`, classified{sink.Retryable, "rate_limited"}},
		"500":                    {500, ``, classified{sink.Retryable, "server_error"}},
		"503 with envelope":      {503, `{"errors":[{"code":"service_unavailable","message":"later"}]}`, classified{sink.Retryable, "service_unavailable"}},
		"418 other 4xx":          {418, ``, classified{sink.Permanent, "invalid_request"}},
	}
	got := map[string]classified{}
	want := map[string]classified{}
	for name, tc := range cases {
		class, code := sink.Classify(classify(tc.status, []byte(tc.body)))
		got[name] = classified{class, code}
		want[name] = tc.want
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(classified{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}
