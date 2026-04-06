package queue

import "errors"

// permanentError 表示“不可重试”的消费错误（例如消息本身损坏）。
// Queue 消费端应当对其执行 Term()，避免无限重投递。
type permanentError struct {
	err error
}

func (e *permanentError) Error() string {
	if e == nil || e.err == nil {
		return "permanent error"
	}
	return e.err.Error()
}

func (e *permanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Permanent wraps err to mark it as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err is a Permanent error.
func IsPermanent(err error) bool {
	var pe *permanentError
	return errors.As(err, &pe)
}

