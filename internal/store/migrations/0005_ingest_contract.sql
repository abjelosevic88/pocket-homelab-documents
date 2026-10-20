-- Reserve IDs before placing original files; an interrupted upload never
-- leaves a file whose ID can be reused by a later upload.
CREATE TABLE document_sequence(singleton INTEGER PRIMARY KEY CHECK(singleton=1), value INTEGER NOT NULL);
INSERT INTO document_sequence VALUES(1,(SELECT COALESCE(MAX(id),0) FROM documents));
