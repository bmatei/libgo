package logs

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type loggerKeyType struct{}

var loggerKey = loggerKeyType{}

func WithLogger(ctx context.Context, logger zerolog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func FromContext(ctx context.Context) zerolog.Logger {
	if logger, ok := ctx.Value(loggerKey).(zerolog.Logger); ok {
		return logger
	}

	return log.With().Logger()
}

type requestIdKeyType struct{}

var requestIdKey = requestIdKeyType{}

func WithRequestId(ctx context.Context, requestId string) context.Context {
	return context.WithValue(ctx, requestIdKey, requestId)
}

func RequestIdFromContext(ctx context.Context) string {
	if requestId, ok := ctx.Value(requestIdKey).(string); ok {
		return requestId
	}

	return uuid.New().String()
}
