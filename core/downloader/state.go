package downloader

import (
	"encoding/base64"
	"encoding/json"
	"math/bits"
	"os"
	"sync"

	"github.com/go-faster/errors"
)

// stateExt is the suffix of the per-file control file that records which
// pieces of a partial download have already been written, so an interrupted
// download can be resumed block-by-block instead of from scratch.
const stateExt = ".tdlstate"

// stateVersion is the on-disk schema version of the control file.
const stateVersion = 1

// State tracks per-piece completion of a single file download and persists it
// to a control file next to the data file (like aria2's .aria2 file).
//
// Pieces are partSize bytes each. The bitmap is the source of truth: the data
// file's size alone is not reliable because pieces may be written out of order
// by concurrent workers, leaving zero-filled holes.
type State struct {
	mu sync.Mutex

	path     string // path of the control file
	size     int64  // total file size in bytes
	partSize int    // bytes per piece
	bits     []uint64
	done     int // number of completed pieces
	dirty    bool
}

type stateFile struct {
	Version  int    `json:"version"`
	Size     int64  `json:"size"`
	PartSize int    `json:"part_size"`
	Bitmap   string `json:"bitmap"` // base64-encoded little-endian []uint64
}

func numPieces(size int64, partSize int) int {
	return int((size + int64(partSize) - 1) / int64(partSize))
}

// NewState creates a State for a fresh download (no pieces completed yet).
// The control file is only written on Flush.
func NewState(path string, size int64, partSize int) *State {
	n := numPieces(size, partSize)
	return &State{
		path:     path,
		size:     size,
		partSize: partSize,
		bits:     make([]uint64, (n+63)/64),
	}
}

// StatePath returns the control file path for a given data file path.
func StatePath(dataPath string) string {
	return dataPath + stateExt
}

// LoadState reads the control file for dataPath. It returns (nil, nil) when no
// control file exists. It returns an error when the file exists but is corrupt
// or does not match the expected size/partSize, in which case the caller should
// restart the download from scratch.
func LoadState(dataPath string, size int64, partSize int) (*State, error) {
	path := StatePath(dataPath)

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "read state")
	}

	var sf stateFile
	if err = json.Unmarshal(raw, &sf); err != nil {
		return nil, errors.Wrap(err, "decode state")
	}

	if sf.Version != stateVersion {
		return nil, errors.Errorf("unsupported state version %d", sf.Version)
	}
	if sf.Size != size || sf.PartSize != partSize {
		return nil, errors.Errorf("state mismatch (size %d/%d, part %d/%d)",
			sf.Size, size, sf.PartSize, partSize)
	}

	bm, err := base64.StdEncoding.DecodeString(sf.Bitmap)
	if err != nil {
		return nil, errors.Wrap(err, "decode bitmap")
	}

	n := numPieces(size, partSize)
	words := (n + 63) / 64
	if len(bm) != words*8 {
		return nil, errors.Errorf("bitmap length %d, want %d", len(bm), words*8)
	}

	s := &State{
		path:     path,
		size:     size,
		partSize: partSize,
		bits:     make([]uint64, words),
	}
	for i := range s.bits {
		s.bits[i] = uint64(bm[i*8]) |
			uint64(bm[i*8+1])<<8 |
			uint64(bm[i*8+2])<<16 |
			uint64(bm[i*8+3])<<24 |
			uint64(bm[i*8+4])<<32 |
			uint64(bm[i*8+5])<<40 |
			uint64(bm[i*8+6])<<48 |
			uint64(bm[i*8+7])<<56
	}
	for _, w := range s.bits {
		s.done += bits.OnesCount64(w)
	}

	return s, nil
}

// Mark records the byte range [off, off+len) as written, marking any piece
// fully covered by written ranges as complete. It returns the total number of
// completed bytes so far (for progress reporting).
func (s *State) Mark(off int64, n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	first := int(off / int64(s.partSize))
	last := int((off + int64(n) - 1) / int64(s.partSize))
	for p := first; p <= last; p++ {
		w := uint(p) / 64
		b := uint(p) % 64
		if s.bits[w]&(1<<b) == 0 {
			s.bits[w] |= 1 << b
			s.done++
		}
	}
	s.dirty = true
	s.mu.Unlock()
}

// Complete reports whether every piece has been written.
func (s *State) Complete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done == numPieces(s.size, s.partSize)
}

// pieceRange is a half-open byte range [Off, End) of one piece.
type pieceRange struct {
	Off int64
	End int64
}

// Missing returns the byte ranges of all incomplete pieces, in order.
func (s *State) Missing() []pieceRange {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := numPieces(s.size, s.partSize)
	out := make([]pieceRange, 0, n-s.done)
	for p := 0; p < n; p++ {
		if s.bits[uint(p)/64]&(1<<(uint(p)%64)) != 0 {
			continue
		}
		off := int64(p) * int64(s.partSize)
		end := off + int64(s.partSize)
		if end > s.size {
			end = s.size
		}
		out = append(out, pieceRange{Off: off, End: end})
	}
	return out
}

// Flush writes the current bitmap to the control file if it changed since the
// last flush. It is a no-op when nothing new was marked.
func (s *State) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}

	bm := make([]byte, len(s.bits)*8)
	for i, w := range s.bits {
		bm[i*8] = byte(w)
		bm[i*8+1] = byte(w >> 8)
		bm[i*8+2] = byte(w >> 16)
		bm[i*8+3] = byte(w >> 24)
		bm[i*8+4] = byte(w >> 32)
		bm[i*8+5] = byte(w >> 40)
		bm[i*8+6] = byte(w >> 48)
		bm[i*8+7] = byte(w >> 56)
	}

	sf := stateFile{
		Version:  stateVersion,
		Size:     s.size,
		PartSize: s.partSize,
		Bitmap:   base64.StdEncoding.EncodeToString(bm),
	}

	raw, err := json.Marshal(sf)
	if err != nil {
		return errors.Wrap(err, "encode state")
	}

	if err = os.WriteFile(s.path, raw, 0o644); err != nil {
		return errors.Wrap(err, "write state")
	}

	s.dirty = false
	return nil
}

// Remove deletes the control file. Called after the download completes and the
// data file has been finalized.
func (s *State) Remove() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "remove state")
	}
	return nil
}
