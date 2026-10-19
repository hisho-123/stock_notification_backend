package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
)

const assetColumns = `id, login_id, code, name, highest_value::text, latest_value::text, latest_fetched_at,
 monitoring_enabled, last_notified_threshold, last_checked_at, last_error_code, created_at, updated_at`

func scanAsset(row pgx.Row) (domain.Asset, error) {
	var a domain.Asset
	var high, latest string
	var errorCode *string
	err := row.Scan(&a.ID, &a.LoginID, &a.Code, &a.Name, &high, &latest, &a.LatestFetchedAt, &a.MonitoringEnabled, &a.LastNotifiedThreshold, &a.LastCheckedAt, &errorCode, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	a.HighestValue, err = domain.ParsePrice(high)
	if err != nil {
		return a, err
	}
	a.LatestValue, err = domain.ParsePrice(latest)
	if err != nil {
		return a, err
	}
	if errorCode != nil {
		e := domain.AssetError(*errorCode)
		a.LastErrorCode = &e
	}
	return a, nil
}

func (s *Store) List(ctx context.Context, o usecase.ListOptions) ([]domain.Asset, error) {
	columns := map[string]string{
		"created_at": "assets.created_at", "code": "assets.code", "name": "assets.name", "highest_value": "assets.highest_value", "latest_value": "assets.latest_value",
		"decline_rate": "assets.id", "latest_fetched_at": "assets.latest_fetched_at", "monitoring_enabled": "assets.monitoring_enabled", "last_notified_threshold": "assets.last_notified_threshold",
	}
	column, ok := columns[o.Sort]
	if !ok || (o.Order != "asc" && o.Order != "desc") {
		return nil, usecase.Invalid("sort", "ソート指定を確認してください。")
	}
	// Escape LIKE metacharacters; the column and direction come only from allowlists.
	query := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(o.Query)
	rows, err := s.q.Query(ctx, fmt.Sprintf(`SELECT %s FROM assets
 WHERE login_id=$2 AND (code ILIKE $1 ESCAPE '\' OR name ILIKE $1 ESCAPE '\')
 ORDER BY %s %s NULLS LAST, id ASC`, assetColumns, column, o.Order), "%"+query+"%", usecase.LoginID(ctx))
	if err != nil {
		return nil, s.mapError(err)
	}
	defer rows.Close()
	assets := make([]domain.Asset, 0)
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, s.mapError(err)
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, s.mapError(err)
	}
	if o.Sort == "decline_rate" {
		// PostgreSQL NUMERIC division rounds repeating decimals. Compare exact
		// cross products in Go so even differences beyond its division scale sort correctly.
		sort.Slice(assets, func(i, j int) bool {
			cmp := domain.CompareDecline(assets[i].HighestValue, assets[i].LatestValue, assets[j].HighestValue, assets[j].LatestValue)
			if cmp == 0 {
				return assets[i].ID < assets[j].ID
			}
			if o.Order == "desc" {
				return cmp > 0
			}
			return cmp < 0
		})
	}
	return assets, nil
}
func (s *Store) ExistsCode(ctx context.Context, code string) (bool, error) {
	var exists bool
	err := s.q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE code=$1 AND login_id=$2)`, code, usecase.LoginID(ctx)).Scan(&exists)
	return exists, s.mapError(err)
}
func (s *Store) Create(ctx context.Context, a domain.Asset) (domain.Asset, error) {
	a, err := scanAsset(s.q.QueryRow(ctx, `INSERT INTO assets
 (login_id,code,name,highest_value,latest_value,latest_fetched_at,monitoring_enabled,last_notified_threshold,last_checked_at,last_error_code,created_at,updated_at)
 VALUES ($12,$1,$2,$3::numeric,$4::numeric,$5,$6,$7,$8,$9,$10,$11) RETURNING `+assetColumns,
		a.Code, a.Name, a.HighestValue.String(), a.LatestValue.String(), a.LatestFetchedAt, a.MonitoringEnabled, a.LastNotifiedThreshold, a.LastCheckedAt, errorValue(a.LastErrorCode), a.CreatedAt, a.UpdatedAt, usecase.LoginID(ctx)))
	return a, s.mapError(err)
}
func (s *Store) Get(ctx context.Context, id int64) (domain.Asset, error) {
	a, err := scanAsset(s.q.QueryRow(ctx, `SELECT `+assetColumns+` FROM assets WHERE id=$1 AND login_id=$2`, id, usecase.LoginID(ctx)))
	return a, s.mapError(err)
}
func (s *Store) SetMonitoring(ctx context.Context, id int64, enabled bool, now time.Time) (domain.Asset, error) {
	a, err := scanAsset(s.q.QueryRow(ctx, `UPDATE assets SET monitoring_enabled=$2,updated_at=$3 WHERE id=$1 AND login_id=$4 RETURNING `+assetColumns, id, enabled, now, usecase.LoginID(ctx)))
	return a, s.mapError(err)
}
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM assets WHERE id=$1 AND login_id=$2`, id, usecase.LoginID(ctx))
	if err != nil {
		return s.mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return usecase.ErrNotFound
	}
	return nil
}
func (s *Store) Targets(ctx context.Context) ([]usecase.Target, error) {
	rows, err := s.q.Query(ctx, `SELECT id,code,login_id FROM assets WHERE monitoring_enabled ORDER BY id`)
	if err != nil {
		return nil, s.mapError(err)
	}
	defer rows.Close()
	result := make([]usecase.Target, 0)
	for rows.Next() {
		var target usecase.Target
		if err := rows.Scan(&target.ID, &target.Code, &target.LoginID); err != nil {
			return nil, s.mapError(err)
		}
		result = append(result, target)
	}
	return result, s.mapError(rows.Err())
}
func (s *Store) SaveObservation(ctx context.Context, a domain.Asset) error {
	// A deletion racing this write is explicitly outside the consistency guarantee.
	_, err := s.q.Exec(ctx, `UPDATE assets SET highest_value=$2::numeric,latest_value=$3::numeric,
 latest_fetched_at=$4,monitoring_enabled=$5,last_notified_threshold=$6,last_checked_at=$7,last_error_code=$8,updated_at=$9 WHERE id=$1 AND login_id=$10`,
		a.ID, a.HighestValue.String(), a.LatestValue.String(), a.LatestFetchedAt, a.MonitoringEnabled, a.LastNotifiedThreshold, a.LastCheckedAt, errorValue(a.LastErrorCode), a.UpdatedAt, usecase.LoginID(ctx))
	return s.mapError(err)
}
func errorValue(code *domain.AssetError) any {
	if code == nil {
		return nil
	}
	return string(*code)
}
