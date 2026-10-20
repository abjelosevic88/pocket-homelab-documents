# Configuration

Everything is an environment variable; every one has a default, so `docker run` with one volume works.

## Server

| Variable | Default | Notes |
|---|---|---|
| `HOST` / `PORT` | `0.0.0.0` / `8080` | |
| `BASE_URL` | – | Public URL. Needed for share links, the OIDC callback and links in notifications. |
| `DATA_DIR` | `/data` (image) / `./data` | Everything lives under it unless the paths below say otherwise. |
| `DB_PATH` | `DATA_DIR/pocket-documents.db` | SQLite, WAL mode. Keep it on local disk, not NFS. |
| `ORIGINALS_DIR` | `DATA_DIR/originals` | Uploaded files as they came, `<id>.<ext>`. |
| `ARCHIVE_DIR` | `DATA_DIR/archive` | The PDF/A twins with the text layer, `<id>.pdf`. |
| `THUMBS_DIR` / `PAGES_DIR` | `DATA_DIR/thumbs`, `DATA_DIR/pages` | Thumbnails; page images rendered on demand (a cache, safe to delete). |
| `CONSUME_DIR` | `DATA_DIR/consume` | The watched folder. |
| `MIRROR_DIR` | `DATA_DIR/mirror` | The browsable copy laid out by storage path; switched on in Settings. |
| `TMP_DIR` | `DATA_DIR/tmp` | Uploads and OCR scratch space. Big scans need room here. |
| `MAX_UPLOAD_BYTES` | 200 MB | Per file. |
| `TRUST_PROXY` | `true` | Use `X-Forwarded-For` for the client IP. |
| `METRICS_ENABLED` | `true` | `GET /metrics` for Prometheus. |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug` logs every request; `json` for a log collector. |
| `DEMO_DATA` | `false` | Seed six sample documents into an empty database. |

## OCR

| Variable | Default | Notes |
|---|---|---|
| `OCR_LANGUAGE` | `eng` | Tesseract codes joined with `+`, e.g. `eng+deu+hrv`. Admins can change it in Settings; the image ships the packs listed in the Dockerfile's `OCR_LANGS`. |
| `OCR_MODE` | `skip` | `skip` OCRs only pages without text; `redo` redoes scanned pages but keeps real text; `force` rasterises everything. Per document from the ⋯ menu. |
| `OCR_WORKERS` | `1` | Documents processed at once. |
| `OCR_THREADS` | min(4, CPUs) | Threads OCRmyPDF may use per document. |
| `OCR_TIMEOUT` | `30m` | Per document. |
| `OCR_DESKEW` / `OCR_ROTATE` / `OCR_CLEAN` | `true` / `true` / `false` | Straighten, auto-rotate, clean with unpaper. |
| `OCR_MAX_PAGES` | `0` | OCR only the first N pages (0 = all). |
| `OCR_OUTPUT_TYPE` | `pdfa` | `pdfa` or `pdf`. |
| `OCR_IMAGE_DPI` | `300` | Assumed for photos and images without a DPI. |
| `GOTENBERG_URL` | – | With a [Gotenberg](https://gotenberg.dev) container, office documents, HTML, Markdown and text get a PDF twin too. Without it their text is still read natively. |
| `CONSUME_POLL` | `10s` | How often the consume folder is looked at. A file is taken once its size is stable for two polls. |
| `CONSUME_DELETE` | `true` | Remove the file from the consume folder once it is in (`false` copies it; the duplicate check keeps it from coming in twice). |

## Auth

| Variable | Default | Notes |
|---|---|---|
| `AUTH_MODE` | `local` | `local`: built-in login, multi-user. `proxy`: trust `AUTH_PROXY_HEADER` (`Remote-User`) and `AUTH_PROXY_EMAIL_HEADER` from your SSO proxy; users are created on first visit. `none`: one implicit user, no login, for a trusted LAN only. |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | Create the first admin without the setup screen. |
| `SESSION_TTL` | `720h` | |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS. |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` / `OIDC_NAME` | – | Sign in with any OpenID Connect provider (callback `BASE_URL/api/v1/auth/oidc/callback`). `OIDC_AUTO_CREATE=false` refuses emails without an account. |

## Pocket Core

| Variable | Default | Notes |
|---|---|---|
| `CORE_URL` | – | With a Pocket Core on the box the app registers itself on start, publishes `documents.*` events, accepts the tokens Core hands to paired phones, offers "Sign in with Pocket Core" with no OIDC configuration, and books in Pocket Money through an app link. |
| `CORE_REGISTRATION_KEY` | – | From Core's Settings page. |
| `CORE_APP_URL` | `BASE_URL` | How Core reaches this app (e.g. `http://host.docker.internal:8183`). |

## Where things are

```
DATA_DIR/
  pocket-documents.db     metadata, text, search index, users, settings
  originals/<id>.<ext>    what was uploaded
  archive/<id>.pdf        PDF/A with the text layer (when one could be made)
  thumbs/<id>.jpg
  pages/<id>/<n>.jpg      page images, rendered on demand (cache)
  consume/                drop files here
  mirror/                 the browsable copy (when switched on)
  backups/                `pocket-documents backup` and Settings → Download database
  core.token              the Pocket Core app token (when registered)
```

## CLI

```
pocket-documents                 start the server
pocket-documents backup -o FILE  consistent SQLite copy (VACUUM INTO)
pocket-documents reindex         rebuild the full-text index
pocket-documents reset-password [-email USER] [-password NEW]
```
