ALTER TABLE exchanges
    ADD COLUMN invite_code VARCHAR(16) NULL;

-- RAND() is re-evaluated per row, so every existing exchange gets its own code
-- before the unique key below is added.
UPDATE exchanges
SET invite_code = SUBSTRING(MD5(RAND()), 1, 12);

ALTER TABLE exchanges
    MODIFY COLUMN invite_code VARCHAR(16) NOT NULL,
    ADD UNIQUE KEY ux_exchanges_invite_code (invite_code);
