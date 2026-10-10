# 実行・運用

Webは単一プロセスで起動する。HTTPSリバースプロキシの背後に置き、ReactとAPIを同じオリジンで配信する。プロキシのタイムアウトは90秒以上にする。APIの実行期限は90秒、外部通信は30秒。`PUBLIC_ORIGIN`はHTTPSのオリジンを末尾スラッシュなしで指定する。

## 初回起動

以下は`docker compose`形式で記載する。単独のCompose CLIを利用する環境では`docker-compose`に読み替える。MakefileのDB操作は自動判別する。

1. ルートの`.env.example`を`.env`へコピーし、秘密情報とcost 12以上のbcryptハッシュを設定する。`.env`は権限600にし、Gitへ追加しない。`htpasswd -nBC 12 owner`は対話入力でハッシュを生成できる。出力の`owner:`を除いたハッシュをシングルクォートで囲む。
2. `make db-up`でローカルPostgreSQLを起動する。
3. 初回のみ次のコマンドでスキーマを作成する。適用済みDBでは再実行しない。

```sh
docker compose -f deploy/compose.yaml exec -T db \
  psql -U stock -d stock -v ON_ERROR_STOP=1 -f /migrations/000001_initial.up.sql
```

4. frontendのビルド成果物を`web/dist/`へコピーする。APIだけを動作確認する場合は不要。成果物がなければ画面URLは404になる。
5. `docker compose -f deploy/compose.yaml --profile app up -d --build api`でWebを起動する。監視を手動実行する場合は、他の実行がないことを確認し、次のコマンドを使う。

```sh
docker compose -f deploy/compose.yaml --profile tools run --rm monitor
```

ローカルDBの固定認証情報と`sslmode=disable`は開発用。本番では専用の接続情報とDBサーバーに応じたTLS設定を使用する。コンテナの公開ポートはループバックに限定する。

## 定期実行

外部スケジューラで毎日07:00 `Asia/Tokyo`に`stock-notification monitor`を1回実行する。UTCでは毎日22:00。cronのタイムゾーン対応は実装に依存するため、スケジューラ側で確認する。秘密情報は実行環境に注入する。

監視終了0は全成功、1は設定不備・DB障害・対象失敗・古いデータ検知。停滞通知成功による自動停止も1になる。実行ID・対象ID・結果・終了時刻をJSONログへ記録する。スケジューラ側で失敗と未実行を検知する。

監視を重複起動しない。停止・再開・削除・手動実行の前に監視コマンドの終了を確認する。アプリに排他ロックはなく、競合時の整合性を保証しない。通知の即時再送や送信待ちキューはなく、翌日の価格から再判定する。LINE受付後のDB保存失敗では重複通知が起こり得る。

`TRUSTED_PROXY_CIDRS`は実際に信頼するプロキシの接続元だけを指定する。未指定時はソケットの接続元をログイン試行制限に使用する。設定した場合も、転送チェーンを右から確認して最初の信頼しないIPを採用する。

## バックアップ・復元

`DATABASE_URL`を安全な環境変数として設定し、バックアップする。

```sh
pg_dump --dbname="$DATABASE_URL" --format=custom --file=stock.dump
```

復元時はWebとスケジューラを停止する。空の専用DBへ復元後、全セッションを削除してから再起動する。

```sh
pg_restore --dbname="$DATABASE_URL" stock.dump
psql --dbname="$DATABASE_URL" -v ON_ERROR_STOP=1 -c 'DELETE FROM sessions'
```

復元では通知済み状態もバックアップ時点へ戻るため、重複通知が起こり得る。バックアップファイルは管理データを含むのでアクセス権を制限する。管理者設定変更時も全セッションを削除してWebを再起動する。
