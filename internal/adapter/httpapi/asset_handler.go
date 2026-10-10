package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	assets, err := s.Assets.List(r.Context(), usecase.ListOptions{Query: q.Get("q"), Sort: q.Get("sort"), Order: q.Get("order")})
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]assetDTO, 0, len(assets))
	for _, a := range assets {
		items = append(items, assetResponse(a))
	}
	writeJSON(w, 200, struct {
		Items []assetDTO `json:"items"`
		Total int        `json:"total"`
	}{items, len(items)})
}
func (s *Server) createAsset(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeObject(w, r, "code")
	var code string
	if err == nil {
		if json.Unmarshal(fields["code"], &code) != nil {
			err = usecase.Invalid("code", "文字列を指定してください。")
		}
	}
	if err != nil {
		writeError(w, err)
		return
	}
	a, err := s.Assets.Create(r.Context(), code)
	if err != nil {
		writeError(w, err)
		return
	}
	response := assetResponse(a)
	w.Header().Set("Location", "/api/v1/assets/"+response.ID)
	writeJSON(w, 201, response)
}
func (s *Server) updateAsset(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	fields, err := decodeObject(w, r, "monitoring_enabled")
	var enabled bool
	if err == nil {
		if json.Unmarshal(fields["monitoring_enabled"], &enabled) != nil {
			err = usecase.Invalid("monitoring_enabled", "真偽値を指定してください。")
		}
	}
	if err != nil {
		writeError(w, err)
		return
	}
	a, err := s.Assets.SetMonitoring(r.Context(), id, enabled)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, assetResponse(a))
}
func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err == nil {
		err = s.Assets.Delete(r.Context(), id)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
