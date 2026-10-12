# テストとカバレッジ

## 実行方法

Go 1.25以降、Python 3.9以降、PostgreSQL 17の専用テストDBを使用する。

```sh
make test
make vet
TEST_DATABASE_URL='postgres://stock:stock@127.0.0.1:5432/stock_test?sslmode=disable' make coverage
```

`make coverage`は計測ツールの自己テスト後、通常・結合テストをrace検査付きで実行する。CLIは実際のバイナリをrace・カバレッジ付きでビルドし、子プロセスの終了コードとログを検証する。結合テストは一時スキーマを使用し、実際のFMP・LINEには接続しない。

CLI・bootstrapを含む全本番パッケージが対象。C0またはC1に未到達があれば、位置を表示して終了コード1を返す。成果物はGit管理対象外の`coverage/`へ出力する。

| 成果物 | 内容 |
| --- | --- |
| `coverage/statements.cover` | 通常・結合テストとCLI子プロセスの文カバレッジを合算したGo標準プロファイル |
| `coverage/branches.json` | 元のファイル・行・列、判定の種類、各分岐の到達状態と通過したテスト名 |
| `coverage/branches.md` | 全分岐と、通過したテスト／サブテストの対応表 |

文カバレッジは`go tool cover -func=coverage/statements.cover`、HTMLは`go tool cover -html=coverage/statements.cover`で確認できる。

## C0/C1の定義

C0はGo標準の文カバレッジを使う。通常・結合テストの`-coverpkg=./...`プロファイルと、CLIの`GOCOVERDIR`を`go tool covdata textfmt`で変換したプロファイルを、同じ計測ブロック単位で合算する。定数だけのテストfixtureには計測対象の文がない。

Go標準ではC1を計測できないため、`scripts/branch-instrument.go`が一時コピーのASTに分岐プローブを追加する。元のソースは変更しない。次の分岐を確認する。

| 構文 | 確認する分岐 |
| --- | --- |
| `if`・条件付き`for` | 判定式全体のtrue／false |
| `range` | 本体への進入と、要素を使い切った終了。空入力は終了だけを通過する |
| `switch` | 各caseとdefault。default省略時は、どのcaseにも一致しない経路も含む |
| `select` | 各caseと、記述されているdefault |
| boolを返す関数・比較関数 | 非定数の戻り値のtrue／false |

`break`・`return`をrangeの正常終了として計上しない。条件のない`for`の終了経路は、その中の判定・returnで確認する。`&&`・`||`の各項を独立して切り替えるC2・MC/DCは対象外。型switch、ラベル、goto、fallthroughは現在の本番コードに存在せず、追加された場合は計測を失敗させて対応を要求する。

テストの一時コピーでは`testing.T.Setenv`で現在のテスト名を設定し、サブテスト・別パッケージの呼び出し・CLI子プロセスまで追跡する。同一テストプロセス内では直列実行を前提とする。`t.Parallel`との併用はGoのSetenv制約によって失敗する。テスト外の実行は`<process>`として記録する。

自己テストは、計測前後の出力の一致、空・反復・break・早期return・入れ子のループ、bool戻り値、switchの不一致、サブテスト名の追跡、未対応構文での失敗を確認する。

## 仕様と代表テストの対応

各判定のtrue／falseやcaseごとの実測対応は、実行時に生成される`coverage/branches.md`を参照する。次の表は期待値を確認する代表テストを示す。

