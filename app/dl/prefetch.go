package dl

import (
	"context"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/tmessage"
)

// prefetchBatch is how many messages are fetched concurrently per peer.
// Batching cuts the metadata fetch time roughly by this factor compared to the
// previous strictly-serial one-RPC-per-message iteration.
const prefetchBatch = 20

// msgResult is the outcome of resolving one message.
type msgResult struct {
	msg *tg.Message
	err error
}

// prefetcher resolves message bodies ahead of the download iterator and hands
// them out in the original dialog order, so per-message API calls overlap with
// downloads instead of blocking them behind a global lock.
//
// Messages are still fetched with tutil.GetSingleMessage (which benefits from
// the DC pool's migration handling), but a whole batch is resolved concurrently
// in the background rather than one RPC per Next() call.
type prefetcher struct {
	pool dcpool.Pool

	mu      sync.Mutex
	results map[int64]map[int]msgResult // peerID -> msgID -> result
	order   []*tmessage.Dialog          // flattened, in iteration order
}

func newPrefetcher(pool dcpool.Pool, dialogs []*tmessage.Dialog) *prefetcher {
	return &prefetcher{
		pool:    pool,
		results: make(map[int64]map[int]msgResult),
		order:   dialogs,
	}
}

// run resolves every message in the background, in batches per peer. It stops
// early when ctx is canceled. Call Get to consume results in order.
func (p *prefetcher) run(ctx context.Context) {
	for _, d := range p.order {
		peerID := tutil.GetInputPeerID(d.Peer)
		msgs := d.Messages

		for start := 0; start < len(msgs); start += prefetchBatch {
			if ctx.Err() != nil {
				return
			}

			end := start + prefetchBatch
			if end > len(msgs) {
				end = len(msgs)
			}
			p.fetchBatch(ctx, d.Peer, peerID, msgs[start:end])
		}
	}
}

func (p *prefetcher) fetchBatch(ctx context.Context, peer tg.InputPeerClass, peerID int64, ids []int) {
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()

			m, err := tutil.GetSingleMessage(ctx, p.pool.Default(ctx), peer, id)
			if err != nil {
				// keep deleted-message semantics: surface as the sentinel error
				if errors.Is(err, tutil.ErrMessageDeleted) {
					err = tutil.ErrMessageDeleted
				}
			}

			p.mu.Lock()
			if p.results[peerID] == nil {
				p.results[peerID] = make(map[int]msgResult)
			}
			p.results[peerID][id] = msgResult{msg: m, err: err}
			p.mu.Unlock()
		}()
	}
	wg.Wait()
}

// get returns the prefetched result for a message, fetching on demand if the
// background pipeline has not reached it yet (e.g. iteration outran prefetch).
func (p *prefetcher) get(ctx context.Context, peer tg.InputPeerClass, id int) (*tg.Message, error) {
	peerID := tutil.GetInputPeerID(peer)

	p.mu.Lock()
	if res, ok := p.results[peerID][id]; ok {
		delete(p.results[peerID], id) // consume once
		p.mu.Unlock()
		return res.msg, res.err
	}
	p.mu.Unlock()

	// fallback: not prefetched yet, fetch synchronously
	m, err := tutil.GetSingleMessage(ctx, p.pool.Default(ctx), peer, id)
	if err != nil && errors.Is(err, tutil.ErrMessageDeleted) {
		err = tutil.ErrMessageDeleted
	}
	return m, err
}

// prefetch starts the background pipeline and logs when it finishes.
func (p *prefetcher) prefetch(ctx context.Context) {
	go func() {
		p.run(ctx)
		logctx.From(ctx).Debug("Prefetch finished", zap.Error(ctx.Err()))
	}()
}
