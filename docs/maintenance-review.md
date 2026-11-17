# Maintenance review

## 2026-11-17

### 非機能・品質調査への対応

- 監視は送信成功済みのレコードID・内容のハッシュをチェックポイントDBへ保存し、
  次の走査では変化したレコードだけを送信します。DBへの照合と書き込みは200件単位です。
  正規メッセージの後着やCopilotのジャーナルを壊さないよう、変更ファイルの解析は維持します。
  1,000件に1件追記した回帰テストでは、再起動後も追加送信は1件です。
  送信失敗時の再試行、既存IDの内容変更、置換・切り詰めも確認します。
- JSON/ZIP出力は閲覧と別のread-only poolを使用し、直接出力の同時実行は2件まで。
  待ち枠を増やし続けず、追加要求には503とRetry-Afterを返します。
  全体のダウンロード時間は制限せず、進捗のない書き込みだけ30秒で打ち切ります。
  停止した出力があっても一覧を取得できる回帰テストを追加しました。
- プロジェクト一覧と子セッション表示は、それぞれのモジュール内で状態と通信を所有します。
  サイドバー検索の変更が並行する一覧取得を中断しないこと、古い応答・エラーで新しい画面を
  上書きしないことを確認します。イベント種別ボタンも選択状態をaria-pressedで公開します。
- 詳細の子セッション一覧は最初の20件だけを取得し、追加分は明示操作で20件ずつ読み込みます。
  47件のfixtureで20→40→47件、追加取得前の通信0回、追加したリンクからのプレビューと
  モバイル表示を確認しました。起動箇所に付く既存のリンクは維持しています。
- x/netを0.60.0、x/textを0.42.0、gRPCを1.83.1へ更新。
  go.modの最低版・Docker・Release workflowが使用するGoを1.27.4へ揃えました。
  govulncheckのソース検査では呼び出し経路の指摘は0件です。
- 通常の振る舞いテストは、一度だけ準備して閉じた空DBを各テスト専用ファイルへ複製します。
  マイグレーション・アップグレード試験は履歴を実際に適用するままです。
  watcherの完了待ちは成功・失敗の通知で判定します。
  アーカイブの非ブロッキング試験はbusy timeoutを試験期限より長く設定し、race実行時の
  SQL処理速度に余裕を持たせながら、ブロッキングcheckpointに戻す変更を検出できます。

検証には、Goの通常・raceテスト、vet、静的構文確認、依存検査、実プロセスの監視、
会話・検索とSkills・プロジェクト一覧・子セッションのブラウザーフロー、
Dockerでの認証・再作成後の永続化・日本語検索を使用します。

以下は前回の対応記録です。

受信キュー、DB更新・検索、エクスポートとアーカイブ、ファイル監視、
CLI・画面側の依存関係を確認し、担当外の変更を相互レビューしました。
P1は見つからず、以下のP2は修正しました。

| 重要度 | 実際に起こる条件・問題 | 対応 |
| --- | --- | --- |
| P2 | 数百MB以上の単体セッションを出力すると、Go側が原本全件を保持し、SQLiteも原本を含む全体ソートを行う。 | 原本IDだけを並べてから原本を取得し、JSON配列を1イベントずつ出力する。形式・順序と空配列を維持する。 |
| P2 | 単体JSON・検索結果ZIPのダウンロードがHTTPサーバーの60秒制限で途切れる。 | アーカイブと同じダウンロード準備処理を使い、通常API用の総書き込み時間制限を解除する。切断・書き込み失敗はそのまま中断する。 |
| P2 | 明示した監視ディレクトリがシンボリックリンクの場合、対象が黙って0件になる。 | 指定したルートを解決する。内部リンクは巡回せず、同一原本の重複探索や循環を防ぐ。 |
| P2 | 長時間の閲覧・エクスポート中、アーカイブの削除200件ごとにWALの切り詰めが最大5秒待ち、書き込み接続を占有する。 | 削除途中は待機しないPASSIVE checkpointを使う。最終的な容量回収は既存のcompaction側に残す。 |

受信キューの接続・容量制限・再処理・監視チェックポイントを`ingestQueue`に集約し、
起動処理やアーカイブ操作からキューのSQLを分離しました。検索条件はテーブル・条件・
バインド値をまとめた`sqlSelection`で扱い、集計用に生成済みSQLを文字列解析する処理を除去しました。
本番で使われなくなった全件読み込みヘルパーは、小規模fixture用のテスト側に移しました。

P3として、キュー先頭の処理時間が外から分かりにくい点、ブラウザー機能間の共有変数、
Python統合テストの起動fixture重複が残ります。今回の障害修正に必要な変更ではなく、
全画面の状態管理やテスト基盤を一括で置き換えるリスクに見合わないため保留しました。

### テストの選び方

- 起動中のACK、強制終了後の再処理、容量上限、ライブ受信枠、監視チェックポイントは
  既存のGo・実プロセステストを流用し、責務の移動だけをなぞるテストは増やしません。
- ダウンロードは、短い実HTTPサーバーの書き込み期限を跨ぐケース、ページ上限を超える出力、
  同時刻・origin混在時の順序、切断後のreader解放を確認します。
- アーカイブは、読み取りsnapshotを保持したまま複数バッチの削除が完了するケースを追加。
  修正前のTRUNCATEに戻すと同じテストが失敗することも確認しました。
- 監視はルートリンク、重複指定、内部リンク・循環の除外、リンク先の後生成と追記に絞ります。
- 検索は既存の日本語・時間・複数プロジェクト・origin条件のAPIテストと、
  検索→集計→ZIP出力→Skillsのブラウザーフローを使います。

