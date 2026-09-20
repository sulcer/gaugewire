package databox

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// call is one request the fake saw.
type call struct {
	Method      string
	Path        string
	Key         string
	Accept      string
	ContentType string
	Body        string
}

// reply is one scripted response for a method+path.
type reply struct {
	Status   int
	Body     string
	Location string
}

// fake is a scripted Databox API: replies are keyed by "METHOD /path"; an
// unscripted request gets 404 with an error envelope. It records every call.
type fake struct {
	mu      sync.Mutex
	replies map[string][]reply
	calls   []call
	server  *httptest.Server
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{replies: map[string][]reply{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// on queues a reply for a method and path; several replies are served in order
// and the last one repeats.
func (f *fake) on(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := method + " " + path
	f.replies[key] = append(f.replies[key], reply{Status: status, Body: body})
}

func (f *fake) onRedirect(method, path string, status int, location string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := method + " " + path
	f.replies[key] = append(f.replies[key], reply{Status: status, Location: location})
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{
		Method:      r.Method,
		Path:        r.URL.Path,
		Key:         r.Header.Get("x-api-key"),
		Accept:      r.Header.Get("Accept"),
		ContentType: r.Header.Get("Content-Type"),
		Body:        string(raw),
	})
	key := r.Method + " " + r.URL.Path
	queue := f.replies[key]
	if len(queue) == 0 {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"requestId":"req-404","status":"error","errors":[{"code":"not_found","message":"no route","field":"","type":"routing"}]}`))
		return
	}
	rep := queue[0]
	if len(queue) > 1 {
		f.replies[key] = queue[1:]
	}
	if rep.Location != "" {
		w.Header().Set("Location", rep.Location)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rep.Status)
	_, _ = w.Write([]byte(rep.Body))
}

func (f *fake) seen() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}
