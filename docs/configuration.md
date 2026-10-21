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
| `FOLDER_ROOTS` | `MIRROR_DIR` | Comma-separated server folders people may choose as their *documents folder* (Settings → Documents folder → Server folder). Mount a NAS share and list it here. |
| `MIRROR_DIR` | `DATA_DIR/mirror` | The server folder offered when `FOLDER_ROOTS` is not set. |
| `FOLDER_INBOX_POLL` | `2m` | How often every person's documents-folder Inbox is looked at. A file is taken once it looks the same on two looks. |
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
| `DATA_DIR/tessdata` | – | Language packs installed from Settings → OCR → Languages land here (tessdata_best models); the image's own packs are linked in and `TESSDATA_PREFIX` points Tesseract at it. The image ships `srp` and `srp_latn` from tessdata_best. |
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
| `CORE_URL` | – | With a Pocket Core on the box the app registers itself on start, publishes `documents.*` events, accepts the tokens Core hands to paired phones, offers "Sign in with Pocket Core" with no OIDC configuration, books in Pocket Money and keeps the documents folder in Pocket Files through app links. |
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
  mirror/                 the default server folder people may choose as their documents folder
  backups/                `pocket-documents backup` and Settings → Download database
  core.token              the Pocket Core app token (when registered)
```

## Barcodes

Settings → OCR → Barcodes (admins, needs zbar, in the image): on every new PDF or image the first pages are read for barcodes. A code `ASN00042` (the prefix is configurable) sets the archive serial number. A separator sheet, a page whose barcode reads `PATCHT` (configurable; any Code 128 / Code 39 / QR works), splits a batch scan into one document per section; the batch itself goes to the trash. Print separator sheets from any barcode generator.

## Mail

Settings → Mail: IMAP accounts and rules, like Paperless'. A rule says which folder to watch, what the mail must look like (sender, subject, attachment name), what to take (attachments, the mail as `.eml`, or both), what to assign, and what to do with the mail afterwards (mark read, flag, move, delete). Accounts are polled on their own interval; a message is consumed once per rule.

## The documents folder

Each person may choose, in Settings → Documents folder, a place the app keeps in step with their archive: **None**, a **server folder** (one of `FOLDER_ROOTS`), **Pocket Files** (through Pocket Core only: a folder inside their files, `Documents` by default), **Nextcloud** (their account through Pocket Core, or an address + app password typed in; a folder inside their files, `Documents` by default) or any **WebDAV** server. There the app writes one file per document (the PDF/A with its text layer, or the original), laid out by storage path, and renames, re-files and removes it as the document changes. Its **Inbox** sub-folder works the other way: files dropped there (from the Nextcloud phone app, a synced folder, a scanner) become documents and are removed from the Inbox once in; sub-folders of the Inbox become tags. The archive itself always stays under `DATA_DIR`: that is what OCR, previews and backups use. Credentials typed in are stored in the database; the ones from Pocket Core are asked for on every use and never stored.

**Pocket Files** is reached with a Core app link (`files:read` + `files:write`, declared in the manifest, so Core creates it on registration): for each person the app asks Core for a short-lived `pcx_` token for that person only and talks WebDAV to `<files url>/dav/<folder>` with it as the password; a token is used for at most four minutes, then a new one is asked for. Switching the link off in Core (Links, or the person's Settings → Apps acting for me) stops the folder at once, and Settings → Documents folder says why. Without `CORE_URL` the choice is not offered. Copies the app removes go to the person's Pocket Files trash; documents taken from its Inbox get the source *Pocket Files Inbox*.

## CLI

```
pocket-documents                 start the server
pocket-documents backup -o FILE  consistent SQLite copy (VACUUM INTO)
pocket-documents reindex         rebuild the full-text index
pocket-documents reset-password [-email USER] [-password NEW]
```
