-- Where a document came from in another system: "paperless:123" for an import, or whatever
-- an app that files documents here says ("invoicing:invoice:42"). Unique per person when set.
ALTER TABLE documents ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX documents_user_external ON documents(user_id, external_id) WHERE external_id <> '';
