package webapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	webassets "tadmin/assets/web"
	"tadmin/internal/store"
	"tadmin/internal/verify"
)

const apiBodyLimit = 32 << 10

type Handler struct {
	service        *verify.Service
	sessionLimiter *fixedWindowLimiter
	clientLimiter  *fixedWindowLimiter
	notifyTerminal func()
	imageSlots     chan struct{}
}

type openRequest struct {
	InitData string `json:"init_data"`
}

type answerRequest struct {
	InitData   string         `json:"init_data"`
	Generation int            `json:"generation"`
	Clicks     []verify.Point `json:"clicks"`
}

type apiView struct {
	Status            string `json:"status"`
	ServerTime        int64  `json:"server_time"`
	Language          string `json:"language"`
	Prompt            string `json:"prompt,omitempty"`
	Generation        int    `json:"generation,omitempty"`
	ExpiresAt         int64  `json:"expires_at"`
	RemainingAttempts int    `json:"remaining_attempts"`
	ImageURL          string `json:"image_url,omitempty"`
}

func New(service *verify.Service, notifyTerminal ...func()) *Handler {
	handler := &Handler{
		service:        service,
		sessionLimiter: newFixedWindowLimiter(time.Minute, 40, 4096),
		clientLimiter:  newFixedWindowLimiter(time.Minute, 240, 1024),
		imageSlots:     make(chan struct{}, 2),
	}
	if len(notifyTerminal) > 0 {
		handler.notifyTerminal = notifyTerminal[0]
	}
	return handler
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header())
	response.Header().Set("Cache-Control", "no-store")

	segments := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	switch {
	case len(segments) == 3 && segments[0] == "verify" && segments[1] == "assets":
		h.serveAsset(response, request, segments[2])
	case len(segments) == 2 && segments[0] == "verify":
		h.servePage(response, request, segments[1])
	case len(segments) == 4 && segments[0] == "api" && segments[1] == "verify" && segments[3] == "open":
		h.serveOpen(response, request, segments[2])
	case len(segments) == 4 && segments[0] == "api" && segments[1] == "verify" && segments[3] == "answer":
		h.serveAnswer(response, request, segments[2])
	case len(segments) == 5 && segments[0] == "api" && segments[1] == "verify" && segments[3] == "image":
		h.serveImage(response, request, segments[2], segments[4])
	default:
		writeAPIError(response, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) serveAsset(response http.ResponseWriter, request *http.Request, name string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	var contentType string
	switch name {
	case "app.css":
		contentType = "text/css; charset=utf-8"
	case "app.js":
		contentType = "text/javascript; charset=utf-8"
	default:
		writeAPIError(response, http.StatusNotFound, "not_found")
		return
	}
	contents, err := webassets.Files.ReadFile(name)
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(contents)
}

func (h *Handler) servePage(response http.ResponseWriter, request *http.Request, token string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	if !validToken(token) {
		writeAPIError(response, http.StatusNotFound, "invalid_session")
		return
	}
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	contents, err := webassets.Files.ReadFile("index.html")
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(contents)
}

func (h *Handler) serveOpen(response http.ResponseWriter, request *http.Request, token string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(response, http.MethodPost)
		return
	}
	if !h.prepareAPI(response, request, token) {
		return
	}
	var body openRequest
	if err := decodeJSON(response, request, &body); err != nil {
		writeDecodeError(response, err)
		return
	}
	view, err := h.service.Open(request.Context(), token, body.InitData)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, viewResponse(token, view))
}

func (h *Handler) serveAnswer(response http.ResponseWriter, request *http.Request, token string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(response, http.MethodPost)
		return
	}
	if !h.prepareAPI(response, request, token) {
		return
	}
	var body answerRequest
	if err := decodeJSON(response, request, &body); err != nil {
		writeDecodeError(response, err)
		return
	}
	result, err := h.service.Answer(request.Context(), token, body.InitData, body.Generation, body.Clicks)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if result.View.Status != store.VerificationChallengeIssued && h.notifyTerminal != nil {
		h.notifyTerminal()
	}
	writeJSON(response, http.StatusOK, viewResponse(token, result.View))
}

