package dl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/spf13/viper"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/runopts"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/key"
	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/tmessage"
	"github.com/iyear/tdl/pkg/utils"
)

type Options struct {
	Dir        string
	RewriteExt bool
	SkipSame   bool
	Template   string
	URLs       []string
	Files      []string
	Include    []string
	Exclude    []string
	Desc       bool
	Takeout    TakeoutMode
	Group      bool // auto detect grouped message

	// resume opts
	Continue, Restart bool

	// serve
	Serve bool
	Port  int

	// Runtime holds per-run tunables (pool size, threads, limit, ...). When
	// zero, values are read from viper global config for backward compatibility;
	// set it to run downloads programmatically with explicit settings.
	Runtime runopts.Options

	// Progress, when non-nil, receives download progress events instead of the
	// built-in terminal progress bar. This lets non-CLI callers (e.g. a web
	// backend) stream progress to their own UI. The terminal UI is used when nil.
	//
	// Note: a custom Progress is responsible for finalizing each element in
	// OnDone; call FinalizeElem for that (the terminal UI does).
	Progress downloader.Progress

	// IterHook, when non-nil, is invoked right before the download starts with
	// the iterator (see Iter). A service backend uses it to read the resume
	// fingerprint, track finished messages, and persist progress. It is not
	// invoked if dialog collection or iterator construction fails.
	IterHook func(it Iter)

	// Dialogs, when non-empty, supplies (peer, message IDs) pairs directly,
	// bypassing the URL/file parsers. A service backend uses this to download a
	// user-selected set of messages without writing an export file. It is merged
	// with whatever URLs/Files produce.
	Dialogs []*tmessage.Dialog
}

// runtime returns the effective runtime options, preferring explicitly-set
// values and falling back to viper global config for any left unset.
func (o Options) runtime() runopts.Options {
	r := o.Runtime
	if r.PoolSize == 0 {
		r.PoolSize = viper.GetInt(consts.FlagPoolSize)
	}
	if r.ReconnectTimeout == 0 {
		r.ReconnectTimeout = viper.GetDuration(consts.FlagReconnectTimeout)
	}
	if r.Threads == 0 {
		r.Threads = viper.GetInt(consts.FlagThreads)
	}
	if r.Limit == 0 {
		r.Limit = viper.GetInt(consts.FlagLimit)
	}
	if r.Delay == 0 {
		r.Delay = viper.GetDuration(consts.FlagDelay)
	}
	if r.PartSize == 0 {
		r.PartSize = viper.GetInt(consts.FlagPartSize)
	}
	// bool has no "unset" zero value to distinguish, so keep honoring the global
	// flag unless the caller explicitly disables it (a service would set it true).
	if !r.DisableProgressPS {
		r.DisableProgressPS = viper.GetBool(consts.FlagDisableProgressPS)
	}
	return r.Normalize()
}

type parser struct {
	Data   []string
	Parser tmessage.ParseSource
}

func Run(ctx context.Context, c *telegram.Client, kvd storage.Storage, opts Options) (rerr error) {
	rt := opts.runtime()

	pool := dcpool.NewPool(c,
		int64(rt.PoolSize),
		tclient.NewDefaultMiddlewares(ctx, rt.ReconnectTimeout)...)
	defer multierr.AppendInvoke(&rerr, multierr.Close(pool))

	parsers := []parser{
		{Data: opts.URLs, Parser: tmessage.FromURL(ctx, pool, kvd, opts.URLs)},
		{Data: opts.Files, Parser: tmessage.FromFile(ctx, pool, kvd, opts.Files, true)},
	}
	dialogs, err := collectDialogs(parsers)
	if err != nil {
		return err
	}
	// merge any directly-supplied (peer, message IDs) dialogs
	if len(opts.Dialogs) > 0 {
		dialogs = append(dialogs, opts.Dialogs)
	}
	logctx.From(ctx).Debug("Collect dialogs",
		zap.Any("dialogs", dialogs))

	if opts.Serve {
		return serve(ctx, kvd, pool, dialogs, opts.Port, opts.Takeout.Enabled(1<<62), rt.PartSize)
	}

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))

	it, err := newIter(pool, manager, dialogs, opts, rt.Delay)
	if err != nil {
		return err
	}
	// resolve message bodies in the background so downloads are not blocked
	// behind per-message API calls
	it.startPrefetch(ctx)

	if !opts.Restart {
		// resume download and ask user to continue
		if err = resume(ctx, kvd, it, !opts.Continue); err != nil {
			return err
		}
	} else {
		color.Yellow("Restart download by 'restart' flag")
	}

	if opts.IterHook != nil {
		opts.IterHook(it)
	}

	defer func() { // save progress
		if rerr != nil { // download is interrupted
			multierr.AppendInto(&rerr, saveProgress(ctx, kvd, it))
		} else { // if finished, we should clear resume key
			multierr.AppendInto(&rerr, kvd.Delete(ctx, key.Resume(it.Fingerprint())))
		}
	}()

	progress := opts.Progress
	if progress == nil {
		dlProgress := prog.New(utils.Byte.FormatBinaryBytes)
		dlProgress.SetNumTrackersExpected(it.Total())
		if !rt.DisableProgressPS {
			prog.EnablePS(ctx, dlProgress)
		}
		progress = newProgress(dlProgress, it, opts)

		go dlProgress.Render()
		defer func() {
			prog.Wait(ctx, dlProgress)

			// Notify user if any messages were skipped due to deletion
			// This is deferred to ensure it shows after progress rendering completes
			if skipped := it.SkippedDeleted(); skipped > 0 {
				deletedIDs := it.DeletedIDs()
				if len(deletedIDs) <= 5 {
					// Show all IDs if 5 or fewer
					color.Yellow("⚠️  %d message(s) were skipped because they were deleted: %v", skipped, deletedIDs)
				} else {
					// Show first 5 and indicate there are more
					color.Yellow("⚠️  %d message(s) were skipped because they were deleted: %v... and %d more",
						skipped, deletedIDs[:5], len(deletedIDs)-5)
				}
			}
		}()
	}

	options := downloader.Options{
		Pool:     pool,
		Threads:  rt.Threads,
		Iter:     it,
		Progress: progress,
	}
	limit := rt.Limit

	logctx.From(ctx).Info("Start download",
		zap.String("dir", opts.Dir),
		zap.Bool("rewrite_ext", opts.RewriteExt),
		zap.Bool("skip_same", opts.SkipSame),
		zap.Int("threads", options.Threads),
		zap.Int("limit", limit))

	color.Green("All files will be downloaded to '%s' dir", opts.Dir)

	return downloader.New(options).Download(ctx, limit)
}

