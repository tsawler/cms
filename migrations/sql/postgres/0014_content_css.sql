-- The generated content stylesheet: Tailwind CSS compiled from the class
-- tokens found in stored content (see Config.Tailwind). A singleton row;
-- class_hash is the content address served as /cms/content-<hash>.css.
CREATE TABLE cms_content_css (
    singleton  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    class_hash TEXT NOT NULL,
    css        TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