| 対象・仕様 | 代表テスト |
| --- | --- |
| CLIの引数・設定失敗、コマンド選択、終了コード、構造化ログ | `TestCommandValidationAndDispatch`、`TestServeAndMonitorExitCodes`、`TestCLIProcess`、`TestBootstrapAndCLIProcesses` |
| 起動時の接続・セッション掃除・設定失敗、サーバーの起動と終了、資源解放 | `TestServeStartupFailuresReleaseResources`、`TestServeLifecycleAndConfiguration`、`TestPublicBootstrapConnectionFailures` |
| bootstrapの依存接続、通知閾値の保存、観測ログ | `TestMonitorWiringAndObservationLog` |
| コマンド別必須環境変数、設定境界、bcrypt検証の適用範囲 | `TestRequiredEnvironmentVariables`、`TestConfigurationBoundariesAndOverrides`、`TestCommandSpecificConfiguration`、`TestServeRejectsInvalidPasswordHashes` |
| トークン生成・ハッシュ比較、bcryptの形式と照合 | `TestRandomTokensAndHashComparison`、`TestValidPasswordHash`、`TestPasswordMatching` |
| 認証の入力・トークン生成・保存失敗、セッション置換・期限・CSRF・ログアウト | `TestLoginValidationAndFailures`、`TestLoginPersistsHashesAndReplacesSession`、`TestAuthenticationAndLogout` |
| Cookieの名前・2件の削除、Origin・CSRF・ログイン制限・信頼プロキシ | `TestSessionLifecycleAndCookies`、`TestCSRFAndStrictInputs`、`TestLoginFailureWindow`、`TestTrustedProxyChain`、`TestProxyFallbacksAndPartialFailureWindow` |
| HTTPの不正JSON・入力・ID・query、エラー応答・DTO、panic・timeout、静的ファイル | `TestInvalidObjectsAndHandlerInputs`、`TestErrorResponseContract`、`TestHTTPDependencyErrorsAndAssetResponses`、`TestGuardRecoveryAndResponseHeaders`、`TestStaticFilesAndSPARoutes`、`TestStaticMethodsAndMissingIndex` |
| FMPの初期値・timeout・redirect・通信・本文・鮮度・価格・銘柄名 | `TestClientConfiguration`、`TestProviderTransportAndBodyFailures`、`TestResponseValidation`、`TestTimeoutAndRedirect`、`TestQuoteTimestampAndFallbackName` |
| LINEの宛先・認証・Content-Type・text型・本文、受理判定と再送なし | `TestPushReceiptAndNoRetry`、`TestClientConfigurationAndFailures`、`TestRedirectIsNotFollowed` |
| 銘柄操作の検証・外部取得・保存失敗・引数転送、設定の正規化と保存 | `TestCreateValidationAndDependencyFailures`、`TestCreateStaleAndDuplicate`、`TestAssetListValidationAndForwarding`、`TestAssetMutationForwarding`、`TestSettingsValidationAndPersistence` |
| 監視開始時の失敗、削除・停止の再確認、DB障害、キャンセルと報告 | `TestMonitorStartupFailures`、`TestTargetRecheckAndThresholdSnapshot`、`TestMonitorRecheckFailuresAndInvalidQuotes`、`TestDBFailureContinuation`、`TestMonitorCancellationAndEmptyTargets` |
| 下落・停滞通知の全文、閾値・価格・JST日時、通知失敗後の再判定・状態保存、ID順の処理 | `TestMonitorStateTransitions`、`TestFailedNotificationReevaluatedNextDay`、`TestMonitorPreservesNotificationStateOnFailure`、`TestMonitorDeterministicTargetOrder` |
| 十進数・文字数・鮮度境界、丸め前の閾値・順序・同率比較、通知の状態遷移 | `TestDecimalStorageLimits`、`TestCodeAndNameBoundaries`、`TestThresholdCollectionBoundaries`、`TestFreshnessBoundary`、`TestExactThresholdAndRounding`、`TestExactDeclineComparison`、`TestHigherThresholdAfterPreviousNotification`、`TestSessionExpiration` |
| PostgreSQLの接続・SQL・scan・反復・制約エラー、同率ソート、トランザクションとセッション置換の原子性 | `TestDatabaseErrorMapping`、`TestOpeningInvalidOrUnavailableDatabase`、`TestAssetScanningFailures`、`TestListAndTargetsFailures`、`TestRepositoryMutationFailures`、`TestDeclineSortingTiesAndDirections`、`TestAlreadyOrderedDeclineTies`、`TestTransactionFailureAndRollback`、`TestSessionReplacementIsAtomic` |
| 実DB制約・正確な下落率ソート、API→監視→通知、接続喪失・マイグレーションrollback | `TestRepositoriesAndDatabaseConstraints`、`TestDeclineSortingWithoutDivisionRounding`、`TestAPIAndMonitoring`、`TestMonitorConnectionLossAndMigrationRollback` |

## テストと本番コードの整理

Assetsに接続していない通知モックの確認は削除した。登録時に通知しない契約は、通知先を実際に接続した`TestAPIAndMonitoring`で維持する。`NormalizeThresholds`のnil／空スライスは代表ケースへ集約し、bcryptの詳細な形式検証はsecurityに集めた。config側は代表的な不正形式・低コスト・monitorでは不要という境界を確認する。bcryptハッシュは固定fixtureを使い、検証のたびに生成しない。

鮮度境界、丸め前の閾値、通知失敗翌日の再判定、DB制約は別々の仕様を守るため維持する。障害の再現は既存interfaceと非公開の依存注入を使用し、戻り値だけでなく保存状態・通知本文・ログ・資源解放を確認する。

Go 1.24以降の`crypto/rand.Read`のエラー分岐、文字列だけのJSONエンコード、オブジェクトのキー位置の文字列型判定、固定MaxConnsを持つpgxpool初期化の到達不能なエラー分岐を整理した。FMPのURL解析はHTTP要求の生成へ一本化した。HTTP・DB・CLIの公開仕様は維持している。
