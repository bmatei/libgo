package hsauth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/bmatei/libgo/observability/logs"
	"github.com/julienschmidt/httprouter"
)

const (
	webhookSignatureHeader = "X-Homeostatic-Webhook-Signature"
	webhookKeyIDHeader     = "X-Homeostatic-Webhook-Key-Id"

	maxWebhookBody = 64 << 10
)

// ErrPermanent marks a failure retrying cannot fix: answered 200, logged.
var ErrPermanent = errors.New("permanent webhook failure")

type WebhookEvent struct {
	ID         string
	Type       string
	RealmID    string
	OccurredAt time.Time
	Data       json.RawMessage
}

// WebhookHandler verifies Homeostatic's signature and passes the event to handle.
// handle sees every event type and should return nil for the ones it ignores.
// Delivery is at-least-once with no freshness check: dedupe on WebhookEvent.ID.
//
// handle returns nil → 200; an ErrPermanent → logged, 200; any other error →
// logged, 500 so Homeostatic retries.
func WebhookHandler(svc Service, handle func(ctx context.Context, e WebhookEvent) error) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			logger.Warn().Err(err).Msg("Webhook: failed to read body")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}

		sig, err := base64.RawURLEncoding.DecodeString(r.Header.Get(webhookSignatureHeader))
		if err != nil || len(sig) == 0 {
			logger.Warn().Err(err).Msg("Webhook: missing or malformed signature")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
			return
		}

		kid := r.Header.Get(webhookKeyIDHeader)
		key, err := svc.PublicKey(ctx, kid)
		switch {
		case errors.Is(err, errUnknownKey):
			logger.Warn().Str("kid", kid).Msg("Webhook: unknown signing key")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
			return
		case err != nil:
			logger.Error().Err(err).Msg("Webhook: failed to fetch JWKS")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "couldn't verify signature, try again"})
			return
		}

		digest := sha256.Sum256(body)
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
			logger.Warn().Str("kid", kid).Msg("Webhook: bad signature")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
			return
		}

		var payload struct {
			EventID    string          `json:"event_id"`
			Type       string          `json:"type"`
			RealmID    string          `json:"realm_id"`
			OccurredAt time.Time       `json:"occurred_at"`
			Data       json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || payload.EventID == "" || payload.Type == "" {
			logger.Warn().Err(err).Msg("Webhook: malformed event")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}

		e := WebhookEvent{
			ID:         payload.EventID,
			Type:       payload.Type,
			RealmID:    payload.RealmID,
			OccurredAt: payload.OccurredAt,
			Data:       payload.Data,
		}
		logger = logger.With().Str("event_id", e.ID).Str("event_type", e.Type).Logger()

		err = handle(ctx, e)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusOK)
		case errors.Is(err, ErrPermanent):
			logger.Error().Err(err).Msg("Webhook: permanent failure; not retrying")
			w.WriteHeader(http.StatusOK)
		default:
			logger.Error().Err(err).Msg("Webhook: handler failed; Homeostatic will retry")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "handler failed"})
		}
	}
}
