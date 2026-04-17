package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zackey-heuristics/cmux-devcontainer-bridge/internal/notifier"
)

// fakeNotifier is a test double for notifier.Notifier.
type fakeNotifier struct {
	err error
}

func (f *fakeNotifier) Notify(_ context.Context, _ notifier.Payload) error {
	return f.err
}

func newHandler(token string, n notifier.Notifier) http.Handler {
	return NewHandler(Config{
		Token:        token,
		MaxBodyBytes: 64,
		DefaultTitle: "Claude Code",
		Notifier:     n,
	})
}

func post(h http.Handler, path, ct, body string, headers map[string]string) *httptest.ResponseRecorder {
	var rb io.Reader
	if body != "" {
		rb = strings.NewReader(body)
	} else {
		rb = strings.NewReader("")
	}
	r := httptest.NewRequest(http.MethodPost, path, rb)
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestHappyPath verifies a valid POST /notify returns 200.
func TestHappyPath(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	w := post(h, "/notify", "application/json", `{"title":"Hello"}`, nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// TestOversizedBody verifies that a body exceeding MaxBodyBytes returns 413.
func TestOversizedBody(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	big := `{"title":"` + strings.Repeat("x", 1000) + `"}`
	w := post(h, "/notify", "application/json", big, nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

// TestBadJSON verifies that malformed JSON returns 400.
func TestBadJSON(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	w := post(h, "/notify", "application/json", `{not json}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// TestMissingContentType verifies that a missing Content-Type returns 415.
func TestMissingContentType(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	w := post(h, "/notify", "", `{"title":"Hi"}`, nil)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415; body: %s", w.Code, w.Body.String())
	}
}

// TestWrongMethod verifies that GET /notify returns 405.
func TestWrongMethod(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	w := get(h, "/notify", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405; body: %s", w.Code, w.Body.String())
	}
}

// TestTokenMissing verifies that a missing Authorization header returns 401 when token is set.
func TestTokenMissing(t *testing.T) {
	h := newHandler("secret", &fakeNotifier{})
	w := post(h, "/notify", "application/json", `{"title":"Hi"}`, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

// TestTokenWrong verifies that a wrong token returns 401.
func TestTokenWrong(t *testing.T) {
	h := newHandler("secret", &fakeNotifier{})
	w := post(h, "/notify", "application/json", `{"title":"Hi"}`, map[string]string{
		"Authorization": "Bearer wrong",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

// TestTokenCorrect verifies that the correct token returns 200.
func TestTokenCorrect(t *testing.T) {
	h := newHandler("secret", &fakeNotifier{})
	w := post(h, "/notify", "application/json", `{"title":"Hi"}`, map[string]string{
		"Authorization": "Bearer secret",
	})
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// TestNotifierError verifies that a notifier error returns 502 with a non-empty error field.
func TestNotifierError(t *testing.T) {
	h := newHandler("", &fakeNotifier{err: errors.New("cmux died")})
	w := post(h, "/notify", "application/json", `{"title":"Hi"}`, nil)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "cmux died") {
		t.Errorf("error not in body: %s", body)
	}
}

// TestHealthzAlwaysOpen verifies /healthz returns 200 even when token is configured.
func TestHealthzAlwaysOpen(t *testing.T) {
	h := newHandler("secret", &fakeNotifier{})
	w := get(h, "/healthz", nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// TestUnknownRoute verifies that unknown routes return 404.
func TestUnknownRoute(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	r := httptest.NewRequest(http.MethodPost, "/unknown", strings.NewReader(""))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestContentTypeCaseInsensitive verifies that mixed-case Content-Type values
// are accepted (RFC 7231 §3.1.1.1).
func TestContentTypeCaseInsensitive(t *testing.T) {
	h := newHandler("", &fakeNotifier{})
	for _, ct := range []string{
		"Application/JSON",
		"APPLICATION/JSON",
		"application/json; charset=utf-8",
	} {
		w := post(h, "/notify", ct, `{"title":"Hi"}`, nil)
		if w.Code != http.StatusOK {
			t.Errorf("Content-Type %q: status = %d, want 200; body: %s", ct, w.Code, w.Body.String())
		}
	}
}

// TestTokenEmptyProvided verifies that "Bearer " with an empty token is 401.
func TestTokenEmptyProvided(t *testing.T) {
	h := newHandler("secret", &fakeNotifier{})
	w := post(h, "/notify", "application/json", `{"title":"Hi"}`, map[string]string{
		"Authorization": "Bearer ",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}

// TestBodyExactlyAtLimit verifies that a request whose body length equals the
// MaxBodyBytes limit is accepted (the limit is exclusive — only bytes beyond
// trigger 413).
func TestBodyExactlyAtLimit(t *testing.T) {
	const limit = 64
	h := NewHandler(Config{
		MaxBodyBytes: limit,
		DefaultTitle: "Claude Code",
		Notifier:     &fakeNotifier{},
	})
	// Construct a JSON payload whose total length is exactly `limit`.
	const prefix = `{"title":"`
	const suffix = `"}`
	padLen := limit - len(prefix) - len(suffix)
	if padLen <= 0 {
		t.Fatalf("limit %d too small for test", limit)
	}
	body := prefix + strings.Repeat("a", padLen) + suffix
	if len(body) != limit {
		t.Fatalf("test setup error: len=%d, want %d", len(body), limit)
	}
	w := post(h, "/notify", "application/json", body, nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// TestTrimCapStripsNewlines verifies that CR/LF in input fields are replaced
// before the payload reaches the notifier (log-injection defence).
func TestTrimCapStripsNewlines(t *testing.T) {
	captured := make(chan notifier.Payload, 1)
	n := &capturingNotifier{payload: captured}
	h := NewHandler(Config{
		MaxBodyBytes: 1024,
		DefaultTitle: "Claude Code",
		Notifier:     n,
	})
	body := "{\"title\":\"hello\\nworld\",\"body\":\"line1\\r\\nline2\"}"
	w := post(h, "/notify", "application/json", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	got := <-captured
	if strings.ContainsAny(got.Title, "\r\n") {
		t.Errorf("title still contains CR/LF: %q", got.Title)
	}
	if strings.ContainsAny(got.Body, "\r\n") {
		t.Errorf("body still contains CR/LF: %q", got.Body)
	}
}

// capturingNotifier stores the Payload it receives for assertion.
type capturingNotifier struct {
	payload chan<- notifier.Payload
}

func (c *capturingNotifier) Notify(_ context.Context, p notifier.Payload) error {
	c.payload <- p
	return nil
}
