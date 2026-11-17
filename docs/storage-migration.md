# ストレージ世代2への移行

旧v0.23.0のデータディレクトリを移行用サーバーへコピーし、Pythonで新構造に変換します。新しいAgentlogは旧DBを直接開きません。移行中は本番を停止し、移行完了まで新しい通信を送らないでください。

移行スクリプトはPythonの標準ライブラリだけで動作し、Goのインストール・ビルド・バイナリ実行は不要です。`uv`が必要なPython 3.14以降を選択します。SQLiteはFTS5・R-tree・contentless-deleteに対応した3.43以降を使用します。

## 1. 旧データを取得

旧サービスのDB準備が完了している状態で停止します。

```sh
cd ~/agentlog
sudo docker compose stop agentlog
```

`data`ディレクトリを丸ごと移行用サーバーにコピーします。

```text
data/
├── agentlog.db
├── agentlog.db.ingest.db
├── agentlog.db-wal                # 存在する場合は一緒にコピー
├── agentlog.db-shm                # 存在する場合は一緒にコピー
├── agentlog.db.ingest.db-wal       # 存在する場合は一緒にコピー
├── agentlog.db.ingest.db-shm       # 存在する場合は一緒にコピー
└── archives/                      # 作成済みのアーカイブがある場合
    └── <archive-id>.zip
```

停止しただけでは、強制終了後などのWALが残っている場合があります。DB本体だけを抜き出さず、ディレクトリごとコピーしてください。監視元のセッションファイルはこの移行には不要です。

## 2. 移行用サーバーで変換

```sh
git clone https://github.com/sasakiy84/agent-log-collector-and-viewer.git
cd agent-log-collector-and-viewer
git rev-parse HEAD  # 本番もこのコミットに揃える

uv run scripts/migrate_db.py \
  --source /srv/migration/old-data/agentlog.db \
  --output /srv/migration/new-data
```

`--output`はまだ存在しないディレクトリを指定します。元のDBは読み取り専用で開き、変更しません。受信DBとアーカイブの配置を変えた場合だけ、次の引数を追加します。

```sh
  --inbox /別の場所/agentlog.db.ingest.db \
  --archives /別の場所/archives
```

変換後にID索引を辞書化し、原本SHA-256を計算します。IDの元キー・alias・検索結果を照合し、不要になった索引の空きページをincremental vacuumで回収します。原本JSONは圧縮・再シリアライズしません。

出力は次の一式です。

```text
new-data/
├── agentlog.db
├── agentlog.db.ingest.db
├── archives/                     # コピー対象がある場合
└── migration-report.json
```

スクリプトは原本・解析済みデータ・管理情報を新しいテーブルへ移送し、全列の型と値を含むSHA-256をテーブル単位で照合します。原本JSONのBLOB、APIキー、削除済みレコードのハッシュ、セッション更新番号を保持します。日本語検索のFTS索引はトークン位置を含めて移送し、別の形態素解析器による検索結果の変化を避けます。

受信済みで本体に未反映のキュー、監視チェックポイント、AUTOINCREMENTの採番位置も移します。未反映の受信キューがあること自体はエラーではありません。作成済みアーカイブをコピーし、ダウンロード可能なZIPはDBに記録されたサイズとSHA-256で照合します。

SQLiteの整合性・外部キー・FTS索引の検証まで成功した場合だけ、出力ディレクトリを完成状態として公開します。通常のエラー・Ctrl+Cでは作業用ディレクトリを削除し、既存の出力を上書きしません。強制終了や電源断で残った`.migrating-*`ディレクトリは完成品ではなく、移行を再実行した後に削除できます。

次の場合は具体的な理由を表示して停止します。

- 旧スキーマがv0.23.0ではない。
- 旧サービスの履歴整備や派生データ更新が未完了。
- 受信DBがない、または必要なアーカイブが欠落・破損している。
- 未知のテーブルがある、列が一致しない、データ検証に失敗する。

履歴整備が未完了の場合は、元の旧サービスで準備を完了させて停止し、データを再取得してください。移行スクリプト内で旧Goバイナリを実行することはありません。

## 3. 本番へ戻す

完成した`new-data`を丸ごと本番サーバーへコピーします。旧データディレクトリを残したうえで、新しいデータディレクトリに置き換え、新版のAgentlogを起動します。

```sh
cd ~/agentlog
git fetch origin
git switch --detach <移行に使用したコミットID>
sudo mv data data.before-storage-v2
sudo mv new-data data
sudo docker compose up -d --build
```

配置したデータは、Composeの`AGENTLOG_UID` / `AGENTLOG_GID`に指定しているユーザーが読み書きできる所有者にしてください。移行元と同じ`.env`・`config.json`・監視元のマウント設定を使用します。`AGENTLOG_TOKEN`はDBには保存されていないため、既存の`.env`を引き継ぎます。

新版でセッション、検索、Skills、Settingsを確認してから送信・同期を再開します。新しいデータを受信した後は、旧DBへ戻すとその受信分を失うため、切り戻す場合は新データも保管してください。

## 移行スクリプトの検証

```sh
uv run scripts/migrate_db_test.py
```

凍結したv0.23.0スキーマの合成データで、原本のバイト一致、コミット済みWAL、キュー・監視状態、日本語FTS・フレーズ検索、R-tree、APIキー、アーカイブ、未完了状態の拒否、上書き防止を確認します。