func (h *Handler) serveImage(response http.ResponseWriter, request *http.Request, token, generationFile string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	if h == nil || h.service == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "service_unavailable")
		return
	}
	if !validToken(token) || request.URL.RawQuery != "" || !h.allowAPIRequest(request, token) {
		if !validToken(token) {
			writeAPIError(response, http.StatusNotFound, "invalid_session")
		} else if request.URL.RawQuery != "" {
			writeAPIError(response, http.StatusBadRequest, "invalid_request")
		} else {
			response.Header().Set("Retry-After", "60")
			writeAPIError(response, http.StatusTooManyRequests, "rate_limited")
		}
		return
	}
	if !strings.HasSuffix(generationFile, ".png") {
		writeAPIError(response, http.StatusNotFound, "not_found")
		return
	}
	generation, err := strconv.Atoi(strings.TrimSuffix(generationFile, ".png"))
	if err != nil || generation < 1 {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	// Bound PNG allocation/CPU under concurrent image requests.
	select {
	case h.imageSlots <- struct{}{}:
		defer func() { <-h.imageSlots }()
	case <-request.Context().Done():
		writeAPIError(response, http.StatusServiceUnavailable, "request_cancelled")
		return
	}
	contents, err := h.service.Image(request.Context(), token, generation)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Content-Type", "image/png")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(contents)
}

func (h *Handler) prepareAPI(response http.ResponseWriter, request *http.Request, token string) bool {
	if h == nil || h.service == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "service_unavailable")
		return false
	}
	if !validToken(token) {
		writeAPIError(response, http.StatusNotFound, "invalid_session")
		return false
	}
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return false
	}
	if !h.allowAPIRequest(request, token) {
		response.Header().Set("Retry-After", "60")
		writeAPIError(response, http.StatusTooManyRequests, "rate_limited")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "content_type_required")
		return false
	}
	return true
}

func (h *Handler) allowAPIRequest(request *http.Request, token string) bool {
	now := time.Now()
	client := request.RemoteAddr
	if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		client = host
	}
	return h.sessionLimiter.allow(token, now) && h.clientLimiter.allow(client, now)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, apiBodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func viewResponse(token string, view verify.View) apiView {
	response := apiView{
		Status:            view.Status,
		ServerTime:        time.Now().Unix(),
		Language:          view.Language,
		Prompt:            view.Prompt,
		Generation:        view.Generation,
		ExpiresAt:         view.ExpiresAt.Unix(),
		RemainingAttempts: view.RemainingAttempts,
	}
	if view.Status == store.VerificationChallengeIssued {
		response.ImageURL = fmt.Sprintf("/api/verify/%s/image/%d.png", token, view.Generation)
	}
	return response
}

func validToken(token string) bool {
	if len(token) < 22 || len(token) > 128 {
		return false
	}
	for _, character := range token {
		asciiLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		asciiDigit := character >= '0' && character <= '9'
		if !asciiLetter && !asciiDigit && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func writeDecodeError(response http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeAPIError(response, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	writeAPIError(response, http.StatusBadRequest, "invalid_request")
}

func writeServiceError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrVerificationNotFound):
		writeAPIError(response, http.StatusNotFound, "invalid_session")
	case errors.Is(err, verify.ErrInitDataInvalid), errors.Is(err, verify.ErrInitDataExpired), errors.Is(err, verify.ErrInitDataFuture), errors.Is(err, verify.ErrSessionIdentity):
		writeAPIError(response, http.StatusUnauthorized, "authorization_failed")
	case errors.Is(err, store.ErrVerificationExpired):
		writeAPIError(response, http.StatusGone, "expired")
	case errors.Is(err, store.ErrVerificationStale):
		writeAPIError(response, http.StatusConflict, "stale_challenge")
	case errors.Is(err, store.ErrVerificationCompleted):
		writeAPIError(response, http.StatusConflict, "already_completed")
	case errors.Is(err, store.ErrVerificationReview):
		writeAPIError(response, http.StatusConflict, "awaiting_review")
	case errors.Is(err, store.ErrVerificationCancelled):
		writeAPIError(response, http.StatusGone, "cancelled")
	case errors.Is(err, verify.ErrInvalidCoordinates):
		writeAPIError(response, http.StatusBadRequest, "invalid_coordinates")
	case errors.Is(err, store.ErrVerificationInvalidState), errors.Is(err, verify.ErrInvalidSession):
		writeAPIError(response, http.StatusConflict, "invalid_state")
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
	}
}

func methodNotAllowed(response http.ResponseWriter, allowed string) {
	response.Header().Set("Allow", allowed)
	writeAPIError(response, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeAPIError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"error": code})
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func setSecurityHeaders(headers http.Header) {
	headers.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://telegram.org; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}
