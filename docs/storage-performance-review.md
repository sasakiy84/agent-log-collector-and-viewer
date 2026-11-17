# ストレージ世代2の性能レビュー

取り込み、検索・集計、容量・通信の3回に分けてレビューし、P1〜P3の観点で実装を再確認しました。

| 指摘 | 対応 |
|---|---|
| P2: レコードごとに同じSQLを再prepare | 取り込みtransaction内だけ固定statementを再利用。末尾空白を除去し、複数SQLは明示的に分割 |
| P2: 変更のない再送でも日本語解析 | read poolでID・原本ハッシュを事前確認し、writer内で再確認。ハッシュ未設定時だけBLOB比較。同じバッチの同ID更新・墓標も保持 |
| P2: プロジェクト検索の前に全FTS一致をカード参照 | FTSを起点にしたまま、documentのsessionでproject/source/directoryを先に絞る。レコード単位の時間・種別・origin一致は維持 |
| P2: ID文字列を索引の各行へ反復保存 | 元キー・aliasを辞書化し、既存の明示的な整数document IDで参照。covering検索を維持 |
| P2: 移行後に旧索引の空きページが残る | オフライン変換の最後にincremental vacuum。区間索引が参照する暗黙rowidは変更しない |
| P3: 詳細のページごとにスキル根拠を全集計 | 同じread snapshotのsession revision・originで上限付きキャッシュ。変更・削除後は再集計 |
| P3: 一覧に不要なusage詳細を転送 | UIはcompact=1を指定。SQLでvariants/basisを省略。APIの既定応答は保持 |
| P3: 詳細を見るだけで原本JSONも転送 | Detailsはraw=0。JSONタブはview=rawで原本だけ取得し、再解析・関連情報取得を省略。切替・中断・コピー対象も確認 |
| P3: 不要な修復・索引・説明が残存 | 下記の削除監査を実施 |

## ローカル計測

同じGo Saveハーネス、メモリSQLiteでの比較です。ファイルI/O・本番ネットワークの保証値ではありません。

| 条件 | 変更前 | 変更後 |
|---|---:|---:|
| 逆順delta metric 2,000件 | 4,777ms | 1,868ms |
| 約600KBの日本語ログ再送 | 287〜294ms | 2.3〜3.0ms |
| 同再送の累計メモリ割当 | 約39MiB | 0.1MiB未満 |
| 新着の同ログ | 343ms | 351ms |

サンプル3,262原本で、ID索引領域は9,789,440から4,358,144 bytesへ約55%減りました。元キー・aliasの復元結果は一致します。27.7万イベントの偏りのある合成セッションは最新会話取得が初回31.72ms、繰り返し中央値6.33ms、ブラウザ初期表示372.76msでした。

原本JSONの圧縮は導入していません。サンプルの原本だけなら大きく縮みますが、DB全体では索引の比率も大きいため、辞書化を先に適用しました。原本ハッシュ・raw_bytes・エクスポートの意味は維持します。HTTP JSON圧縮はホストNginxのQuickStart設定に追加し、ZIPやイベントストリームをgzip_typesへ加えていません。

## 削除監査

- 履歴走査専用のcards_agent_candidates / cards_title_backfill / cards_presentation_pendingとpresentation_ready列。
- 生成経路がなくなったpending_storageビュー・refreshRecordTotals修復処理。通常のrecord_totals更新トリガーは保持。
- 未使用のsearchMatch / scanSkillsとテスト専用createArchiveラッパー。
- 本番で常時trueだったprocessingCountSQLのready引数・分岐。直接SQLとの比較テストは保持。
- 旧スキーマを新サーバーで自動修復するブラウザテスト分岐。v0.23からのPython移行テストは保持。
- 未到達のSkills履歴索引状態表示、旧バックフィルの孤立コメント、廃止テストへのリンク・再起動時再取り込みの説明。

原本・照合キー・元のIDキー、使用量の区間索引、墓標・usage_tombstonesは残しています。削除済みログの復活防止と重複計上防止に必要です。確定済みschema.sqlは変更せず、追加migrationとPython変換で不要物を除去します。

## 検証

Go通常テスト・関連raceテスト・vet・静的検査、Python移行10件、実v0.23からの60 API比較を実行しました。会話・JSON遅延取得・検索条件・Skills・アーカイブ削除・プロジェクト件数をブラウザで確認し、PC/モバイルの画面を点検しました。移行は原本BLOB、FTS、R-tree、欠番rowid、監視状態、ACK済みキュー、キー、アーカイブを保持します。
