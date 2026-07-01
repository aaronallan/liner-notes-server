package identify_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aaronpollock/liner-notes-server/internal/identify"
)

type mockIdentifier struct {
	result       identify.Result
	err          error
	gotMediaType string
}

func (m *mockIdentifier) Identify(_ context.Context, _, mediaType string) (identify.Result, error) {
	m.gotMediaType = mediaType
	return m.result, m.err
}

func strPtr(s string) *string { return &s }

func post(h http.Handler, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/identify", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := identify.NewHandler(&mockIdentifier{})
	req := httptest.NewRequest(http.MethodGet, "/v1/identify", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", w.Code)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	h := identify.NewHandler(&mockIdentifier{})
	req := httptest.NewRequest(http.MethodPost, "/v1/identify", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestHandler_MissingImage(t *testing.T) {
	h := identify.NewHandler(&mockIdentifier{})
	w := post(h, map[string]string{"media_type": "image/jpeg"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestHandler_IdentifierError(t *testing.T) {
	h := identify.NewHandler(&mockIdentifier{err: errors.New("llm unavailable")})
	w := post(h, map[string]string{"image": "abc123"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", w.Code)
	}
}

func TestHandler_SuccessReturnsArtistAndAlbum(t *testing.T) {
	mock := &mockIdentifier{
		result: identify.Result{
			Artist:    strPtr("Pink Floyd"),
			Album:     strPtr("The Dark Side of the Moon"),
			Uncertain: false,
		},
	}
	h := identify.NewHandler(mock)
	w := post(h, map[string]string{"image": "abc123", "media_type": "image/jpeg"})

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var result identify.Result
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Artist == nil || *result.Artist != "Pink Floyd" {
		t.Errorf("artist: got %v, want Pink Floyd", result.Artist)
	}
	if result.Album == nil || *result.Album != "The Dark Side of the Moon" {
		t.Errorf("album: got %v, want The Dark Side of the Moon", result.Album)
	}
	if result.Uncertain {
		t.Error("uncertain: want false")
	}
}

func TestHandler_UncertainResult(t *testing.T) {
	mock := &mockIdentifier{
		result: identify.Result{Uncertain: true},
	}
	h := identify.NewHandler(mock)
	w := post(h, map[string]string{"image": "abc123"})

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var result identify.Result
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Uncertain {
		t.Error("uncertain: want true")
	}
}

func TestHandler_DefaultsMediaTypeToJPEG(t *testing.T) {
	mock := &mockIdentifier{result: identify.Result{Uncertain: true}}
	h := identify.NewHandler(mock)
	post(h, map[string]string{"image": "abc123"})
	if mock.gotMediaType != "image/jpeg" {
		t.Errorf("media_type: got %q, want image/jpeg", mock.gotMediaType)
	}
}

func TestHandler_ContentTypeJSON(t *testing.T) {
	h := identify.NewHandler(&mockIdentifier{})
	w := post(h, map[string]string{"image": "abc123"})
	ct := w.Header().Get("Content-Type")
	if ct == "" || ct[:16] != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
}
