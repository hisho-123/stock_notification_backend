package usecase

import "context"

type loginIDKey struct{}

// 所有者は認証済みセッションから渡し、リクエスト本文では指定させない。
func WithLoginID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, loginIDKey{}, id)
}
func LoginID(ctx context.Context) string { id, _ := ctx.Value(loginIDKey{}).(string); return id }
