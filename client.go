package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const (
	maxResponseBytes = 8 << 20
	errorBodyRunes   = 200
	redactedMarker   = "[redacted]"
)

var (
	// ErrJevRefused is returned when the System One API answers with a status
	// other than 200. Match it with errors.Is.
	ErrJevRefused = errors.New("jev request refused")
	// ErrJevMalformed is returned when the System One API answers with a body
	// that is not a JSON object carrying answers. Match it with errors.Is.
	ErrJevMalformed = errors.New("jev returned a malformed response")
	// ErrJevMissingAnswer is returned when Jev leaves a question without a
	// probability. Match it with errors.Is.
	ErrJevMissingAnswer = errors.New("jev left a question unanswered")
	// ErrJevRedirected is returned when the System One API answers with a
	// redirect, which the stage never follows. Match it with errors.Is.
	ErrJevRedirected = errors.New("jev answered with a redirect")
)

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s (%d): %s", ErrJevRefused, e.status, e.body)
}

func (e *statusError) Unwrap() error { return ErrJevRefused }

type jevRequest struct {
	Model     string              `json:"model"`
	State     compactionState     `json:"state"`
	Questions map[string]question `json:"questions"`
}

type jevAnswer struct {
	Noul *float64 `json:"noul"`
}

type jevResponse struct {
	Answers map[string]jevAnswer `json:"answers"`
}

type jevClient struct {
	http     *http.Client
	endpoint string
	apiKey   string
	model    string
}

func (c *jevClient) ask(ctx context.Context, state compactionState, questions map[string]question) (map[string]jevAnswer, error) {
	body, err := encodeJSON(jevRequest{Model: c.model, State: state, Questions: questions})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("jev: build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("jev: request: %w", err)
	}
	defer closeBody(response.Body)
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("jev: read response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, &statusError{status: response.StatusCode, body: truncate(c.redacted(string(payload)), errorBodyRunes)}
	}
	var decoded jevResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJevMalformed, err)
	}
	if decoded.Answers == nil {
		return nil, fmt.Errorf("%w: no answers", ErrJevMalformed)
	}
	return decoded.Answers, nil
}

func (c *jevClient) redacted(text string) string {
	if c.apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, c.apiKey, redactedMarker)
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return ErrJevRedirected
}

func closeBody(body io.Closer) {
	if err := body.Close(); err != nil {
		slog.Debug("jev: close response body", "err", err)
	}
}

func noul(answers map[string]jevAnswer, name string) (float64, error) {
	answer, found := answers[name]
	if !found || answer.Noul == nil {
		return 0, fmt.Errorf("%w: %s", ErrJevMissingAnswer, name)
	}
	return *answer.Noul, nil
}
