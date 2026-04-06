package queue

import (
	"context"

	"mailapi/internal/model"
)

// Interface defines all queue operations. Implemented by *Queue.
type Interface interface {
	Publish(ctx context.Context, email *model.IncomingEmail) error
	Consume(ctx context.Context, handler MessageHandler) error
	Close()
}
