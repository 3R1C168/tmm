package downloader

import (
	"context"
	"io"

	"github.com/gotd/td/tg"
)

type Iter interface {
	Next(ctx context.Context) bool
	Value() Elem
	Err() error
}

type Elem interface {
	File() File
	To() io.WriterAt

	AsTakeout() bool

	// Resume returns the per-piece download state to resume from, or nil for a
	// fresh download. When non-nil, only the missing pieces are fetched.
	Resume() *State
}

type File interface {
	Location() tg.InputFileLocationClass
	Size() int64
	DC() int
}
