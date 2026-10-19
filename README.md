# 株・指数の下落通知バックエンド

Goによる複数ユーザー向けAPI・日次監視コマンド。LINEから専用登録URLを発行し、PostgreSQLにユーザー・本人の監視対象・通知設定・セッションを保存する。FMPの価格から下落率を判定し、所有者のLINEへ通知する。

仕様の正本は隣接する`../stock_notification_docs`リポジトリの`api/openapi.yaml`と`design/`。APIは`/api/v1`配下の12操作を実装する。フロントエンドは別リポジトリで管理する。

## 構成

| ディレクトリ | 責務 |
| --- | --- |
| `cmd/stock-notification` | `serve`・`monitor`の入口と終了コード |
| `internal/domain` | 正確な十進数価格、閾値・鮮度・状態遷移 |
| `internal/usecase` | 操作の処理順序、外部接続のinterface、トランザクション範囲 |
| `internal/adapter` | HTTP・PostgreSQL・FMP・LINE・認証用暗号処理 |
| `internal/bootstrap` | 設定・接続・依存注入・起動と終了 |
| `internal/config` | `.env`・環境変数の読み込みとコマンド別の設定検証 |
| `migrations` | 空の6テーブル・制約。起動時には適用しない |
| `tests/integration` | 専用PostgreSQLと模擬FMP/LINEを使う結合テスト |

domainは外部サービスに依存せず、usecaseはdomainと標準ライブラリだけを利用する。adapterがusecaseのinterfaceを実装し、bootstrapで具体実装を注入する。HTTP要求・応答と業務モデルは分離する。

価格は`math/big`による正確な十進数として扱い、閾値は丸め前の値で判定する。APIは価格とIDを文字列、下落率を小数2桁、日時をUTCのRFC 3339で返す。DBは精度指定のないNUMERICを使用する。

## macOSでの開発環境構築

HomebrewとGoが導入済みのmacOSを前提とする。Go 1.25以降、PostgreSQL 17を使用する。以下のコマンドはリポジトリのルートで実行する。Dockerの実行環境にはColimaを使い、APIはホスト上のGoで動かす。

### 1. DockerとColimaを導入する

```sh
brew install colima docker docker-compose
colima start
# ColimaのDocker接続先を選択する
docker context use colima
docker info
docker-compose version
go version
```

HomebrewのComposeは単独の`docker-compose`コマンドで利用できる。この手順ではこちらを使用する。Makefileは`docker compose`が利用可能ならそちらを、そうでなければ`docker-compose`を自動選択する。ホストへのPostgreSQL・psqlのインストールは不要。

### 2. ローカル設定を作成する

既存の`.env`がある場合はコピーを省略し、必要な項目だけ編集する。

```sh
cp -n .env.example .env
chmod 600 .env
```

`PUBLIC_ORIGIN='https://localhost'`、`DATABASE_URL`、`FMP_API_KEY`、`LINE_TOKEN`、`LINE_CHANNEL_SECRET`を設定する。Webにはすべて、`monitor`にはDB・FMP・LINE_TOKENが必須。LINEのトークンとシークレットは同じ公式アカウントのチャネルを使う。ユーザーのID・パスワードは登録画面で設定し、固定の管理者情報・固定の通知先は設定しない。

外部サービスを使わない検証には後述の結合テストを使う。実際の銘柄登録には有効なFMPキー、登録URLの返信と日次通知には有効なLINE設定が必要。

`serve`・`monitor`は起動時の作業ディレクトリにある`.env`を自動で読み込む。リポジトリのルートで起動すれば、シェルでの手動読み込みは不要。同じ項目がある場合は環境変数より`.env`を優先し、明示された空値もそのまま使う。`.env`や項目がない場合は環境変数を使用する。必須項目が空の場合や、`.env`の読み込み・解析に失敗した場合は起動エラーになる。

### 3. DBを起動・初期化する

```sh
make db-up
docker-compose -f deploy/compose.yaml ps
# healthyになるまで待つ。接続可能ならaccepting connectionsと表示される
docker-compose -f deploy/compose.yaml exec -T db pg_isready -U stock -d stock
```

初回のみ空の6テーブルを作成する。初期ユーザー・全体通知設定は作成しない。旧3テーブルとの互換・データ移行は提供しないため、新しい専用の開発DBを用意して接続先を切り替える。既存DBへこのDDLを重ねて実行しない。起動時の自動マイグレーションはない。

