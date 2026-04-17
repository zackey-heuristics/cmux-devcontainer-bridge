// Package server provides the HTTP handler for the cmux-notify-bridge.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zackey-heuristics/cmux-devcontainer-bridge/internal/notifier"
)

// Config holds the dependencies and settings for the HTTP handler.
type Config struct {
	// Token is the expected bearer token. Empty string disables auth.
	Token string
	// MaxBodyBytes caps the request body size. Default 16384.
	MaxBodyBytes int64
	// DefaultTitle substitutes an empty title field. Default "Claude Code".
	DefaultTitle string
	// Notifier delivers the notification.
	Notifier notifier.Notifier
	// Logger is the structured logger to use.
	Logger *slog.Logger
}

// NewHandler constructs the http.Handler for the bridge.
func NewHandler(cfg Config) http.Handler {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 16384
	}
	if cfg.DefaultTitle == "" {
		cfg.DefaultTitle = "Claude Code"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/notify", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		status := handleNotify(w, r, cfg)
		cfg.Logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, jsonBody{OK: false, Error: "not found"})
	})
	return mux
}

// handleNotify implements POST /notify.
// Returns the HTTP status code that was written, for logging.
func handleNotify(w http.ResponseWriter, r *http.Request, cfg Config) int {
	// Method guard.
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, jsonBody{OK: false, Error: "method not allowed"})
		return http.StatusMethodNotAllowed
	}

	// Token auth (applied to /notify only; /healthz is always open).
	if cfg.Token != "" {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeJSON(w, http.StatusUnauthorized, jsonBody{OK: false, Error: "unauthorized"})
			return http.StatusUnauthorized
		}
		provided := header[len(prefix):]
		// Reject empty tokens explicitly so intent is readable; ConstantTimeCompare
		// would also reject (length mismatch) but the explicit guard documents it.
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, jsonBody{OK: false, Error: "unauthorized"})
			return http.StatusUnauthorized
		}
	}

	// Content-Type guard. RFC 7231 §3.1.1.1 — media type names are case
	// insensitive, so fold to lower before matching.
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, jsonBody{OK: false, Error: "content-type must be application/json"})
		return http.StatusUnsupportedMediaType
	}

	// Body cap.
	r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)

	// Decode JSON.
	var raw struct {
		Title       string `json:"title"`
		Subtitle    string `json:"subtitle"`
		Body        string `json:"body"`
		WorkspaceID string `json:"workspace_id"`
		SurfaceID   string `json:"surface_id"`
		Source      string `json:"source"`
		Kind        string `json:"kind"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil {
		// http.MaxBytesReader returns an error whose message contains "request body too large"
		// on overflow. Use that to distinguish 413 from 400.
		if isBodyTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, jsonBody{OK: false, Error: "request body too large"})
			return http.StatusRequestEntityTooLarge
		}
		writeJSON(w, http.StatusBadRequest, jsonBody{OK: false, Error: "invalid JSON"})
		return http.StatusBadRequest
	}

	// Trim and cap fields.
	title := trimCap(raw.Title, 256)
	subtitle := trimCap(raw.Subtitle, 256)
	body := trimCap(raw.Body, 4096)
	workspaceID := trimCap(raw.WorkspaceID, 128)
	surfaceID := trimCap(raw.SurfaceID, 128)
	source := trimCap(raw.Source, 64)
	kind := trimCap(raw.Kind, 64)

	// Apply default title.
	if title == "" {
		title = cfg.DefaultTitle
	}

	cfg.Logger.Info("notify",
		"title_len", utf8.RuneCountInString(title),
		"body_len", utf8.RuneCountInString(body),
	)
	cfg.Logger.Debug("notify detail",
		"title_prefix", titlePrefix(title, 32),
	)

	p := notifier.Payload{
		Title:       title,
		Subtitle:    subtitle,
		Body:        body,
		WorkspaceID: workspaceID,
		SurfaceID:   surfaceID,
		Source:      source,
		Kind:        kind,
	}

	if err := cfg.Notifier.Notify(r.Context(), p); err != nil {
		errMsg := sanitizeError(err)
		writeJSON(w, http.StatusBadGateway, jsonBody{OK: false, Error: errMsg})
		return http.StatusBadGateway
	}

	writeJSON(w, http.StatusOK, jsonBody{OK: true})
	return http.StatusOK
}

// handleHealthz implements GET /healthz.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, jsonBody{OK: false, Error: "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, jsonBody{OK: true})
}

type jsonBody struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body jsonBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// trimCap trims whitespace, strips CR/LF (to prevent log and argv newline
// injection), and caps the string at maxRunes rune count.
func trimCap(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
		s = string(runes)
	}
	return s
}

// titlePrefix returns the first n runes of title for debug logging.
func titlePrefix(title string, n int) string {
	runes := []rune(title)
	if len(runes) <= n {
		return title
	}
	return string(runes[:n])
}

// sanitizeError strips CR/LF and caps the error message.
func sanitizeError(err error) string {
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "\r", "")
	msg = strings.ReplaceAll(msg, "\n", " ")
	runes := []rune(msg)
	if len(runes) > 256 {
		runes = runes[:256]
		msg = string(runes)
	}
	return msg
}

// isBodyTooLarge reports whether err indicates the body exceeded MaxBytesReader's limit.
func isBodyTooLarge(err error) bool {
	// http.MaxBytesError was added in Go 1.19.
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return true
	}
	// Fallback: check for io.EOF-derived errors with the known message text.
	// This branch covers cases where the error is wrapped differently.
	if err == io.ErrUnexpectedEOF {
		return false
	}
	return strings.Contains(err.Error(), "request body too large")
}
