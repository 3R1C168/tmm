package downloader

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/tutil"
)

// MaxPartSize refer to https://core.telegram.org/api/files#downloading-files
const MaxPartSize = 1024 * 1024

type Downloader struct {
	opts Options
}

type Options struct {
	Pool     dcpool.Pool
	Threads  int
	Iter     Iter
	Progress Progress
}

func New(opts Options) *Downloader {
	return &Downloader{
		opts: opts,
	}
}

func (d *Downloader) Download(ctx context.Context, limit int) error {
	wg, wgctx := errgroup.WithContext(ctx)
	wg.SetLimit(limit)

	for d.opts.Iter.Next(wgctx) {
		elem := d.opts.Iter.Value()

		wg.Go(func() (rerr error) {
			d.opts.Progress.OnAdd(elem)
			defer func() { d.opts.Progress.OnDone(elem, rerr) }()

			if err := d.download(wgctx, elem); err != nil {
				// canceled by user, so we directly return error to stop all
				if errors.Is(err, context.Canceled) {
					return errors.Wrap(err, "download")
				}

				// don't return error, just log it
				logctx.
					From(ctx).
					Error("Download error",
						zap.Any("element", elem),
						zap.Error(err),
					)
			}

			return nil
		})
	}

	if err := d.opts.Iter.Err(); err != nil {
		return errors.Wrap(err, "iter")
	}

	return wg.Wait()
}

func (d *Downloader) download(ctx context.Context, elem Elem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	logctx.From(ctx).Debug("Start download elem",
		zap.Any("elem", elem))

	client := d.opts.Pool.Client(ctx, elem.File().DC())
	if elem.AsTakeout() {
		client = d.opts.Pool.Takeout(ctx, elem.File().DC())
	}

	threads := tutil.BestThreads(elem.File().Size(), d.opts.Threads)

	if state := elem.Resume(); state != nil {
		// resume: only fetch the pieces that are still missing
		if err := d.resume(ctx, client, elem, state, threads); err != nil {
			return errors.Wrap(err, "resume download")
		}
		// 完整性校验：gotd plain 模式可能提前退出（服务器返回短块被当 EOF），
		// 导致部分 piece 没下。resume 后用 state.Complete() 兜底，缺块则报错，
		// FinalizeElem 会保留 .tmp/.tdlstate 让下次继续补齐。
		if !state.Complete() {
			return errors.Errorf("incomplete after resume: %d/%d pieces", state.done, numPieces(state.size, state.partSize))
		}
		return nil
	}

	_, err := downloader.NewDownloader().WithPartSize(MaxPartSize).
		Download(client, elem.File().Location()).
		WithThreads(threads).
		Parallel(ctx, newWriteAt(elem, d.opts.Progress, MaxPartSize))
	if err != nil {
		return errors.Wrap(err, "download")
	}

	// 全新下载同样校验完整性（state 由 newWriteAt 的 Mark 更新）。
	// 提前退出会留下未置位的 piece，Complete() 检测出来。
	if st := elem.Resume(); st != nil {
		if !st.Complete() {
			return errors.Errorf("incomplete: %d/%d pieces", st.done, numPieces(st.size, st.partSize))
		}
	}

	return nil
}

// resume downloads only the missing pieces recorded in state, in parallel,
// and persists progress to the control file as pieces complete.
func (d *Downloader) resume(ctx context.Context, client *tg.Client, elem Elem, state *State, threads int) error {
	missing := state.Missing()
	if len(missing) == 0 {
		return nil
	}

	logctx.From(ctx).Info("Resume download",
		zap.String("file", elem.File().Location().String()),
		zap.Int("missing_pieces", len(missing)))

	w := newWriteAt(elem, d.opts.Progress, MaxPartSize)

	wg, wgctx := errgroup.WithContext(ctx)
	wg.SetLimit(threads)

	for _, pr := range missing {
		pr := pr
		wg.Go(func() error {
			return d.fetchPiece(wgctx, client, elem, w, pr)
		})
	}

	if err := wg.Wait(); err != nil {
		// persist whatever we managed to write so a later run resumes from here
		_ = state.Flush()
		return err
	}

	return state.Flush()
}

// fetchPiece downloads a single piece [pr.Off, pr.End) and writes it at its
// absolute offset. Offsets are aligned to partSize, satisfying Telegram's
// precise-mode requirement (offset % 1024 == 0).
func (d *Downloader) fetchPiece(ctx context.Context, client *tg.Client, elem Elem, w *writeAt, pr pieceRange) error {
	off := pr.Off
	for off < pr.End {
		limit := int64(MaxPartSize)
		if rem := pr.End - off; rem < limit {
			limit = rem
		}

		res, err := client.UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Precise:  true,
			Location: elem.File().Location(),
			Offset:   off,
			Limit:    int(limit),
		})
		if err != nil {
			return errors.Wrap(err, "get file")
		}

		file, ok := res.(*tg.UploadFile)
		if !ok {
			return errors.Errorf("unexpected response type %T", res)
		}
		if len(file.Bytes) == 0 {
			return errors.Errorf("empty chunk at offset %d", off)
		}

		if _, err = w.WriteAt(file.Bytes, off); err != nil {
			return errors.Wrap(err, "write chunk")
		}

		off += int64(len(file.Bytes))
	}

	return nil
}
