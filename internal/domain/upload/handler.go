package upload

import (
	"io"
	"net/http"
	"time"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
	"github.com/qwish/backend/internal/storage"
)

const (
	maxImageSize = 2 << 20  // 2MB
	maxVideoSize = 25 << 20 // 25MB
	// formOverhead covers multipart boundaries and the prefix field.
	formOverhead = 64 << 10
)

// mediaKind is one uploadable class of file: what the bytes may be, how big,
// and the closed set of storage folders it may land in.
type mediaKind struct {
	types    map[string]bool
	maxSize  int64
	prefixes []string // first is the default
	label    string   // for error messages
}

var (
	imageKind = mediaKind{
		types:    map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true},
		maxSize:  maxImageSize,
		prefixes: []string{"quiz-images"},
		label:    "JPEG, PNG, WebP or GIF images up to 2MB",
	}
	videoKind = mediaKind{
		types:    map[string]bool{"video/mp4": true, "video/webm": true},
		maxSize:  maxVideoSize,
		prefixes: []string{"quiz-videos"},
		label:    "MP4 or WebM videos up to 25MB",
	}
)

func (k mediaKind) prefix(p string) (string, bool) {
	if p == "" {
		return k.prefixes[0], true
	}
	for _, allowed := range k.prefixes {
		if p == allowed {
			return p, true
		}
	}
	return "", false
}

type Handler struct {
	s3 *storage.S3Client
}

func NewHandler(client *storage.S3Client) *Handler {
	return &Handler{s3: client}
}

type presignReq struct {
	ContentType string `json:"content_type"`
	Prefix      string `json:"prefix"`
	Size        int64  `json:"size"`
}

// POST /api/v1/upload/presign
func (h *Handler) PresignUpload(w http.ResponseWriter, r *http.Request) {
	var req presignReq
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.Size <= 0 || req.Size > imageKind.maxSize || !imageKind.types[req.ContentType] {
		middleware.BadRequest(w, "size is required; only "+imageKind.label+" are allowed")
		return
	}

	prefix, ok := imageKind.prefix(req.Prefix)
	if !ok {
		middleware.BadRequest(w, "invalid prefix")
		return
	}

	uploadURL, publicURL, key, err := h.s3.PresignUpload(r.Context(), prefix, req.ContentType, req.Size, 5*time.Minute)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"upload_url": uploadURL,
		"public_url": publicURL,
		"key":        key,
		"expires_in": 300,
	})
}

// POST /api/v1/upload/image
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	h.upload(w, r, imageKind)
}

// POST /api/v1/upload/video
func (h *Handler) UploadVideo(w http.ResponseWriter, r *http.Request) {
	h.upload(w, r, videoKind)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request, kind mediaKind) {
	tooLarge := "only " + kind.label + " are allowed"
	r.Body = http.MaxBytesReader(w, r.Body, kind.maxSize+formOverhead)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		middleware.BadRequest(w, tooLarge)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		middleware.BadRequest(w, "file field is required")
		return
	}
	defer file.Close()
	if header.Size > kind.maxSize {
		middleware.BadRequest(w, tooLarge)
		return
	}

	// Trust the bytes, not the client's declared type.
	sniff := make([]byte, 512)
	n, _ := io.ReadFull(file, sniff)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		middleware.InternalError(w)
		return
	}
	contentType := http.DetectContentType(sniff[:n])
	if !kind.types[contentType] {
		middleware.BadRequest(w, tooLarge)
		return
	}

	prefix, ok := kind.prefix(r.FormValue("prefix"))
	if !ok {
		middleware.BadRequest(w, "invalid prefix")
		return
	}

	url, err := h.s3.Upload(r.Context(), prefix, contentType, file, header.Size)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusCreated, map[string]string{"url": url})
}
