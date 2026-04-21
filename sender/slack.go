package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type slackWebhook struct {
	webhook string
}

type slackPayload struct {
	Text string `json:"text"`
}

func NewSlackWebhook(webhook string) *slackWebhook {
	return &slackWebhook{webhook}
}

func (sw *slackWebhook) Send(ctx context.Context, message string, properties ...MessageProperty) error {
	payload := slackPayload{
		Text: message,
	}

	bytesPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(sw.webhook, "application/json", bytes.NewBuffer(bytesPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack responded with non-200 status: %s", resp.Status)
	}

	return nil
}
