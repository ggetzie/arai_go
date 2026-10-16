# arai_go

Go port of [AskRedditButAI](https://github.com/ggetzie/askreddit_but_ai) (askredditbut.ai). It replaces the Django app with one self-contained binary. Templates and static files are embedded in it.

The site shows 25 GPT-2 generated AskReddit titles a day, taken from the `main_generatedq` table (~85k rows). Visitors vote titles up or down. It uses the Django app's existing Postgres tables in place, so no data migration is needed.

## Commands

```
arai serve   [-env file]   run the web server (default 127.0.0.1:8007)
arai select  [-env file]   choose today's 25 questions (systemd timer, 00:01 UTC)
arai migrate [-env file]   apply the Go port's schema additions (idempotent)
arai hashpw                print a bcrypt hash for ARAI_ADMIN_PASSWORD_HASH
```

## Configuration

Settings come from environment variables. `-env` can load them from a `KEY=VALUE` file, and systemd uses the same file as its `EnvironmentFile=`. See `deploy/arai.env.example`.

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Postgres URL. If unset, it is built from the Django `.env` keys `DB_NAME`, `DB_USER`, `arai_DB_PW` (host localhost) |
| `ARAI_ADDR` | Listen address, default `127.0.0.1:8007` |
| `ARAI_SECRET_KEY` | ≥32 chars, signs the admin cookie (`openssl rand -base64 48`) |
| `ARAI_ADMIN_USER`, `ARAI_ADMIN_PASSWORD_HASH` | Admin login (`arai hashpw` makes the hash) |
| `ARAI_DEBUG` | `1` for local HTTP dev: no `Secure` cookies, no CSP header |

## Routes

| Path | Page |
|---|---|
| `/` and `/YYYYMMDD` | The day's questions with vote arrows (`POST /vote/`, JSON) |
| `/archive/` | Every day that has had questions |
| `/on-deck/` | Upvoted questions that haven't been tweeted |
| `/about/` | The "about" entry, rendered from markdown |
| `/admin/` | Login, edit About entries (markdown), search and edit questions |
| `/healthz` | Database health check |

## Database

- **Shared tables:** `main_generatedq` and `main_about` belong to the Django app. Their columns are never altered, so the Django and Go versions can run against the same database during cutover.
- **`arai migrate` adds:**
  - an index on `main_generatedq(displayed)`;
  - the `arai_vote_state` table. It holds each visitor's vote per question, keyed by the random `arai_sid` cookie, and replaces Django's Redis sessions. `arai select` deletes rows older than 30 days.

## Changes from the Django version

- **Votes:** each vote runs in one transaction with a row lock, so concurrent votes are no longer lost. An invalid direction is rejected. Django set the state to 0 but kept the vote, which allowed unlimited upvotes.
- **CSRF:** `http.CrossOriginProtection` (Fetch metadata / Origin) replaces Django's token, so `project.js` no longer sends one.
- **Vote state on the page:** delivered as a JSON `<script type="application/json">`, so the CSP needs no inline scripts.
- **Daily selection:** `arai select` uses the same algorithm as `manage.py select_questions`. It does nothing if today already has 25 questions, so a double or catch-up run can't add a second page.
- **Hosting:** static files are served from the binary rather than S3/CloudFront. Redis is no longer needed.
- **Markdown:** About pages use goldmark. The output for the existing entries is byte-identical to python-markdown's.

## Development

```sh
docker run -d --name arai-pg -e POSTGRES_USER=arai -e POSTGRES_PASSWORD=arai -e POSTGRES_DB=arai \
  -p 127.0.0.1:55432:5432 postgres:18

go test ./...                                    # unit tests only
ARAI_TEST_DATABASE_URL=postgres://arai:arai@127.0.0.1:55432/arai go test ./...   # plus integration tests
```

Integration tests create a throwaway schema per test from `internal/store/storetest/schema.sql`, which is a `pg_dump --schema-only` of the production Django tables.

To run against real data:
1. Create a database and load that schema into it (strip the `public.` prefixes).
2. Load a `pg_dump --data-only -t main_generatedq -t main_about` from production.
3. Run `arai migrate` and then `ARAI_DEBUG=1 arai serve` with a dev env file.

## Deployment (kotsf.com)

Build on a dev machine and copy over. The server has no Go toolchain.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o arai ./cmd/arai
scp arai ubuntu@kotsf.com:/tmp/arai
```

On the server:

1. **Install:**
   - `sudo install -o root -g root -m 0755 /tmp/arai /usr/local/src/askreddit_but_ai/go/arai`
   - Create `go/arai.env` from `deploy/arai.env.example`, owned by `arai_user` with mode `0600`.
2. **Schema:** `sudo -u arai_user go/arai migrate -env go/arai.env`
3. **Trial on port 8017**, with Django still serving on 8007:
   - `sudo systemd-run --unit=arai-trial --uid=arai_user --gid=webapps -p EnvironmentFile=/usr/local/src/askreddit_but_ai/go/arai.env /usr/bin/env ARAI_ADDR=127.0.0.1:8017 /usr/local/src/askreddit_but_ai/go/arai serve`
   - Set the address with `env`: systemd applies `EnvironmentFile=` after `--setenv`, so `ARAI_ADDR=127.0.0.1:8007` from the file would win.
   - Check it through `ssh -L 8017:127.0.0.1:8017`, then `sudo systemctl stop arai-trial`.
4. **Cutover:**
   - Copy `deploy/askreddit_but_ai.service`, `askreddit_but_ai-select.service` and `askreddit_but_ai-select.timer` to `/etc/systemd/system/`.
   - `sudo supervisorctl stop askreddit_but_ai`
   - `sudo systemctl daemon-reload && sudo systemctl enable --now askreddit_but_ai askreddit_but_ai-select.timer`
   - Back up the supervisor config with `sudo cp /etc/supervisor/conf.d/super_askreddit_but_ai.conf /usr/local/src/askreddit_but_ai/srv/production/super_askreddit_but_ai.conf.bak`. Then remove it with `sudo rm /etc/supervisor/conf.d/super_askreddit_but_ai.conf && sudo supervisorctl reread && sudo supervisorctl update`.
   - Replace the nginx site with `deploy/nginx-askreddit_but_ai.conf`, which drops the `/static/` alias. Then `sudo nginx -t && sudo systemctl reload nginx`.
   - Remove the `select_questions.sh` line from the `ubuntu` crontab. The systemd timer replaces it.
5. **Logs:** `journalctl -u askreddit_but_ai` and `journalctl -u askreddit_but_ai-select`
6. **Rollback:**
   - `sudo systemctl disable --now askreddit_but_ai askreddit_but_ai-select.timer`
   - Restore the supervisor conf and run `sudo supervisorctl update`.
   - Restore the old nginx site and crontab line.

   The database stays compatible with Django throughout.

## Not yet ported

Posting the top-voted question to Reddit and X (`manage.py tweet`) is planned as a follow-up.
