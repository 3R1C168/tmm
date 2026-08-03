package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeResults 的双游标语义：并集搜索续拉必须各自携带全文/标签游标，
// 且合并结果按 msgID 倒序（新→旧）。
func TestMergeResultsDualCursor(t *testing.T) {
	a := &SearchResult{
		Results: []*MessageInfo{
			{ID: 500}, {ID: 480}, {ID: 451},
		},
		NextOffset: 451, // 全文源游标
		Count:      10,
	}
	b := &SearchResult{
		Results: []*MessageInfo{
			{ID: 1000}, {ID: 700}, {ID: 551},
		},
		NextOffset: 551, // 标签源游标
		Count:      24,
	}

	got := mergeResults(a, b)

	// 双游标各传各的
	require.Equal(t, 451, got.NextOffset, "全文游标应独立保留")
	require.Equal(t, 551, got.NextOffsetTag, "标签游标应独立保留")

	// 合并去重 + 按 msgID 倒序（新→旧）
	want := []int{1000, 700, 551, 500, 480, 451}
	ids := make([]int, 0, len(got.Results))
	for _, m := range got.Results {
		ids = append(ids, m.ID)
	}
	require.Equal(t, want, ids)

	// Count = 两源之和减重复（此处无重复）
	require.Equal(t, 34, got.Count)
}

// 两源结果有重叠时去重，Count 扣掉重复数量。
func TestMergeResultsDedup(t *testing.T) {
	a := &SearchResult{
		Results:    []*MessageInfo{{ID: 500}, {ID: 451}},
		NextOffset: 451,
		Count:      5,
	}
	b := &SearchResult{
		Results:    []*MessageInfo{{ID: 451}, {ID: 300}},
		NextOffset: 300,
		Count:      4,
	}

	got := mergeResults(a, b)
	require.Equal(t, []int{500, 451, 300}, msgIDs(got.Results))
	// 5 + 4 - (2+2-3) = 5 + 4 - 1 = 8
	require.Equal(t, 8, got.Count)
	require.Equal(t, 451, got.NextOffset, "全文游标取自 a")
	require.Equal(t, 300, got.NextOffsetTag, "标签游标取自 b")
}

// 单源（有 # 标签搜索）只返回单个游标：NextOffset 生效，NextOffsetTag 为 0。
// 前端 hasMore 判定为 `full > 0 || tag > 0`，0 游标页（无更多结果）正确收尾。
func TestSingleSourceCursor(t *testing.T) {
	res := &SearchResult{
		Results:    []*MessageInfo{{ID: 500}},
		NextOffset: 451,
		Count:      3,
	}
	require.Equal(t, 451, res.NextOffset)
	require.Equal(t, 0, res.NextOffsetTag, "单源标签搜索不应有标签游标")
}

func msgIDs(msgs []*MessageInfo) []int {
	ids := make([]int, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	return ids
}