```sh
# stock_multiuser が未作成の場合だけ作成する。既存DBは削除しない。
docker-compose -f deploy/compose.yaml exec -T db createdb -U stock stock_multiuser
docker-compose -f deploy/compose.yaml exec -T db \
  psql -U stock -d stock_multiuser -v ON_ERROR_STOP=1 -f /migrations/000001_initial.up.sql
```

作成後、`.env`の`DATABASE_URL`のDB名を`stock_multiuser`へ変更する。`make migrate`はホストの`psql`と`DATABASE_URL`を使うため、この構成では上記のコンテナ内コマンドを使用する。

### 4. ビルド・API起動

```sh
make build
./bin/stock-notification serve
```

`web_started`ログが表示されれば起動完了。停止はCtrl+C。別ターミナルで次の動作確認を行う。APIだけならフロントエンドの成果物は不要で、画面URLの404は正常。画面を配信する場合は別リポジトリのReact成果物を`web/dist/`へ配置する。

### 5. LINEから登録・ログインを確認する

公開HTTPSプロキシを用意し、LINE Developers ConsoleのWebhook URLを`PUBLIC_ORIGIN/api/v1/webhooks/line`へ設定する。Webhookと再配信を有効化し、署名付きの空イベント検証が200になることを確認する。LINE公式アカウントを友だち追加すると、24時間・1回限りの`/register#token=...`が届く。既に友だちなら「登録」と送信する。

専用URLでログインID（1〜100文字、大小文字・前後空白を区別）とパスワード（UTF-8で8〜72バイト）を登録する。初期一覧は0件、本人の通知率は`[10,15,20,25,30]`。URLを他人へ共有しない。期限切れ・URL未着時は「登録」を再送する。登録済みならログインURLが届く。

ログインAPIの本文は`{"login_id":"owner","password":"example-password"}`。認証・登録成功時のSessionは`login_id`・`expires_at`・`csrf_token`を返す。CookieはSecure・HttpOnly・SameSite=Strict固定なのでブラウザ利用にはHTTPSが必要。保護された変更APIにはOriginとX-CSRF-Token、公開ログイン・登録・登録確認にはOriginとJSON形式が必要。Webhookには未加工本文のLINE署名を要求する。

登録時にはユーザー・初期通知設定・24時間セッションの作成とトークン消費を同一トランザクションで確定する。現在のブラウザだけ旧セッションを置換し、他ブラウザを維持する。ID重複は409でトークン未消費、使えないURLは410。登録の成否不明時は自動再送せず、セッション確認または設定したID・パスワードでログインする。

### 6. テスト

```sh
make test
make vet
# 結合テスト用の専用DBを初回だけ作成する
docker-compose -f deploy/compose.yaml exec -T db createdb -U stock stock_test
TEST_DATABASE_URL='postgres://stock:stock@127.0.0.1:5432/stock_test?sslmode=disable' make test-integration
# 通常・結合・CLIを合算したC0/C1の100%検査（Python 3.9以降が必要）
TEST_DATABASE_URL='postgres://stock:stock@127.0.0.1:5432/stock_test?sslmode=disable' make coverage
```

結合テストは一時スキーマへマイグレーションを適用し、終了時にそのスキーマだけを削除する。DBロールにはスキーマ作成権限が必要。`stock_test`が既に存在すれば`createdb`を省略する。実際のFMP取得・LINE送信は行わない。

カバレッジの判定範囲、分岐とテストの対応表、テスト整理の方針は[テストとカバレッジ](docs/testing.md)を参照。

### 停止・再開とトラブル対応

```sh
# DBコンテナを停止・削除する（DBデータのボリュームは保持）
make db-down
colima stop

# 開発を再開する
colima start
docker context use colima
make db-up
# APIを起動する（.envは自動読み込み）。初期マイグレーションは再適用しない
./bin/stock-notification serve
```

DBデータはDockerボリュームに保存する。通常の停止で`down -v`や`colima delete`は使用しない。

- Dockerへ接続できない場合は`colima status`と`docker context show`を確認し、Colimaを起動して`colima`コンテキストを選ぶ。
- 5432番が使用中なら`lsof -nP -iTCP:5432 -sTCP:LISTEN`で使用元を確認する。既存サービスを勝手に停止せず、必要ならComposeのホスト側ポートと`.env`・テスト用DB URLを同じ番号へ変更する。
- APIの8080番が使用中なら`lsof -nP -iTCP:8080 -sTCP:LISTEN`で確認し、必要なら`HTTP_ADDR`とcurlの接続先を変更する。
- DBが起動しない場合は`docker-compose -f deploy/compose.yaml logs db`で確認する。

本番のコンテナ起動、スケジュール、バックアップ・復元は[運用手順](deploy/README.md)を参照。
