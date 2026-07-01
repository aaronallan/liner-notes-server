// Package identify serves POST /v1/identify: given a base64-encoded album cover
// image, it calls an LLM to return the artist and album name.
package identify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const identifyPrompt = `You are looking at a photo of a vinyl record album cover. Identify the album.

Return ONLY a raw JSON object — no surrounding text, no markdown fences:
{"artist":"<artist name>","album":"<album title>","uncertain":false}

If you cannot identify the album or the image does not show an album cover, return:
{"artist":null,"album":null,"uncertain":true}`

// Result is the structured identification returned to the caller.
type Result struct {
	Artist    *string `json:"artist"`
	Album     *string `json:"album"`
	Uncertain bool    `json:"uncertain"`
}

// Identifier calls an LLM to identify a record from an image.
type Identifier interface {
	Identify(ctx context.Context, imageBase64, mediaType string) (Result, error)
}

// Handler serves POST /v1/identify.
type Handler struct {
	identifier Identifier
}

// NewHandler constructs an identify handler with the given Identifier implementation.
func NewHandler(id Identifier) *Handler {
	return &Handler{identifier: id}
}

// NewClaudeIdentifier returns an Identifier backed by claude-sonnet-4-6.
func NewClaudeIdentifier(apiKey string, logger *slog.Logger) Identifier {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &claudeIdentifier{client: &client, logger: logger}
}

type requestBody struct {
	Image     string `json:"image"`
	MediaType string `json:"media_type"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Image == "" {
		writeError(w, http.StatusBadRequest, "image is required")
		return
	}
	mediaType := body.MediaType
	if mediaType == "" {
		mediaType = "image/jpeg"
	}

	result, err := h.identifier.Identify(r.Context(), body.Image, mediaType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "identification failed")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// claudeIdentifier calls the Anthropic API.
type claudeIdentifier struct {
	client *anthropic.Client
	logger *slog.Logger
}

func (c *claudeIdentifier) Identify(ctx context.Context, imageBase64, mediaType string) (Result, error) {
	msg, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 512,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewImageBlockBase64(mediaType, imageBase64),
				anthropic.NewTextBlock(identifyPrompt),
			),
		},
	})
	if err != nil {
		return Result{}, err
	}

	var text string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text = tb.Text
			break
		}
	}

	text = stripMarkdownFences(text)

	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		c.logger.Warn("[identify] failed to parse LLM response", "raw", text, "err", err)
		return Result{Uncertain: true}, nil
	}

	artist, album := "(none)", "(none)"
	if result.Artist != nil {
		artist = *result.Artist
	}
	if result.Album != nil {
		album = *result.Album
	}
	c.logger.Info("[identify] result", "artist", artist, "album", album, "uncertain", result.Uncertain)
	return result, nil
}

// stripMarkdownFences removes surrounding ```json ... ``` or ``` ... ``` wrappers.
func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Remove opening fence (```json or ```)
	end := strings.Index(s[3:], "\n")
	if end < 0 {
		return s
	}
	s = s[3+end+1:]
	// Remove closing fence
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
