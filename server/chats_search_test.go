package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// 搜索 handler 的纯逻辑测试（不连 telegram——activeAccount 返回 nil 走 401 分支）
func TestHandleChatSearchNoAccount(t *testing.T) {
	s := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/chats/1/search?q=%23分享", nil)
	w := httptest.NewRecorder()
	s.handleChatSearch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code) // 未登录
}
