# 株・指数の下落通知バックエンド

Goによる単一管理者向けAPI・日次監視コマンド。PostgreSQLに監視対象・通知設定・セッションを保存し、FMPの価格から下落率を判定してLINEへ通知する。

仕様の正本は隣接する`../stock_notification_docs`リポジトリの`api/openapi.yaml`と`design/`。APIは`/api/v1`配下の9操作を実装する。フロントエンドは別リポジトリで管理する。

## 構成

| ディレクトリ | 責務 |
| --- | --- |
| `cmd/stock-notification` | `serve`・`monitor`の入口と終了コード |
| `internal/domain` | 正確な十進数価格、閾値・鮮度・状態遷移 |
| `internal/usecase` | 操作の処理順序、外部接続のinterface、トランザクション範囲 |
| `internal/adapter` | HTTP・PostgreSQL・FMP・LINE・認証用暗号処理 |
| `internal/bootstrap` | 設定・接続・依存注入・起動と終了 |
| `internal/config` | コマンド別の必須環境変数検証 |
| `migrations` | テーブル・制約・初期設定。起動時には適用しない |
| `tests/integration` | 専用PostgreSQLと模擬FMP/LINEを使う結合テスト |

domainは外部サービスに依存せず、usecaseはdomainと標準ライブラリだけを利用する。adapterがusecaseのinterfaceを実装し、bootstrapで具体実装を注入する。HTTP要求・応答と業務モデルは分離する。

価格は`math/big`による正確な十進数として扱い、閾値は丸め前の値で判定する。APIは価格とIDを文字列、下落率を小数2桁、日時をUTCのRFC 3339で返す。DBは精度指定のないNUMERICを使用する。

## 開発

Go 1.25以降、PostgreSQL 17、Docker Composeまたは利用可能なPostgreSQLを用意する。

```sh
make build
make test
make vet
```

`.env`を設定後、シェルで読み込んで実行する。アプリ自体は`.env`を自動読込しない。

```sh
set -a
. ./.env
set +a
./bin/stock-notification serve
# 別途、日次処理を1回実行
./bin/stock-notification monitor
```

セッションCookieはSecure固定のため、ブラウザからはHTTPS経由で利用する。Reactの成果物は`web/dist/`へ配置する。

結合テストは専用のDB URLを明示する。テストごとに一時スキーマを作成してマイグレーションを適用し、終了時にそのスキーマだけを削除する。DBロールにはスキーマ作成権限が必要。

```sh
TEST_DATABASE_URL='postgres://stock:stock@127.0.0.1:5432/stock?sslmode=disable' make test-integration
```

実際のFMP取得・LINE送信を伴うテストは行わない。初回DB作成、コンテナ起動、スケジュール、バックアップ・復元は[運用手順](deploy/README.md)を参照。
