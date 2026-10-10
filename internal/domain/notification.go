package domain

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type NotificationSettings struct {
	Thresholds []int
	UpdatedAt  time.Time
}

func NormalizeThresholds(input []int) ([]int, error) {
	if len(input) < 1 || len(input) > 99 {
		return nil, errors.New("invalid thresholds")
	}
	result := append([]int(nil), input...)
	sort.Ints(result)
	for i, t := range result {
		if t < 1 || t > 99 || (i > 0 && result[i-1] == t) {
			return nil, errors.New("invalid thresholds")
		}
	}
	return result, nil
}

var jst = time.FixedZone("JST", 9*60*60)

func displayTime(t time.Time) string { return t.In(jst).Format("2006-01-02 15:04 MST") }

func DeclineMessage(a Asset, threshold int) string {
	return fmt.Sprintf("株・指数の下落通知\n銘柄: %s (%s)\n下落率: %s%%\n到達閾値: %d%%\n監視開始後の最高値: %s\n最終取得値: %s\n取得日時: %s", a.Name, a.Code, DeclineRate(a.HighestValue, a.LatestValue), threshold, a.HighestValue.String(), a.LatestValue.String(), displayTime(a.LatestFetchedAt))
}
func StaleMessage(a Asset, q Quote) string {
	return fmt.Sprintf("価格データの停滞通知\n銘柄: %s (%s)\n価格データが10日以上前です。\nFMPデータ日時: %s\n検知日時: %s\n監視を自動停止します。データ提供状況を確認し、必要に応じて手動で再開してください。", a.Name, a.Code, displayTime(q.ProvidedAt), displayTime(q.FetchedAt))
}