### Docker build follow-up

Dockerfile・Compose・リリース設定・Dockerテストを別担当がレビューしました。
SSL終端はホストNginx、コンテナはHTTPの構成を維持しています。

| 重要度 | 問題 | 対応 |
| --- | --- | --- |
| P2 | ソース変更でGoのコンパイル層が失効すると、SQLite・日本語辞書などの依存も毎回コンパイルされる。 | module/build cacheをBuildKitで共有し、VERSION指定を依存取得の後へ移動。テスト専用ファイルもコンテキストから除外。 |
| P2 | 大規模DockerテストでDBを差し替えて同じURLで再起動すると、古い準備済みキャッシュと原本件数だけで成功判定し、索引整備中の一覧を検証してしまう。 | 毎回`/api/status`のready・未処理0件を確認してから検証する。 |
| P3 | Dockerテストが固定socketで利用者のDocker contextを上書きする。 | 標準Docker CLIのcontext・DOCKER_HOSTをそのまま使う。 |

ソース1ファイルを変更した実測では、コンパイル工程が32.0秒から3.4秒へ短縮しました。
テスト専用ファイルの変更はコンパイル層がCACHEDになり、VERSION変更でも依存取得層を再利用します。
キャッシュ変更前後の既定ビルドは同一の実行イメージになりました。

## v0.13.0

This records the v0.13.0 review. CI-proof release verification was subsequently removed in favor of local checks; see [the current development workflow](development.md#local-checks-and-releases).

Scope: CLI/configuration, import/watch parsers, OTLP ingestion, database lifecycle,
migrations and projections, query/cache paths, archive stages, browser scripts,
delivery scripts and tests. Review passes covered ownership/duplication first,
then cancellation, transaction boundaries, cache dimensions and late telemetry.
Features, public HTTP responses and visual design are preserved except for the
incorrect results described below.

## Findings and disposition

No P1 finding was identified. All identified P2 findings are addressed.

| Priority | Finding | Resolution |
| --- | --- | --- |
| P2 | Startup registered termination signals after preparation; ingestion detached database work from canceled HTTP/watch requests. App lifetime was created by the archive subsystem. | One caller-owned context starts before database preparation and reaches startup, workers, HTTP base contexts, local sinks and write transactions. Open failure and normal shutdown share `Close`. |
| P2 | The folder summary cache ignored the project filter, including the meaningful empty/unassigned value. | One parsed filter supplies SQL and cache eligibility. Unsupported cache dimensions use the existing filtered query. |
| P2 | JSON OTLP reception rounded numeric int64 timestamps and counters through float64 before protobuf validation. | A strict, number-preserving JSON decoder serves telemetry documents, embedded values and JSON reception. |
| P2 | Resolved per-call usage retained an old ancestor after a closer/conflicting request or missing bridge changed the trace. | Trace changes coalesce in a durable queue; indexed reconciliation reevaluates affected usage with the existing unique-nearest/session/origin rules. Replace the old pending-only index with one matching trace/role/span lookups. Migration 0009 schedules existing facts once; previous migrations remain immutable. |
| P2 | Incremental archive reclamation followed a moving global freelist, allowing unrelated writes to extend a job indefinitely. | Bound reclamation by the initial free-page budget and count actual reclaimed pages, with ingestion allowed between batches. |
| P3 | Archive scheduling/export/deletion/reclamation shared one large implementation; failure handling duplicated nested transaction/rollback logic. | Separate stage files; scheduling owns recovery and uses the existing transaction runner. Export/delete bodies retain their previous behavior. |
| P3 | Repeated HTML shell handlers and failure-path cleanup obscured shared ownership. | Single shell handler and application cleanup path. |
| P3 | Compressed browser scripts made review difficult; an unused folder URL helper and always-zero project offset remained. | Format scripts, remove dead helper/state. The formatting step was checked by comparing Babel syntax trees, excluding source locations, comments and empty statements. |
| P3 | Browser features still communicate through shared globals. | Deferred: converting every feature to modules changes initialization/state ownership across all flows. Readable scripts and current navigation coverage provide an incremental starting point. |
| P3 | Standalone Python integration scripts repeat process/fixture setup. | Deferred: those fixtures cover different published-version and performance scenarios. A broad harness rewrite adds little failure coverage for this change. |

## Test value versus cost

- Add focused cancellation tests: canceled startup, a canceled receiver blocked on
  the single writer connection, and application workers stopping with their owner.
  They cover shutdown hangs without large fixtures or fixed sleeps.
- Extend the existing cached/dynamic folder comparison with project, unassigned
  and combined source/project filters. Avoid one test per implementation field.
- Exercise numeric int64 through the actual HTTP/protobuf pipeline for all three
  signals. Retain invalid/trailing JSON rejection.
- Exercise late closer requests, ambiguity and bridge removal. An older-schema
  fixture verifies repair of already-resolved facts and no repeated upgrade work.
- Retain archive download/confirmation/replay, corrupt archive, crash/resume,
  actual space reclamation and vacuum row-draining tests: failures can lose data
  or leave a large database unreclaimed. File moves need no extra unit tests.
- Retain published-binary upgrades, Japanese search, accounting precision and
  bounded-memory/performance checks. Their fixtures address previously observed
  production-scale failures. Avoid adding timing-sensitive producer stress tests
  or assertions that merely duplicate the new control flow.
- Browser checks cover project/activity navigation and the complete archive
  flow. Formatting alone is checked structurally rather than with new snapshots.

Validation results and CI links are recorded in the pull request.
