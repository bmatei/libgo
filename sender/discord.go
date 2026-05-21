package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type discordWebhook struct {
	webhook string
}

type discordPayload struct {
	Content string `json:"content"`
}

func NewDiscordWebhook(webhook string) *discordWebhook {
	return &discordWebhook{webhook}
}

func (dw *discordWebhook) Send(ctx context.Context, message string, properties ...MessageProperty) error {
	payload := discordPayload{message}

	bytesPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(dw.webhook, "application/json", bytes.NewBuffer(bytesPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("discord responded with non-200 status: %s", resp.Status)
	}

	return nil
}