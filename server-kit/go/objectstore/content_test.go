package objectstore

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSniffUploadAcceptsRealSignatures(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":      "\xFF\xD8\xFF\xE0rest",
		"image/JPG":       "\xFF\xD8\xFF\xE0rest",
		"image/png":       "\x89PNG\r\n\x1a\nrest",
		"image/gif":       "GIF89arest",
		"image/webp":      "RIFF\x00\x00\x00\x00WEBPVP8 ",
		"application/pdf": "%PDF-1.7\n",
		"video/mp4":       "\x00\x00\x00\x18ftypmp42rest",
		"video/quicktime": "\x00\x00\x00\x14ftypqt  rest",
		"video/webm":      "\x1A\x45\xDF\xA3rest",
		"audio/ogg":       "OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00",
		"audio/mpeg":      "ID3\x03\x00\x00\x00\x00\x00\x00",
		"audio/mp3":       "\xFF\xFB\x90\x64\x00\x00\x00\x00",
		"audio/wav":       "RIFF\x24\x00\x00\x00WAVEfmt ",
		"audio/aac":       "\xFF\xF1\x50\x80\x00\x00\x00\x00",
		"application/zip": "PK\x03\x04\x14\x00\x00\x00\x08\x00",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "PK\x03\x04\x14\x00\x00\x00\x08\x00",
	}
	for declared, body := range cases {
		reader, err := SniffUpload(strings.NewReader(body), declared)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", declared, err)
		}
		replayed, _ := io.ReadAll(reader)
		if string(replayed) != body {
			t.Fatalf("%s: body not replayed intact", declared)
		}
	}
}

func TestSniffUploadRefusesMismatchAndUnknown(t *testing.T) {
	if _, err := SniffUpload(strings.NewReader("<html><script>alert(1)</script>"), "image/png"); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("html declared as png: got %v, want ErrContentMismatch", err)
	}
	if _, err := SniffUpload(strings.NewReader(""), "image/jpeg"); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("empty body: got %v, want ErrContentMismatch", err)
	}
	if _, err := SniffUpload(strings.NewReader("anything"), "text/html"); !errors.Is(err, ErrContentTypeUnsupported) {
		t.Fatalf("unsupported type: got %v, want ErrContentTypeUnsupported", err)
	}
	if !SniffSupported("video/mp4") || SniffSupported("text/plain") {
		t.Fatal("SniffSupported disagrees with the signature table")
	}
}

func TestSniffUploadRejectsScriptCapableTypes(t *testing.T) {
	for _, declared := range []string{
		"image/svg+xml", "text/html", "text/xml", "application/xml",
	} {
		if _, err := SniffUpload(strings.NewReader("<svg onload=alert(1)></svg>"), declared); !errors.Is(err, ErrContentTypeUnsupported) {
			t.Errorf("%s: error = %v, want ErrContentTypeUnsupported", declared, err)
		}
		if SniffSupported(declared) {
			t.Errorf("%s: must not report sniff support", declared)
		}
	}
}
