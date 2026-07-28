package retry

import (
	"context"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
)

var internalErrors = []string{
	"Timedout", // #373
	"No workers running",
	"RPC_CALL_FAIL",
	"RPC_MCGET_FAIL",
	"WORKER_BUSY_TOO_LONG_RETRY", // #462
	"memory limit exit",          // #504
}

type retry struct {
	max    int
	errors []string
}

// retryInterval is the base delay between retries. Retrying immediately in a
// busy loop would only amplify the server-side problem we are recovering from.
const retryInterval = 500 * time.Millisecond

func (r retry) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		retries := 0
		var err error

		for retries < r.max {
			if err = next.Invoke(ctx, input, output); err != nil {
				if tgerr.Is(err, r.errors...) {
					logctx.From(ctx).Debug("retry middleware", zap.Int("retries", retries), zap.Error(err))
					retries++

					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Duration(retries) * retryInterval):
					}
					continue
				}
				return errors.Wrap(err, "retry middleware skip")
			}

			return nil
		}

		return errors.Wrapf(err, "retry limit reached after %d attempts", r.max)
	}
}

// New returns middleware that retries request if it fails with one of provided errors.
func New(max int, errors ...string) telegram.Middleware {
	return retry{
		max:    max,
		errors: append(errors, internalErrors...), // #373
	}
}