func collectDialogs(parsers []parser) ([][]*tmessage.Dialog, error) {
	var dialogs [][]*tmessage.Dialog
	for _, p := range parsers {
		d, err := tmessage.Parse(p.Parser)
		if err != nil {
			return nil, err
		}
		dialogs = append(dialogs, d)
	}
	return dialogs, nil
}

func resume(ctx context.Context, kvd storage.Storage, iter *iter, ask bool) error {
	logctx.From(ctx).Debug("Check resume key",
		zap.String("fingerprint", iter.Fingerprint()))

	b, err := kvd.Get(ctx, key.Resume(iter.Fingerprint()))
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if len(b) == 0 { // no progress
		return nil
	}

	finished, err := unmarshalProgress(b)
	if err != nil {
		return err
	}

	// finished is empty, no need to resume
	if len(finished) == 0 {
		return nil
	}

	confirm := false
	resumeStr := fmt.Sprintf("Found unfinished download, continue from '%d/%d'", len(finished), iter.Total())
	if ask {
		if err = survey.AskOne(&survey.Confirm{
			Message: color.YellowString(resumeStr + "?"),
		}, &confirm); err != nil {
			return err
		}
	} else {
		color.Yellow(resumeStr)
		confirm = true
	}

	logctx.From(ctx).Debug("Resume download",
		zap.Int("finished", len(finished)))

	if !confirm {
		// clear resume key
		return kvd.Delete(ctx, key.Resume(iter.Fingerprint()))
	}

	iter.SetFinished(finished)
	return nil
}

// progressRecord is the on-disk resume format. Finished holds order-independent
// "peerID/msgID" keys so progress survives --desc, grouping and deletion.
type progressRecord struct {
	Finished []string `json:"finished"`
}

func marshalProgress(finished map[MsgKey]struct{}) ([]byte, error) {
	rec := progressRecord{Finished: make([]string, 0, len(finished))}
	for k := range finished {
		rec.Finished = append(rec.Finished, fmt.Sprintf("%d/%d", k.PeerID, k.MsgID))
	}
	return json.Marshal(rec)
}

// unmarshalProgress reads the resume record. It understands the current
// order-independent format and the legacy logical-position format; legacy
// positions cannot be mapped back to messages, so they are treated as no
// resumable progress (the affected files simply re-download once).
func unmarshalProgress(b []byte) (map[MsgKey]struct{}, error) {
	out := make(map[MsgKey]struct{})

	// current format
	var rec progressRecord
	if err := json.Unmarshal(b, &rec); err == nil && rec.Finished != nil {
		for _, s := range rec.Finished {
			var peerID int64
			var msgID int
			if _, err := fmt.Sscanf(s, "%d/%d", &peerID, &msgID); err == nil {
				out[MsgKey{PeerID: peerID, MsgID: msgID}] = struct{}{}
			}
		}
		return out, nil
	}

	// legacy format: a JSON object mapping logical positions to {}, which we
	// cannot translate to message keys, so resume is skipped for it.
	var legacy map[int]struct{}
	if err := json.Unmarshal(b, &legacy); err == nil {
		logctx.From(context.Background()).Debug("Ignore legacy resume progress (logical positions)")
		return out, nil
	}

	return nil, errors.New("unrecognized resume progress format")
}

func saveProgress(ctx context.Context, kvd storage.Storage, it *iter) error {
	finished := it.Finished()
	logctx.From(ctx).Debug("Save progress",
		zap.Int("finished", len(finished)))

	b, err := marshalProgress(finished)
	if err != nil {
		return err
	}
	return kvd.Set(ctx, key.Resume(it.Fingerprint()), b)
}
