package chat

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDialogInfoReadInboxMaxID(t *testing.T) {
	// DialogInfo 结构有 ReadInboxMaxID 字段（编译期验证）
	var d DialogInfo
	d.ReadInboxMaxID = 12345
	require.Equal(t, 12345, d.ReadInboxMaxID)
	// JSON tag 与前端约定一致（readInboxMaxId,omitempty）
	f, ok := reflect.TypeOf(d).FieldByName("ReadInboxMaxID")
	require.True(t, ok)
	require.Equal(t, "readInboxMaxId,omitempty", f.Tag.Get("json"))
}
