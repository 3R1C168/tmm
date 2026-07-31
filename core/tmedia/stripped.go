package tmedia

import (
	"encoding/base64"
	"encoding/hex"

	"github.com/gotd/td/tg"
)

// StrippedThumb is a message's inline low-res preview: the raw JPEG bytes
// (already expanded from Telegram's stripped format) plus natural dimensions.
// The bytes are small (~a few hundred bytes) and travel inside the message,
// so rendering it needs zero network — telegram-tt shows this blurred
// placeholder instantly, then cross-fades to the real thumbnail.
type StrippedThumb struct {
	DataURI string // data:image/jpeg;base64,...
	W       int
	H       int
}

// jpegHeader is the fixed JFIF/quantization/huffman header Telegram strips
// from inline thumbnails; the image's real width/height are patched into
// bytes 164/166, then the stripped payload and an EOI marker are appended.
// Ported from telegram-tt src/lib/gramjs/Utils.ts strippedPhotoToJpg.
var jpegHeader = mustHex("ffd8ffe000104a46494600010100000100010000ffdb004300281c1e231e19282321232d2b28303c64413c37373c7b585d4964918099968f808c8aa0b4e6c3a0aadaad8a8cc8ffcbdaeef5ffffff9bc1fffffffaffe6fdfff8ffdb0043012b2d2d3c353c76414176f8a58ca5f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8ffc00011080000000003012200021101031101ffc4001f0000010501010101010100000000000000000102030405060708090a0bffc400b5100002010303020403050504040000017d01020300041105122131410613516107227114328191a1082342b1c11552d1f02433627282090a161718191a25262728292a3435363738393a434445464748494a535455565758595a636465666768696a737475767778797a838485868788898a92939495969798999aa2a3a4a5a6a7a8a9aab2b3b4b5b6b7b8b9bac2c3c4c5c6c7c8c9cad2d3d4d5d6d7d8d9dae1e2e3e4e5e6e7e8e9eaf1f2f3f4f5f6f7f8f9faffc4001f0100030101010101010101010000000000000102030405060708090a0bffc400b51100020102040403040705040400010277000102031104052131061241510761711322328108144291a1b1c109233352f0156272d10a162434e125f11718191a262728292a35363738393a434445464748494a535455565758595a636465666768696a737475767778797a82838485868788898a92939495969798999aa2a3a4a5a6a7a8a9aab2b3b4b5b6b7b8b9bac2c3c4c5c6c7c8c9cad2d3d4d5d6d7d8d9dae2e3e4e5e6e7e8e9eaf2f3f4f5f6f7f8f9faffda000c03010002110311003f00")

var jpegFooter = mustHex("ffd9")

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// strippedPhotoToJpg expands Telegram's stripped thumbnail bytes into a full
// JPEG. Returns the input unchanged if it's not in the expected format.
func strippedPhotoToJpg(stripped []byte) []byte {
	if len(stripped) < 3 || stripped[0] != 1 {
		return stripped
	}
	header := make([]byte, len(jpegHeader))
	copy(header, jpegHeader)
	header[164] = stripped[1]
	header[166] = stripped[2]
	out := make([]byte, 0, len(header)+len(stripped)-3+len(jpegFooter))
	out = append(out, header...)
	out = append(out, stripped[3:]...)
	out = append(out, jpegFooter...)
	return out
}

// extractStripped finds the PhotoStrippedSize in a list of photo sizes,
// expands it to a full JPEG, and returns a data URI plus the largest real
// size's dimensions (used for aspect-ratio layout before the real thumb
// loads). mimeType should be "image/jpeg" for photos; for documents pass the
// document's mime — non-jpeg returns the raw bytes unexpanded.
func extractStripped(sizes []tg.PhotoSizeClass, mimeType string) *StrippedThumb {
	if len(sizes) == 0 {
		return nil
	}
	var stripped *tg.PhotoStrippedSize
	var lastW, lastH int
	for _, sc := range sizes {
		switch s := sc.(type) {
		case *tg.PhotoStrippedSize:
			stripped = s
		case *tg.PhotoSize:
			lastW, lastH = s.W, s.H
		case *tg.PhotoSizeProgressive:
			if n := len(s.Sizes); n > 0 {
				// progressive sizes share the photo's dimensions; use the
				// type's W/H via the largest progressive entry if present
			}
		}
	}
	if stripped == nil || len(stripped.Bytes) == 0 {
		return nil
	}
	data := stripped.Bytes
	if mimeType == "" || mimeType == "image/jpeg" {
		data = strippedPhotoToJpg(data)
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return &StrippedThumb{
		DataURI: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
		W:       lastW,
		H:       lastH,
	}
}

// GetStrippedThumb extracts the inline stripped thumbnail from a message's
// media (photo sizes or document thumbs), ready to embed as a data URI.
func GetStrippedThumb(msg *tg.Message) *StrippedThumb {
	md, ok := msg.GetMedia()
	if !ok {
		return nil
	}
	switch mm := md.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := mm.Photo.(*tg.Photo); ok {
			return extractStripped(p.Sizes, "image/jpeg")
		}
	case *tg.MessageMediaDocument:
		if doc, ok := mm.Document.(*tg.Document); ok {
			thumbs := make([]tg.PhotoSizeClass, 0, len(doc.Thumbs))
			thumbs = append(thumbs, doc.Thumbs...)
			// The thumbnail image itself is always JPEG — even when the document
			// is a video (video/mp4). Passing the document's mime here would
			// skip the stripped→JPEG expansion and yield undecodable bytes
			// (the "blank white video preview" bug). telegram-tt expands the
			// thumb whenever it's jpeg, so we hard-code image/jpeg for thumbs.
			return extractStripped(thumbs, "image/jpeg")
		}
	}
	return nil
}
