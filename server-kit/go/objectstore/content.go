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
	case "audio/mp3":
		return "audio/mpeg"
	case "audio/x-wav":
		return "audio/wav"
	case "audio/opus":
		return "audio/ogg"
	case "audio/m4a", "audio/x-m4a":
		return "audio/mp4"
	case "application/x-zip-compressed":
		return "application/zip"
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
	// Audio formats
	"audio/ogg":  prefix("OggS"),
	"audio/mpeg": func(b []byte) bool { return prefix("ID3")(b) || (len(b) >= 2 && b[0] == 0xFF && (b[1]&0xE0) == 0xE0) },
	"audio/wav":  func(b []byte) bool { return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WAVE" },
	"audio/aac":  func(b []byte) bool { return len(b) >= 2 && b[0] == 0xFF && (b[1]&0xF6) == 0xF0 },
	"audio/mp4":  isoBaseMedia,
	// Zip & Office OpenXML documents
	"application/zip": prefix("PK\x03\x04"),
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   prefix("PK\x03\x04"),
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         prefix("PK\x03\x04"),
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": prefix("PK\x03\x04"),
}

func prefix(sig string) func([]byte) bool {
	return func(b []byte) bool { return bytes.HasPrefix(b, []byte(sig)) }
}

func isoBaseMedia(b []byte) bool {
	return len(b) >= 12 && string(b[4:8]) == "ftyp"
}
