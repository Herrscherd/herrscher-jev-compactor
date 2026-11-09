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
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes = 8 << 20
	errorBodyRunes   = 200
	redactedMarker   = "[redacted]"
	maxRetries       = 10
	maxRetryDelay    = 30 * time.Second
	backoffFactor    = 2
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
	status     int
	body       string
	retryAfter time.Duration
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s (%d): %s", ErrJevRefused, e.status, e.body)
}

func (e *statusError) Unwrap() error { return ErrJevRefused }

func (e *statusError) retryable() bool {
	return e.status == http.StatusTooManyRequests || e.status >= http.StatusInternalServerError
}

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
	retries  int
	backoff  time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

func (c *jevClient) ask(ctx context.Context, state compactionState, questions map[string]question) error {
	body, err := c.body(state, questions)
	if err != nil {
		return err
	}
	_, err = c.post(ctx, body, newSpendLedger(0, 0))
	return err
}

func (c *jevClient) body(state compactionState, questions map[string]question) (string, error) {
	return encodeJSON(jevRequest{Model: c.model, State: state, Questions: questions})
}

func (c *jevClient) post(ctx context.Context, body string, spend *spendLedger) (map[string]jevAnswer, error) {
	tokens := estimateTokens(body)
	for attempt := 0; ; attempt++ {
		if err := spend.reserve(tokens); err != nil {
			return nil, err
		}
		answers, err := c.send(ctx, body)
		delay, retry := c.retryDelay(err, attempt)
		if !retry {
			return answers, err
		}
		slog.Debug("jev: request retried", "attempt", attempt+1, "delay", delay, "reason", failureReason(err))
		if err := c.wait(ctx, delay); err != nil {
			return nil, fmt.Errorf("jev: wait before retry: %w", err)
		}
	}
}

func (c *jevClient) retryDelay(err error, attempt int) (time.Duration, bool) {
	var refused *statusError
	if !errors.As(err, &refused) || !refused.retryable() || attempt >= c.retries {
		return 0, false
	}
	if refused.retryAfter > maxRetryDelay {
		return 0, false
	}
	return max(backoffDelay(c.backoff, attempt), refused.retryAfter), true
}

func (c *jevClient) wait(ctx context.Context, delay time.Duration) error {
	if c.sleep == nil {
		return sleepContext(ctx, delay)
	}
	return c.sleep(ctx, delay)
}

func (c *jevClient) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

func backoffDelay(base time.Duration, attempt int) time.Duration {
	delay := min(base, maxRetryDelay)
	for range attempt {
		delay = min(delay*backoffFactor, maxRetryDelay)
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32); err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(when.Sub(now), 0)
	}
	return 0
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *jevClient) send(ctx context.Context, body string) (map[string]jevAnswer, error) {
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
		return nil, &statusError{
			status:     response.StatusCode,
			body:       truncate(c.redacted(string(payload)), errorBodyRunes),
			retryAfter: parseRetryAfter(response.Header.Get("Retry-After"), c.clock()),
		}
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

func refusingClient(httpClient *http.Client, timeout time.Duration) *http.Client {
	refusing := http.Client{Timeout: timeout}
	if httpClient != nil {
		refusing = *httpClient
	}
	refusing.CheckRedirect = refuseRedirect
	return &refusing
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
