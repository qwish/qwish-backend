package upload

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Real file headers must sniff to a type their kind accepts.
func TestSniffedTypesAreAccepted(t *testing.T) {
	cases := []struct {
		kind  mediaKind
		bytes string
	}{
		{imageKind, "GIF89a\x01\x00\x01\x00"},
		{imageKind, "RIFF\x24\x00\x00\x00WEBPVP8 "},
		{videoKind, "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"},
		{videoKind, "\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\xf7\x81\x01\x42\xf2\x81\x04\x42\xf3\x81\x08\x42\x82\x84webm"},
	}
	for _, c := range cases {
		if ct := http.DetectContentType([]byte(c.bytes)); !c.kind.types[ct] {
			t.Errorf("%q sniffed as %s, rejected by %s", c.bytes[:8], ct, c.kind.label)
		}
	}
}

func post(t *testing.T, h http.HandlerFunc, body []byte) int {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "f")
	fw.Write(body)
	mw.Close()
	r := httptest.NewRequest("POST", "/", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h(w, r)
	return w.Code
}

// Oversized and wrong-type files are refused before any storage call.
func TestUploadRejects(t *testing.T) {
	h := &Handler{} // nil S3: reaching storage would panic
	gif := append([]byte("GIF89a"), make([]byte, maxImageSize)...)
	if code := post(t, h.UploadImage, gif); code != http.StatusBadRequest {
		t.Errorf("2MB+ image: %d", code)
	}
	if code := post(t, h.UploadImage, []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom")); code != http.StatusBadRequest {
		t.Errorf("video on image route: %d", code)
	}
	if code := post(t, h.UploadVideo, []byte("GIF89a\x01\x00\x01\x00")); code != http.StatusBadRequest {
		t.Errorf("image on video route: %d", code)
	}
}
