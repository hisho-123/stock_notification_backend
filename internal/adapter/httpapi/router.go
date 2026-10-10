package httpapi

import (
	"net/http"
	"net/netip"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type Server struct {
	Assets                  usecase.Assets
	Settings                usecase.Settings
	Auth                    usecase.Auth
	Clock                   usecase.Clock
	PublicOrigin, StaticDir string
	TrustedProxies          []netip.Prefix
	limiter                 *attemptLimiter
}

func (s *Server) Handler() http.Handler {
	s.limiter = &attemptLimiter{failures: make(map[string][]time.Time)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/auth/session", s.getSession)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/assets", s.listAssets)
	mux.HandleFunc("POST /api/v1/assets", s.createAsset)
	mux.HandleFunc("PATCH /api/v1/assets/{id}", s.updateAsset)
	mux.HandleFunc("DELETE /api/v1/assets/{id}", s.deleteAsset)
	mux.HandleFunc("GET /api/v1/settings/notifications", s.getSettings)
	mux.HandleFunc("PUT /api/v1/settings/notifications", s.updateSettings)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.Handle("/", staticHandler(s.StaticDir))
	timeoutBody := `{"error":{"code":"SERVICE_UNAVAILABLE","message":"処理を完了できませんでした。再取得して確認してください。","fields":{}}}`
	return responseHeaders(http.TimeoutHandler(s.recover(s.guard(mux)), 90*time.Second, timeoutBody))
}
