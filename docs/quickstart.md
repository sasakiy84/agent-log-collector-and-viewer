# QuickStart: ~/agentlog/ + Docker Compose + Nginx

Ubuntu/Debianサーバー向け。Git、Docker Compose v2、OpenSSLを使います。

```text
~/agentlog/
├── Dockerfile
├── compose.yaml
├── compose.override.yaml
├── .env
├── config.json
├── sessions/
│   ├── codex/
│   ├── claude/
│   └── copilot/
├── data/           # SQLite（コンテナを作り直しても残る）
└── …               # ビルド用のgo.mod、cmd/、internal/など
```

## 1. 配置・起動

```sh
git clone https://github.com/sasakiy84/agent-log-collector-and-viewer.git "$HOME/agentlog"
cd "$HOME/agentlog"
cp docs/examples/compose.watch.yaml compose.override.yaml
cp docs/examples/agentlog.docker.watch.json config.json
mkdir -p sessions/codex sessions/claude sessions/copilot data
printf 'AGENTLOG_TOKEN=%s\nAGENTLOG_UID=%s\nAGENTLOG_GID=%s\n' \
  "$(openssl rand -hex 32)" "$(id -u)" "$(id -g)" > .env
chmod 700 sessions data
chmod 600 .env config.json

docker compose up -d --build
```

既存DBは初回起動時に表示用・全文検索用索引を作成します。完了後にHTTPの待ち受けを開始し、中断時は次回起動で続きから処理します。索引とSQLiteの一時ファイルは`data/`に保存されるため、その分の空き容量が必要です。

起動ログにDB初期化の各段階を表示します。スキル履歴の索引はHTTP開始後にバックグラウンドで作成します。

コンテナは`.env`のUID/GIDで動きます。ホストのユーザーだけが読めるログも取り込め、`data/`も同じユーザーの所有になります。

`compose.override.yaml`はComposeが自動で読み込みます。`~/agentlog/sessions/`をコンテナの`/sessions`へ読み取り専用でマウントし、既存分・新規ファイル・追記を1秒間隔で取り込みます。

JSONLはエージェントごとに配置します。内容からの自動判別にも対応していますが、この設定ではフォルダごとに形式を指定しています。各エージェント内のディレクトリ構造はそのまま保ってください。

| 配置先 | コピーする内容 |
| --- | --- |
| `~/agentlog/sessions/codex/` | Codexの`sessions/`、`archived_sessions/`の内容 |
| `~/agentlog/sessions/claude/` | Claude Codeの`projects/`の内容。セッションの`subagents/`も保つ |
| `~/agentlog/sessions/copilot/` | Copilotの`session-state/`の内容。各セッション内の`events.jsonl`と分割ログをまとめて配置 |

```sh
cp -R /path/to/codex/sessions/. "$HOME/agentlog/sessions/codex/"
cp -R /path/to/claude/projects/. "$HOME/agentlog/sessions/claude/"
cp -R /path/to/copilot/session-state/. "$HOME/agentlog/sessions/copilot/"
```

`~/agentlog/config.json`:

```json
{
  "watch": [
    { "dir": "/sessions/codex", "source": "codex" },
    { "dir": "/sessions/claude", "source": "claude" },
    { "dir": "/sessions/copilot", "source": "copilot" }
  ]
}
```

プロジェクト別に分ける場合は、例えばClaudeのログを`sessions/claude/shop/`に置き、Claudeの監視先を変更します。

```json
{
  "watch": [
    { "dir": "/sessions/codex", "source": "codex" },
    { "dir": "/sessions/claude/shop", "source": "claude", "project": "shop", "tags": ["backend"] },
    { "dir": "/sessions/copilot", "source": "copilot" }
  ]
}
```

設定のパスはコンテナ内のパスです。追加項目は[監視設定](import.md#local-collector)を参照してください。

## 2. ホスト側のNginx

ドメインをサーバーへ向け、証明書・秘密鍵を用意します。`agents.example.com`と証明書パスを実際の値に変更してください。

```sh
sudo apt-get update
sudo apt-get install -y nginx
sudo tee /etc/nginx/conf.d/agentlog.conf > /dev/null <<'EOF'
server {
    listen 443 ssl;
    server_name agents.example.com;
    ssl_certificate /etc/nginx/certs/agents.example.com.crt;
    ssl_certificate_key /etc/nginx/certs/agents.example.com.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 16m;

    location / {
        proxy_read_timeout 300s;
        send_timeout 300s;
        proxy_pass http://127.0.0.1:4318;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
EOF

sudo systemctl enable --now nginx
sudo systemctl reload nginx
```

https://agents.example.com を開き、次のファイルの`AGENTLOG_TOKEN=`以降の値でログインします。

```sh
cat "$HOME/agentlog/.env"
```

Nginxは`127.0.0.1:4318`へ中継します。ホームディレクトリへのアクセス権は不要です。ネイティブOTELも送る場合の設定は[CLI設定](senders.md)を参照してください。

## 設定変更・ログ・停止

```sh
cd "$HOME/agentlog"

# config.jsonを変更した後
# .envを変更した後は docker compose up -d --force-recreate
docker compose restart

# ログ
docker compose logs -f

# 停止（sessions/とdata/は残る）
docker compose down
```

[1か月動きのないセッションのアーカイブ・DB容量の回収](archive.md)
