# arai_go

Go port of [AskRedditButAI](https://github.com/ggetzie/askreddit_but_ai) (askredditbut.ai). It replaces the Django app with one self-contained binary. Templates and static files are embedded in it.

The site shows 25 GPT-2 generated AskReddit titles a day, taken from the `main_generatedq` table (~85k rows). Visitors vote titles up or down. It uses the Django app's existing Postgres tables in place, so no data migration is needed.

## Commands

```
arai serve   [-env file]   run the web server (default 127.0.0.1:8007)
arai select  [-env file]   choose today's 25 questions (systemd timer, 00:01 UTC)
arai migrate [-env file]   apply the Go port's schema additions (idempotent)
arai post reddit|x [-env file] [-dry-run]
                           post the top-voted question not yet posted there (timers, every 6 h)
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

The site went live on 2026-10-16. It replaced the Django/gunicorn/supervisor setup in `/usr/local/src/askreddit_but_ai`.

### Server layout

| Path | Contents |
|---|---|
| `/usr/local/src/arai_go/arai` | the binary (root, 0755) |
| `/usr/local/src/arai_go/arai.env` | settings (arai_user:webapps, 0600). See `deploy/arai.env.example` |
| `/usr/local/src/arai_go/logs/` | nginx access and error logs, rotated by `/etc/logrotate.d/arai_go` |
| `/etc/systemd/system/askreddit_but_ai.service` | web server on 127.0.0.1:8007 (`deploy/`) |
| `/etc/systemd/system/askreddit_but_ai-select.{service,timer}` | daily selection at 00:01 UTC (`deploy/`) |
| `/etc/systemd/system/askreddit_but_ai-post@.{service,timer}` | posting, enabled per platform as `askreddit_but_ai-post@reddit.timer` / `@x.timer` (`deploy/`) |
| `/etc/nginx/sites-available/askreddit_but_ai` | TLS and proxy (`deploy/nginx-askreddit_but_ai.conf`), symlinked from `sites-enabled` |

Logs: `journalctl -u askreddit_but_ai`, `journalctl -u askreddit_but_ai-select`, `journalctl -u 'askreddit_but_ai-post@*'`.

### Deploying a new version

Build on a dev machine and copy it over. The server has no Go toolchain.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o arai ./cmd/arai
scp arai ubuntu@kotsf.com:/tmp/arai
ssh ubuntu@kotsf.com 'sudo install -o root -g root -m 0755 /tmp/arai /usr/local/src/arai_go/arai && rm /tmp/arai \
  && sudo -u arai_user /usr/local/src/arai_go/arai migrate -env /usr/local/src/arai_go/arai.env \
  && sudo systemctl restart askreddit_but_ai'
```

To try a build before it replaces the live one, run it on another port:
- `sudo systemd-run --unit=arai-trial --uid=arai_user --gid=webapps -p EnvironmentFile=/usr/local/src/arai_go/arai.env /usr/bin/env ARAI_ADDR=127.0.0.1:8017 /path/to/new/arai serve`
- Set the address through `env`, because systemd applies `EnvironmentFile=` after `--setenv`.
- Check it through `ssh -L 8017:127.0.0.1:8017`, then `sudo systemctl stop arai-trial`.

To change the admin password, run this from a real terminal (not a `!` command):

```sh
ssh -t ubuntu@kotsf.com 'H=$(/usr/local/src/arai_go/arai hashpw) && sudo sed -i "s|^ARAI_ADMIN_PASSWORD_HASH=.*|ARAI_ADMIN_PASSWORD_HASH=$H|" /usr/local/src/arai_go/arai.env && sudo systemctl restart askreddit_but_ai'
```

### Rolling back to Django

The Python app is still in `/usr/local/src/askreddit_but_ai`. Its old supervisor config, crontab and nginx symlink target are saved in `srv/production/pre-go-backup-20261016/`.

1. `sudo systemctl disable --now askreddit_but_ai askreddit_but_ai-select.timer`
2. Copy `super_askreddit_but_ai.conf` back to `/etc/supervisor/conf.d/` and run `sudo supervisorctl update`.
3. Point `/etc/nginx/sites-enabled/askreddit_but_ai` back at the saved target and reload nginx.
4. Restore the crontab from `ubuntu.crontab`.

The database works with both versions. Go only adds an index and the `arai_vote_state` table.

## Posting to Reddit and X

`arai post reddit` and `arai post x` each post the question at the head of that platform's own queue and then mark it posted. The queue holds questions with more than 0 votes that were shown before today and haven't been posted to that platform yet. It is ordered by most votes, then longest-waiting. The systemd timers run each command at 01, 07, 13 and 19 UTC. `-dry-run` shows the next question without posting.

The Django version posted only one question per run. It always posted to Reddit, but it marked the question only when the tweet worked. Once X started failing in February 2026, the same question went to Reddit every 6 hours. Now:
- Each platform has its own flag: `submitted`/`reddit_id` for Reddit, `tweeted` for X.
- A question is marked only after its own post succeeds.
- A per-platform lock keeps overlapping runs from posting the same question twice.
- Reddit rejections that come back as HTTP 200 with an `errors` list now count as failures.

If a question keeps failing (for example, a title Reddit refuses), it blocks that queue. Mark it posted in the admin to skip it.

### Reddit

- **Credentials:** the old "script" app (`arai_Reddit_*` keys) logs in with the password grant.
- **Public API shutdown** (announced 2026-09-30):
  - New access requests close 2026-10-31.
  - Apps that haven't registered at <https://developers.reddit.com/app-registration> lose access from 2027-01-12.
  - All public access ends in March 2027.
- **Before 2027-01-12:** register the existing app so this keeps working until March 2027. The migration bounty for apps that move to Devvit closes 2026-11-30.
- **The long-term plan is a Devvit app installed on r/AskRedditButAI.** A Devvit scheduler task (`devvit.json` `scheduler.tasks` with a cron) would:
  1. fetch the next question from an authenticated askredditbut.ai endpoint (the domain must be allow-listed under `permissions.http.domains`);
  2. post it with the Devvit Reddit API;
  3. report the post id back.

### X

X has had no free API tier since its 2026 move to pay-per-use billing. Posts are prepaid credits at about $0.015 each, or $0.20 if the post contains a URL. At 4 posts a day that's roughly $2 a month. `arai post x` signs requests with OAuth 1.0a (`arai_Twitter_*` keys, Read and Write permission). It reports HTTP 402 as "out of credits". X's "duplicate content" refusal is treated as already posted. Enable `askreddit_but_ai-post@x.timer` once billing is set up in the X Developer Console.
