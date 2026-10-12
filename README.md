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
| `internal/config` | `.env`・環境変数の読み込みとコマンド別の設定検証 |
| `migrations` | テーブル・制約・初期設定。起動時には適用しない |
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
# 対話入力で管理者パスワードのbcryptハッシュを作成する
htpasswd -nBC 12 owner
```

出力の`owner:`を除いたハッシュを、`.env`の`ADMIN_PASSWORD_HASH`へシングルクォートで囲んで設定する。ハッシュ中の`$`をシェルに展開させないこと。`ADMIN_ID='owner'`とし、`PUBLIC_ORIGIN='https://localhost'`へ変更する。DB接続先と`HTTP_ADDR='127.0.0.1:8080'`はサンプルのままでよい。

このPCの初回構築では管理者パスワードを自動生成し、Git管理対象外の`bin/local-admin-password`（権限600）へ保存した。必要時にローカルで確認できる。再構築する場合は上記の対話入力で自分のパスワードを設定してよい。

FMP・LINEの値はローカルの起動・認証確認では仮値のままでよい。銘柄登録など価格取得を伴う操作には有効な`FMP_API_KEY`が必要。`monitor`にはさらに有効な`LINE_TOKEN`・`LINE_USER_ID`が必要で、実際の通知が送信され得るため、この確認では実行しない。

`serve`・`monitor`は起動時の作業ディレクトリにある`.env`を自動で読み込む。リポジトリのルートで起動すれば、シェルでの手動読み込みは不要。同じ項目がある場合は環境変数より`.env`を優先し、明示された空値もそのまま使う。`.env`や項目がない場合は環境変数を使用する。必須項目が空の場合や、`.env`の読み込み・解析に失敗した場合は起動エラーになる。

### 3. DBを起動・初期化する

```sh
make db-up
docker-compose -f deploy/compose.yaml ps
# healthyになるまで待つ。接続可能ならaccepting connectionsと表示される
docker-compose -f deploy/compose.yaml exec -T db pg_isready -U stock -d stock
```

初回のみスキーマを作成する。適用済みのDBには再実行しない。起動時の自動マイグレーションはない。

```sh
docker-compose -f deploy/compose.yaml exec -T db \
  psql -U stock -d stock -v ON_ERROR_STOP=1 -f /migrations/000001_initial.up.sql
```

`make migrate`はホストの`psql`を使うため、この構成では上記のコンテナ内コマンドを使用する。

### 4. ビルド・API起動

```sh
make build
./bin/stock-notification serve
```

`web_started`ログが表示されれば起動完了。停止はCtrl+C。別ターミナルで次の動作確認を行う。APIだけならフロントエンドの成果物は不要で、画面URLの404は正常。画面を配信する場合は別リポジトリのReact成果物を`web/dist/`へ配置する。

### 5. curlでログイン・設定取得を確認する

以下はzsh/bashで実行する。Originには手順2で設定した`https://localhost`を指定する。`PUBLIC_ORIGIN`を別の値にした場合は、curlのOriginも同じ値へ変更する。パスワードをコマンド履歴へ残さないよう対話入力し、レスポンスヘッダーは権限を制限した一時ディレクトリへ保存する。

```sh
check_dir=$(mktemp -d)
chmod 700 "$check_dir"
# 入力値はこのPCで設定した管理者パスワード
printf 'Admin password: '
read -r -s admin_password
printf '\n'
# パスワード内のJSON特殊文字をエスケープする
escaped_password=$(printf '%s' "$admin_password" | sed 's/\\/\\\\/g; s/"/\\"/g')
printf '{"admin_id":"owner","password":"%s"}' "$escaped_password" | \
  curl --fail-with-body -sS -D "$check_dir/headers" \
    -H 'Origin: https://localhost' -H 'Content-Type: application/json' \
    --data-binary @- http://127.0.0.1:8080/api/v1/auth/login
unset admin_password escaped_password

# HTTPでの確認用に、返された2種類のCookieを明示する
cookies=$(awk 'tolower($1) == "set-cookie:" {sub(/;.*/, "", $2); printf "%s%s", sep, $2; sep="; "}' "$check_dir/headers")
curl --fail-with-body -sS -H "Cookie: $cookies" \
  http://127.0.0.1:8080/api/v1/settings/notifications
unset cookies
rm -r "$check_dir"
unset check_dir
```

どちらもHTTP 200とJSONが返ることを確認する。`PUBLIC_ORIGIN`はAPIのOrigin検証用設定であり、この設定だけでHTTPSサーバーが起動するわけではない。CookieはSecure固定なので、ブラウザから利用する場合はHTTPSリバースプロキシを別途用意する。更新系APIにはOriginに加えてCSRFトークンが必要。

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
