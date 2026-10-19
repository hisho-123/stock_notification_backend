package httpapi

import (
	"errors"
	"net/http"

	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

var errMediaType = errors.New("unsupported media type")

type apiError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

func writeError(w http.ResponseWriter, err error) {
	status, code, message := 500, "INTERNAL_ERROR", "処理を完了できませんでした。"
	fields := map[string]string{}
	var validation *usecase.ValidationError
	switch {
	case errors.As(err, &validation):
		status, code, message = 400, "VALIDATION_ERROR", "入力内容を確認してください。"
		if validation.Fields != nil {
			fields = validation.Fields
		}
	case errors.Is(err, errMediaType):
		status, code, message = 415, "UNSUPPORTED_MEDIA_TYPE", "JSON形式で送信してください。"
	case errors.Is(err, usecase.ErrInvalidCredentials):
		status, code, message = 401, "INVALID_CREDENTIALS", "IDまたはパスワードが違います。"
	case errors.Is(err, usecase.ErrUnauthenticated):
		status, code, message = 401, "UNAUTHENTICATED", "ログインしてください。"
	case errors.Is(err, usecase.ErrRegistrationUnavailable):
		status, code, message = 410, "REGISTRATION_LINK_UNAVAILABLE", "登録URLを利用できません。LINEで『登録』と送信してください。"
	case errors.Is(err, usecase.ErrLoginIDExists):
		status, code, message = 409, "LOGIN_ID_ALREADY_EXISTS", "このログインIDは使用済みです。"
	case errors.Is(err, usecase.ErrInvalidLINESignature):
		status, code, message = 403, "INVALID_LINE_SIGNATURE", "LINEの署名を確認できませんでした。"
	case errors.Is(err, usecase.ErrCSRF):
		status, code, message = 403, "CSRF_FAILED", "要求元または認証トークンを確認できませんでした。"
	case errors.Is(err, usecase.ErrNotFound):
		status, code, message = 404, "ASSET_NOT_FOUND", "監視対象が見つかりません。"
	case errors.Is(err, usecase.ErrAlreadyExists):
		status, code, message = 409, "ASSET_ALREADY_EXISTS", "この銘柄は登録済みです。"
	case errors.Is(err, usecase.ErrSymbolUnavailable):
		status, code, message = 422, "SYMBOL_UNAVAILABLE", "このコードの価格を取得できません。"
	case errors.Is(err, usecase.ErrPriceProvider):
		status, code, message = 502, "PRICE_PROVIDER_ERROR", "価格提供サービスから有効な価格を取得できません。"
	case errors.Is(err, usecase.ErrPriceTimeout):
		status, code, message = 504, "PRICE_PROVIDER_TIMEOUT", "価格取得がタイムアウトしました。"
	case errors.Is(err, usecase.ErrUnavailable), errors.Is(err, usecase.ErrConnectionLost):
		status, code, message = 503, "SERVICE_UNAVAILABLE", "処理を完了できませんでした。再取得して確認してください。"
	}
	writeJSON(w, status, struct {
		Error apiError `json:"error"`
	}{apiError{code, message, fields}})
}
