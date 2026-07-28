-- MySQL/MariaDB port of the Postgres schema of the same version. Both
-- directories share one version sequence: NNNN here is the same change as
-- NNNN in ../postgres.
--
-- Conventions that recur throughout this directory, not repeated per file:
--
--   * BIGINT GENERATED ALWAYS AS IDENTITY -> BIGINT AUTO_INCREMENT.
--   * TIMESTAMPTZ -> DATETIME(6). Neither type stores a zone, so the CMS
--     writes UTC and expects the session time zone to be UTC (see the DSN
--     guidance on cms.Config.Dialect).
--   * A keyed TEXT column becomes VARCHAR(n): InnoDB cannot index TEXT
--     without a prefix length. Postgres narrows the same columns in 0022,
--     so both engines enforce identical limits.
--   * DEFAULT '' on a TEXT column becomes the expression form DEFAULT (''),
--     which is the only spelling MySQL accepts for TEXT.
--   * Foreign keys are declared as table-level FOREIGN KEY clauses. InnoDB
--     parses column-level REFERENCES and then silently ignores it, so the
--     inline form used in the Postgres files would create no constraint.
--   * CHECK constraints are named explicitly: later migrations drop them by
--     name, and MySQL would otherwise generate names like cms_users_chk_1.

CREATE TABLE cms_users (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    email         VARCHAR(255) NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT (''),
    password_hash TEXT NOT NULL,
    role          VARCHAR(32) NOT NULL DEFAULT 'editor',
    active        BOOLEAN NOT NULL DEFAULT true,
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT cms_users_role_check CHECK (role IN ('admin', 'editor'))
);

CREATE TABLE cms_sessions (
    token  VARCHAR(128) PRIMARY KEY,
    data   LONGBLOB NOT NULL,
    expiry DATETIME(6) NOT NULL
);

CREATE INDEX cms_sessions_expiry_idx ON cms_sessions (expiry);
