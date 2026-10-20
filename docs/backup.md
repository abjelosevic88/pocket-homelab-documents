# Backup

Back up the whole data directory. Two things matter:

1. **The database** — `pocket-documents.db`: metadata, the OCR text, the search index, notes, history, users, settings. Take a consistent copy with `pocket-documents backup [-o FILE]` (SQLite `VACUUM INTO`) while the app runs, or download it from Settings → Import & export (admins). Don't copy the live `.db` with its `-wal` file.
2. **The files** — `originals/` (what was uploaded; the one copy that cannot be regenerated) and `archive/` (the PDF/A twins; regenerable by "Run OCR again", but slow). `thumbs/` and `pages/` are caches.

A full export (Settings → Import & export → Download everything) is a portable alternative: one zip with files and a `manifest.json` that imports anywhere.

Restore: stop the app, put the data directory back, start it. Jobs left running are queued again.
