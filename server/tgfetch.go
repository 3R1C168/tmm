package server

import (
	"context"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// RPC timeout/retry tuning for UploadGetFile chunk fetches.
//
// Background: a single UploadGetFile RPC can hang for ~30s on RPC jitter (seen
// in server.log "cost:29.7s" for one 512KB chunk), and the per-file middleware
// chain (recovery → retry → floodwait) only retries a limited set of errors with
// NO per-call timeout — so a stuck RPC dead-waits until the request ctx cancels.
// Prefetch runs under context.Background(), so it has no upper bound at all.
//
// These wrap each chunk fetch with a hard per-attempt deadline and a small
// bounded retry, so a stuck RPC is aborted at rpcPerAttemptTimeout and retried
// instead of stalling the whole stream/download.
const (
	rpcPerAttemptTimeout = 25 * time.Second
	rpcMaxAttempts       = 3 // first attempt + 2 retries
	rpcRetryBackoff      = 500 * time.Millisecond
)

// fetchUploadFile calls UploadGetFile with a per-attempt timeout and bounded
// retries. It is safe to retry: UploadGetFile is a pure read keyed by
// (location, offset, limit), so re-issuing the same request is idempotent.
//
// The per-attempt deadline is reset on every retry, so a legitimately slow but
// progressing chunk is never killed — only a truly stuck RPC times out.
func fetchUploadFile(ctx context.Context, client *tg.Client, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	var lastErr error
	for attempt := 0; attempt < rpcMaxAttempts; attempt++ {
		if attempt > 0 {
			// Back off between attempts, but bail out if the parent ctx is cancelled.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(rpcRetryBackoff * time.Duration(attempt)):
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, rpcPerAttemptTimeout)
		res, err := client.UploadGetFile(callCtx, req)
		cancel()
		if err == nil {
			return res, nil
		}
		lastErr = err

		// Parent ctx cancelled — don't retry, surface immediately.
		if ctx.Err() != nil {
			return nil, err
		}
		// A permanent Telegram business error (LOCATION_INVALID, FILE_REFERENCE_*
		// etc.) will never succeed by retrying — return it as-is so the caller can
		// react. Transient errors (timeout, network reset, RPC jitter) fall through
		// and get retried.
		if isPermanentTGError(err) {
			return nil, err
		}
	}
	return nil, errors.Wrap(lastErr, "upload get file after retries")
}

// isPermanentTGError reports whether err is a Telegram business error that
// retrying cannot fix. Transient transport/RPC jitter (timeouts, connection
// resets, the 5 internalErrors the retry middleware handles) returns false and
// is therefore retried by fetchUploadFile.
//
// Conservative by design: when in doubt we retry, since the cost of an extra
// retry is low but the cost of dead-waiting is high (the bug this fixes).
func isPermanentTGError(err error) bool {
	if err == nil {
		return false
	}
	// ctx deadline from the inner WithTimeout is transient → retry.
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// A Telegram RPC error (has a code). 4xx are permanent client errors;
	// 5xx / flood are server-side but floodwait middleware already absorbed
	// those before they reach us, so treat any surfaced tgerr as permanent.
	if rpcErr, ok := tgerr.As(err); ok {
		return rpcErr.IsCodeOneOf(400, 401, 403, 404)
	}
	// Fallback: match well-known permanent error substrings that gotd may wrap
	// without a structured code. Keep the list narrow.
	msg := strings.ToLower(err.Error())
	for _, p := range permanentErrorSubstrings {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

var permanentErrorSubstrings = []string{
	"location_invalid",
	"file_reference", // FILE_REFERENCE_EXPIRED / _EMPTY
	"crc32",          // checksum mismatch — re-reading the same bytes won't help
	"file_id_invalid",
	"file_migrate", // we already dial per-DC; if it still happens, surface it
}
