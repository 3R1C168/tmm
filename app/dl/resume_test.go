package dl

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/tmessage"
)

// TestFingerprint_OrderIndependent verifies the fingerprint is normalized: the
// same set of (peer, message) pairs yields the same fingerprint regardless of
// message ordering (e.g. --desc) or input grouping.
func TestFingerprint_OrderIndependent(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 123, AccessHash: 1}

	asc := []*tmessage.Dialog{{Peer: peer, Messages: []int{1, 2, 3, 10}}}
	desc := []*tmessage.Dialog{{Peer: peer, Messages: []int{10, 3, 2, 1}}}
	shuffled := []*tmessage.Dialog{{Peer: peer, Messages: []int{3, 1, 10, 2}}}

	require.Equal(t, fingerprint(asc), fingerprint(desc), "desc order must not change fingerprint")
	require.Equal(t, fingerprint(asc), fingerprint(shuffled), "shuffled order must not change fingerprint")
}

// TestFingerprint_Dedup verifies duplicate message IDs collapse to the same
// fingerprint as the de-duplicated set.
func TestFingerprint_Dedup(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 123, AccessHash: 1}

	withDup := []*tmessage.Dialog{{Peer: peer, Messages: []int{1, 2, 2, 3, 3, 3}}}
	noDup := []*tmessage.Dialog{{Peer: peer, Messages: []int{1, 2, 3}}}

	require.Equal(t, fingerprint(noDup), fingerprint(withDup))
}

// TestFingerprint_DifferentContent ensures different message sets produce
// different fingerprints (resume keys must not collide).
func TestFingerprint_DifferentContent(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 123, AccessHash: 1}

	a := []*tmessage.Dialog{{Peer: peer, Messages: []int{1, 2, 3}}}
	b := []*tmessage.Dialog{{Peer: peer, Messages: []int{1, 2, 4}}}

	require.NotEqual(t, fingerprint(a), fingerprint(b))
}

// TestProgressRoundTrip verifies marshal/unmarshal of the order-independent
// resume record preserves the finished message keys.
func TestProgressRoundTrip(t *testing.T) {
	finished := map[MsgKey]struct{}{
		{PeerID: 123, MsgID: 1}: {},
		{PeerID: 123, MsgID: 5}: {},
		{PeerID: 456, MsgID: 9}: {},
	}

	b, err := marshalProgress(finished)
	require.NoError(t, err)

	got, err := unmarshalProgress(b)
	require.NoError(t, err)
	require.Equal(t, finished, got)
}

// TestUnmarshalProgress_Legacy verifies the legacy logical-position format is
// tolerated (ignored) rather than crashing resume.
func TestUnmarshalProgress_Legacy(t *testing.T) {
	// legacy format: JSON object mapping logical positions to {}
	legacy := []byte(`{"0":{},"1":{},"5":{}}`)

	got, err := unmarshalProgress(legacy)
	require.NoError(t, err)
	require.Empty(t, got, "legacy positions cannot be mapped to message keys")
}

// TestUnmarshalProgress_Garbage ensures invalid data errors out cleanly.
func TestUnmarshalProgress_Garbage(t *testing.T) {
	_, err := unmarshalProgress([]byte(`{not json`))
	require.Error(t, err)
}
