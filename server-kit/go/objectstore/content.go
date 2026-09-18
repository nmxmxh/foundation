package objectstore

import (
	"bytes"
	"errors"
	"io"
	"strings"
)

// ErrContentMismatch means the uploaded bytes are not the declared type.
var ErrContentMismatch = errors.New("objectstore: content does not match declared type")

// ErrContentTypeUnsupported means SniffUpload has no signature for the type.
var ErrContentTypeUnsupported = errors.New("objectstore: content type has no known signature")

// sniffLength is how many leading bytes SniffUpload reads. Every signature
// below sits in the first 12 bytes; 512 matches net/http sniffing.
const sniffLength = 512

// NormalizeContentType lowercases a declared type, drops parameters and maps
// common aliases, so "image/JPG; charset=x" compares as "image/jpeg".
func NormalizeContentType(value string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch ct {
	case "image/jpg", "image/pjpeg":
		return "image/jpeg"
	case "video/x-m4v":
		return "video/mp4"
	}
	return ct
}

// SniffUpload checks the leading bytes of body against the declared content
// type and returns a reader that yields the complete body again.
//
// Upload handlers must not trust a client Content-Type. A file declared as
// image/png that holds HTML or a script is stored and later served under that
// type. Magic-byte checks stop the mismatch at the edge. Only the types listed
// in signatureMatches are supported; any other type returns
// ErrContentTypeUnsupported so a caller cannot skip the check by accident.
func SniffUpload(body io.Reader, declared string) (io.Reader, error) {
	ct := NormalizeContentType(declared)
	if _, ok := signatures[ct]; !ok {
		return nil, ErrContentTypeUnsupported
	}
	head := make([]byte, sniffLength)
	n, err := io.ReadFull(body, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	head = head[:n]
	if !signatures[ct](head) {
		return nil, ErrContentMismatch
	}
	return io.MultiReader(bytes.NewReader(head), body), nil
}

// SniffSupported reports whether SniffUpload can verify a content type.
func SniffSupported(contentType string) bool {
	_, ok := signatures[NormalizeContentType(contentType)]
	return ok
}

var signatures = map[string]func([]byte) bool{
	"image/jpeg":      prefix("\xFF\xD8\xFF"),
	"image/png":       prefix("\x89PNG\r\n\x1a\n"),
	"image/gif":       func(b []byte) bool { return prefix("GIF87a")(b) || prefix("GIF89a")(b) },
	"image/webp":      func(b []byte) bool { return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP" },
	"application/pdf": prefix("%PDF-"),
	// ISO base media (MP4 and QuickTime) carries an "ftyp" box at offset 4.
	"video/mp4":       isoBaseMedia,
	"video/quicktime": func(b []byte) bool { return isoBaseMedia(b) || (len(b) >= 8 && string(b[4:8]) == "moov") },
	"video/webm":      prefix("\x1A\x45\xDF\xA3"),
}

func prefix(sig string) func([]byte) bool {
	return func(b []byte) bool { return bytes.HasPrefix(b, []byte(sig)) }
}

func isoBaseMedia(b []byte) bool {
	return len(b) >= 12 && string(b[4:8]) == "ftyp"
}
