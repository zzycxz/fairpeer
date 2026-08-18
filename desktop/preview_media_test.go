package main

import "testing"

func TestPreviewMediaKind(t *testing.T) {
	cases := []struct {
		path  string
		kind  string
		mimes string
	}{
		{"a.png", "image", "image/png"},
		{"a.jpg", "image", "image/jpeg"},
		{"a.svg", "image", "image/svg+xml"},
		{"a.pdf", "pdf", "application/pdf"},
		{"a.html", "html", "text/html"},
		{"a.htm", "html", "text/html"},
		{"a.mp3", "audio", "audio/mpeg"},
		{"a.wav", "audio", "audio/wav"},
		{"a.flac", "audio", "audio/flac"},
		{"a.m4a", "audio", "audio/mp4"},
		{"a.MP3", "audio", "audio/mpeg"}, // case-insensitive ext
		{"a.mp4", "video", "video/mp4"},
		{"a.webm", "video", "video/webm"},
		{"a.mkv", "video", "video/x-matroska"},
		{"a.mov", "video", "video/quicktime"},
		{"a.txt", "", ""},
		{"a.docx", "", ""}, // office docs extract as text, not media
		{"a.md", "", ""},
		{"noext", "", ""},
	}
	for _, tc := range cases {
		kind, mime := previewMediaKind(tc.path)
		if kind != tc.kind || mime != tc.mimes {
			t.Errorf("previewMediaKind(%q) = (%q, %q), want (%q, %q)", tc.path, kind, mime, tc.kind, tc.mimes)
		}
	}
}
