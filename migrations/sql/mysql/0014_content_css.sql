-- The generated content stylesheet: Tailwind CSS compiled from the class
-- tokens found in stored content (see Config.Tailwind). A singleton row;
-- class_hash is the content address served as /cms/content-<hash>.css.
CREATE TABLE cms_content_css (
    singleton  BOOLEAN NOT NULL DEFAULT TRUE PRIMARY KEY,
    class_hash TEXT NOT NULL,
    css        TEXT NOT NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT cms_content_css_singleton_check CHECK (singleton = TRUE)
);
