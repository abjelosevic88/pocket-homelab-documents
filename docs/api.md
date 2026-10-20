# HTTP API

Everything the web app does goes through `/api/v1`. Authenticate with the session cookie, with `Authorization: Bearer <token>` (Settings → API tokens), or with `X-API-Key`. Every answer is scoped to the token's person.

## Documents

| | |
| --- | --- |
| `GET /documents?q=&tags=1,2&tags__none=&tags__any=&correspondent=&document_type=&storage_path=&created=2024&created_from=&created_to=&added=&inbox=1&untagged=1&favorite=1&status=&mime=&asn=&field=<id>:<value>&ids=&sort=-created&limit=50&offset=0&page=&content=1&no_facets=1` | `{results, count, facets, offset, limit, query}`. `q` takes the [search syntax](#search-syntax). `correspondent=none` for unset. Sort: `created added modified title asn size pages correspondent type notes rank` with `-` for descending. |
| `POST /documents` (multipart `document`, more than one allowed; fields `title created tags correspondent document_type storage_path asn allow_duplicates`) | 201 with the document (or `{results, added}` for several). 409 with `duplicate_of` when the same file is already in. Also `POST /documents/post_document` for Paperless clients, and a raw body with `X-File-Name`. |
| `GET /documents/{id}` | `{document (with content), prev, next, job?}` |
| `PUT /documents/{id} {title, content, correspondent, document_type, storage_path, asn, created, favorite, tags[], custom_fields[{field, value}], remove_inbox_tags}` | only the keys sent change |
| `DELETE /documents/{id}` · `POST …/restore` · `DELETE …/purge?force=1` | trash, back, gone |
| `GET /documents/{id}/download?original=1&inline=1` · `/preview` · `/thumb` · `/pages/{n}.jpg` · `/text` | the PDF/A (or original), inline, thumbnail, a page image, the text |
| `POST /documents/{id}/reprocess {mode: skip\|redo\|force, force_text}` | queue OCR again |
| `GET /documents/{id}/suggestions` · `POST …/suggestions/apply` | `{tags, correspondents, document_types, storage_paths, dates, confidence, trained_on}` |
| `GET /documents/{id}/similar?limit=` | documents sharing the most telling words |
| `GET/POST /documents/{id}/notes {text}` · `DELETE …/notes/{nid}` | |
| `GET /documents/{id}/history` | who changed what, when |
| `GET/POST /documents/{id}/shares {days, file: archive\|original}` · `GET /shares` · `DELETE /shares/{id}` | public links `/s/<token>` |
| `POST /documents/{id}/money {amount, currency, account_id, category_id, date, description, payee}` · `GET /money/status` | book in Pocket Money |
| `POST /documents/bulk {ids[], op, …}` | `op`: `add_tag` (`tag` or `add_tags[]`), `remove_tag`, `modify_tags {add_tags, remove_tags}`, `set_correspondent`, `set_document_type`, `set_storage_path`, `set_favorite`, `set_fields {custom_fields, remove_fields}`, `remove_inbox`, `delete`, `restore`, `purge`, `reprocess {mode}` |
| `GET /documents/next_asn` | |

## Labels

`GET/POST /tags`, `PUT/DELETE /tags/{id}` with `{name, color, match, matching_algorithm, is_insensitive, is_inbox}`; the same for `/correspondents`, `/document_types`, `/storage_paths` (`+ path`) and `/custom_fields` (`{name, data_type, extra: {options[], currency}}`). `GET /taxonomy` returns all of them plus saved views in one answer.

Matching algorithms: `0` none, `1` any word, `2` all words, `3` exact phrase, `4` regular expression, `5` fuzzy, `6` auto (learned).

## Views, rules, jobs

| | |
| --- | --- |
| `GET/POST /saved_views`, `PUT/DELETE /saved_views/{id}` | `{name, query: {…list parameters}, on_dashboard, in_sidebar, position}` |
| `GET/POST /rules`, `PUT/DELETE /rules/{id}`, `POST /rules/{id}/run` | `{name, enabled, trigger: added\|updated, filters: {filename, path, source, mime, content, correspondent, document_type, tags[]}, actions: {title, add_tags[], remove_tags[], correspondent, document_type, storage_path, fields{}, favorite}}` |
| `GET /jobs?status=active\|failed&limit=`, `GET /jobs/{id}` (with log), `POST /jobs/{id}/retry`, `POST /jobs/{id}/cancel`, `DELETE /jobs` | |

## The rest

`GET /dashboard` (Homepage widget: `documents`, `inbox`, `untagged`, `pending`, `failed`, `added_today`, `added_week`, `storage_mb`, …), `GET /statistics`, `GET /changes` (a stamp that moves when anything changes), `GET /trash`, `DELETE /trash`, `GET /export?no_archive=1` (zip), `POST /import` (multipart zip: ours or a Paperless export), `POST /import/paperless {url, token}` and `…/test`, `POST /reindex`, `POST /retrain`, `GET/PUT /settings`, `GET /settings/system`, `GET /users`…, `GET /tokens`…, `GET /backup` (admin), `GET /healthz`, `GET /metrics`.

## Search syntax

`invoice ikea` both words (prefixes match) · `"amount due"` phrase · `-draft` without · `tag:x` `#x` `-tag:x` · `from:x` `type:x` `path:x` (`none` for unset) · `created:2024` `created:2024-05` `created:2023..2024` `created:>2024-01-01` `created:30d` `created:month` · `added:…` · `asn:12` `asn:10..20` · `is:inbox` `is:untagged` `is:favorite` `is:failed` `is:pdf` `is:image` · `has:notes` `has:asn` · `field:name=value` · `title:x`.

## Examples

```sh
# upload with tags and a title
curl -F document=@scan.pdf -F tags=1,2 -F title="Water bill" -H "Authorization: Bearer pd_…" https://documents.example.com/api/v1/documents

# everything from IKEA this year, as JSON
curl -H "Authorization: Bearer pd_…" "https://documents.example.com/api/v1/documents?q=from:ikea+created:year"

# the PDF/A
curl -OJ -H "Authorization: Bearer pd_…" https://documents.example.com/api/v1/documents/12/download
```
