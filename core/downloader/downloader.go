package downloader

import (
	"context"
	"os"

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
	// PauseCheck, when non-nil, is invoked before every write of a chunk.
	// A non-nil error aborts the current file's download (the element is
	// finalized with that error). It is how a service backend implements
	// per-file pause. Nil means no check (CLI behavior unchanged).
	PauseCheck func(elem Elem) error
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

		wg.Go(func() error {
			d.opts.Progress.OnAdd(elem)
			if err := d.download(wgctx, elem); err != nil {
				// canceled by user (task-level), so we directly return error to
				// stop all: errgroup cancels wgctx and Download returns the error.
				if errors.Is(err, context.Canceled) {
					rerr := errors.Wrap(err, "download")
					d.opts.Progress.OnDone(elem, rerr)
					return rerr
				}
				// 单文件失败（含 PauseCheck 的用户暂停哨兵）：把错误显式传给 OnDone 判定
				// （上层据此标 FileFailed / FilePaused），但不返回错误 → 不取消 wgctx，
				// 其他文件继续下载。这正是 spec §2.1「文件级暂停不打断任务」的要求。
				d.opts.Progress.OnDone(elem, err)
				logctx.
					From(ctx).
					Error("Download error",
						zap.Any("element", elem),
						zap.Error(err),
					)
				return nil
			}

			d.opts.Progress.OnDone(elem, nil)
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

	// 全新下载（state 无进度）走原版 Parallel——经验证完整可靠。
	// 仅当 state 有部分进度（done>0，即之前下过一部分）才走 fetchPiece resume。
	// 之前的 bug：全新下载也走 fetchPiece（手动 UploadGetFile），它对最后一块
	// 的处理在某些情况下少下，导致文件缺 ~1MB 损坏。
	if state := elem.Resume(); state != nil && state.HasProgress() && !state.Complete() {
		if err := d.resume(ctx, client, elem, state, threads); err != nil {
			return errors.Wrap(err, "resume download")
		}
		return d.ensureComplete(ctx, client, elem, state, threads)
	}

	_, err := downloader.NewDownloader().WithPartSize(MaxPartSize).
		Download(client, elem.File().Location()).
		WithThreads(threads).
		Parallel(ctx, newWriteAt(elem, d.opts.Progress, MaxPartSize, d.opts.PauseCheck))
	if err != nil {
		return errors.Wrap(err, "download")
	}

	// Parallel 后兜底校验（极少数情况 Parallel 也可能不完整）。
	if st := elem.Resume(); st != nil {
		return d.ensureComplete(ctx, client, elem, st, threads)
	}
	return nil
}

// ensureComplete 校验下载完整性：比对磁盘实际字节数与 elem.File().Size()，
// 不足则把缺失的尾部 piece 标记为未完成并重新 resume，循环直到完整或达到重试上限。
// 这道兜底弥补 state.Complete() 与磁盘实际不同步、以及 gotd 提前退出的问题。
func (d *Downloader) ensureComplete(ctx context.Context, client *tg.Client, elem Elem, state *State, threads int) error {
	const maxRetries = 3
	want := elem.File().Size()
	// elem.To() 实际是 *os.File（iter 创建时传入），取其路径做 size 校验
	path := ""
	if f, ok := elem.To().(interface{ Name() string }); ok {
		path = f.Name()
	}
	for i := 0; i < maxRetries && path != ""; i++ {
		fi, serr := os.Stat(path)
		if serr != nil {
			return errors.Wrap(serr, "stat download")
		}
		if fi.Size() == want {
			return nil // 完整
		}
		if fi.Size() > want {
			return errors.Errorf("size overflow: got %d want %d", fi.Size(), want)
		}
		// 磁盘不足：把缺失的尾部 piece 强制标记为未完成，触发 resume 重下。
		state.MarkMissingFrom(fi.Size())
		logctx.From(ctx).Warn("incomplete download, retrying tail",
			zap.Int64("got", fi.Size()), zap.Int64("want", want), zap.Int("retry", i+1))
		if err := d.resume(ctx, client, elem, state, threads); err != nil {
			return errors.Wrap(err, "resume missing tail")
		}
	}
	// 重试上限仍不完整
	if path != "" {
		if fi, _ := os.Stat(path); fi != nil && fi.Size() != want {
			return errors.Errorf("incomplete after %d retries: got %d want %d", maxRetries, fi.Size(), want)
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

	w := newWriteAt(elem, d.opts.Progress, MaxPartSize, d.opts.PauseCheck)

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
//
// Telegram's upload.getFile requires `limit` to be divisible by 4096 (the
// minimum part granularity). The trailing piece of a file is almost never a
// multiple of 4096, so we round the requested limit UP to the next 4096
// boundary (capped at MaxPartSize). Telegram returns only the bytes that
// actually exist, which we write at the exact offset — requesting a bit more
// than the tail is harmless and avoids LIMIT_INVALID (rpc error code 400).
const minPartAlign = 4096

func (d *Downloader) fetchPiece(ctx context.Context, client *tg.Client, elem Elem, w *writeAt, pr pieceRange) error {
	off := pr.Off
	for off < pr.End {
		limit := int64(MaxPartSize)
		if rem := pr.End - off; rem < limit {
			limit = rem
		}
		// 对齐到 4096 倍数：limit 向上取整，避免尾部非整分片触发 LIMIT_INVALID
		if r := limit % minPartAlign; r != 0 {
			limit += minPartAlign - r
			if limit > MaxPartSize {
				limit = MaxPartSize
			}
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
