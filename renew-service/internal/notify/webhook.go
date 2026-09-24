package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type Config struct {
	WebhookURLEnv string
}

type Event struct {
	Certificate string    `json:"certificate"`
	Operation   string    `json:"operation"`
	Result      string    `json:"result"`
	Error       string    `json:"error,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func Send(ctx context.Context, cfg Config, event Event) error {
	if cfg.WebhookURLEnv == "" {
		return nil
	}
	url := os.Getenv(cfg.WebhookURLEnv)
	if url == "" {
		return nil
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("notification returned HTTP %d", res.StatusCode)
	}
	return nil
}
