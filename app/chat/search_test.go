package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 搜索是真实 Telegram RPC，无法单测 mock；这些测试验证纯逻辑边界。
// 真正调通由 Task 4 端到端（临时程序或真实账号）验证。

func TestValidSearchQuery(t *testing.T) {
	// 空查询校验规则（与 spec §3.0 一致）：去 # 后仍有非空白字符才有效
	cases := []struct {
		q    string
		want bool
	}{
		{"#", false},
		{"##", false},
		{"# ", false},
		{"# #", false},
		{"", false},
		{"   ", false},
		{"#分", true},
		{"#分享", true},
		{"# 分", true},
		{"#💩", true},
		{"分享", true},
		{"分", true},
	}
	for _, c := range cases {
		got := ValidSearchQuery(c.q)
		require.Equal(t, c.want, got, "q=%q", c.q)
	}
}
