-- A page's search description stops being the same column as its summary.
-- See the Postgres file of the same name for why.
--
-- The default is parenthesized because MySQL only accepts a default on a
-- TEXT column as an expression, which is how every other TEXT column here
-- is written.
ALTER TABLE cms_page_meta
    ADD COLUMN meta_description TEXT NOT NULL DEFAULT ('');
