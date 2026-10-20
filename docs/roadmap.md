# Roadmap

What Pocket Documents does not do yet, roughly in the order it might:

- **Mail fetching** (IMAP accounts whose attachments become documents). Until then: a mail rule that saves attachments into the consume folder, or an n8n flow posting to the API.
- **Barcodes**: ASN barcodes and separator sheets that split a batch scan into documents.
- **Split / merge / rotate** pages inside the app (today: Stirling-PDF or similar, then re-upload).
- **Document versions** (a new file replacing the old, with history).
- **Permissions between people** (sharing a tag or a document with another account). Everything is strictly per person by design.
- **Multiple languages per document** detected automatically.

Said no to: a message queue, a second database, a job runner outside the binary.
