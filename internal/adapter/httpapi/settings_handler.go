package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Settings.Get(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, settingsResponse(settings))
}
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeObject(w, r, "thresholds")
	var values []int
	if err == nil {
		var elements []json.RawMessage
		if json.Unmarshal(fields["thresholds"], &elements) != nil {
			err = usecase.Invalid("thresholds", "整数の配列を指定してください。")
		} else {
			for _, raw := range elements {
				var value int
				if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &value) != nil {
					err = usecase.Invalid("thresholds", "整数の配列を指定してください。")
					break
				}
				values = append(values, value)
			}
		}
	}
	if err != nil {
		writeError(w, err)
		return
	}
	settings, err := s.Settings.Update(r.Context(), values)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, settingsResponse(settings))
}
