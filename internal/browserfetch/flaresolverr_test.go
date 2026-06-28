package browserfetch

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestFlareSolverrFetch(t *testing.T) {
	var got map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"status":"ok","solution":{"url":"https://manga.test","status":200,"response":"<html>ok</html>","userAgent":"ua"}}`), nil
	})}

	result, err := NewFlareSolverr("http://solver.test/v1", time.Second, client).Fetch(t.Context(), "https://manga.test")
	if err != nil {
		t.Fatal(err)
	}
	if got["cmd"] != "request.get" || got["url"] != "https://manga.test" {
		t.Fatalf("request = %#v", got)
	}
	if result.HTML != "<html>ok</html>" || result.UserAgent != "ua" {
		t.Fatalf("result = %#v", result)
	}
}

func TestFlareSolverrHealth(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got["cmd"] != "sessions.list" {
			t.Fatalf("cmd = %q", got["cmd"])
		}
		return jsonResponse(`{"status":"ok"}`), nil
	})}

	if err := NewFlareSolverr("http://solver.test/v1", time.Second, client).Health(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestFlareSolverrError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"status":"error","message":"blocked"}`), nil
	})}

	if _, err := NewFlareSolverr("http://solver.test/v1", time.Second, client).Fetch(t.Context(), "https://manga.test"); err == nil {
		t.Fatal("Fetch() error = nil")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
