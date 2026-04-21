package sender

import (
	"context"
)

type MessageProperty struct {
	Type  string
	Value string
}

type Sender interface {
	Send(ctx context.Context, message string, properties ...MessageProperty) error
}
