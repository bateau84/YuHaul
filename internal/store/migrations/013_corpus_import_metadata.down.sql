-- Reverses migration 013. Dropping the singleton metadata row only discards
-- the recorded completion time; the next successful import recreates it
-- through the same migration on the next forward run. No corpus row, title
-- fingerprint, or import history is affected.

DROP TABLE IF EXISTS corpus_import_metadata;
