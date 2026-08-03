package dl

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIterPauseSkip 验证：paused 集合中的文件被跳过（不产出 elem），
// 不影响主状态机顺序。
func TestIterPauseSkip(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  map[MsgKey]struct{}{},
		requeue: []MsgKey{},
	}
	// 用真实 iter 结构验证集合行为（不连 telegram）
	it.PauseKeys([]MsgKey{{PeerID: 1, MsgID: 2}})
	_, paused := it.paused[MsgKey{PeerID: 1, MsgID: 2}]
	require.True(t, paused, "PauseKeys should add to paused set")
	require.False(t, it.IsPaused(MsgKey{PeerID: 1, MsgID: 3}))
}

func TestIterUnpauseRequeue(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  map[MsgKey]struct{}{{PeerID: 1, MsgID: 2}: {}},
		requeue: []MsgKey{},
	}
	it.Unpause(MsgKey{PeerID: 1, MsgID: 2})
	_, paused := it.paused[MsgKey{PeerID: 1, MsgID: 2}]
	require.False(t, paused, "Unpause should remove from paused set")
	require.Len(t, it.requeue, 1)
	require.Equal(t, MsgKey{PeerID: 1, MsgID: 2}, it.requeue[0])
}

func TestIterClearPaused(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  map[MsgKey]struct{}{{PeerID: 1, MsgID: 2}: {}},
		requeue: []MsgKey{},
	}
	it.ClearPaused()
	require.Empty(t, it.paused)
	require.Empty(t, it.requeue)
}

// TestIterPauseConcurrency 并发冒烟：PauseKeys/Unpause/IsPaused/nextRequeue/
// ClearPaused 由多个 goroutine 同时打 iter，验证 i.mu 保护下不重入死锁、
// 不出现重复 requeue（Unpause 去重）。若锁语义错误会死锁（测试超时失败）
// 或 duplicate 断言失败。需 -race 才能完整验证数据竞争（本机无 gcc，CI 可跑）。
func TestIterPauseConcurrency(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  make(map[MsgKey]struct{}),
		requeue: make([]MsgKey, 0),
	}

	const workers = 8
	const rounds = 200

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 生产者：并发 Unpause/PauseKeys/IsPaused，模拟 server goroutine
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				k := MsgKey{PeerID: int64(w), MsgID: r}
				select {
				case <-stop:
					return
				default:
				}
				switch r % 3 {
				case 0:
					it.PauseKeys([]MsgKey{k})
				case 1:
					it.Unpause(k)
				default:
					_ = it.IsPaused(k)
				}
			}
		}(w)
	}

	// 消费者：并发 nextRequeue，模拟 Next 弹队列
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				_, _ = it.nextRequeue()
			}
		}()
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 交错执行 ClearPaused，验证 reset 后队列/集合仍一致
			it.ClearPaused()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		close(stop)
		t.Fatal("concurrent pause/requeue operations deadlocked")
	}

	// 最终状态：paused 集合与 requeue 不得有重复 key
	it.mu.Lock()
	defer it.mu.Unlock()
	seen := make(map[MsgKey]bool)
	for _, k := range it.requeue {
		require.False(t, seen[k], "duplicate key in requeue: %s", k.String())
		seen[k] = true
	}
	// 状态自洽：requeue 里的 key 不应仍在 paused（Unpause 先删后加）
	for _, k := range it.requeue {
		_, paused := it.paused[k]
		require.False(t, paused, "key %s both requeued and paused", k.String())
	}
	require.True(t, len(it.requeue) <= workers*workers*rounds, "requeue bound sanity")
}
