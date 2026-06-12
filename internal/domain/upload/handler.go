package upload

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/qwish/backend/internal/middleware"
	"github.com/qwish/backend/internal/storage"
)

const maxUploadSize = 5 << 20 // 5MB

type Handler struct {
	r2 *storage.R2Client
}

func NewHandler(r2 *storage.R2Client) *Handler {
	return &Handler{r2: r2}
}

type presignReq struct {
	ContentType string `json:"content_type"`
	Prefix      string `json:"prefix"`
}

// POST /api/v1/upload/presign
func (h *Handler) PresignUpload(w http.ResponseWriter, r *http.Request) {
	var req presignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	switch req.ContentType {
	case "image/jpeg", "image/png", "image/webp":
		// allowed
	default:
		middleware.BadRequest(w, "only JPEG, PNG, and WebP images are allowed")
		return
	}

	if req.Prefix == "" {
		req.Prefix = "quiz-images"
	}

	uploadURL, publicURL, key, err := h.r2.PresignUpload(r.Context(), req.Prefix, req.ContentType, 5*time.Minute)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		middleware.BadRequest(w, "file too large (max 5MB)")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		middleware.BadRequest(w, "file field is required")
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		// allowed
	default:
		middleware.BadRequest(w, "only JPEG, PNG, and WebP images are allowed")
		return
	}

	prefix := r.FormValue("prefix")
	if prefix == "" {
		prefix = "quiz-images"
	}

	url, err := h.r2.Upload(r.Context(), prefix, contentType, file, header.Size)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusCreated, map[string]string{"url": url})
}
