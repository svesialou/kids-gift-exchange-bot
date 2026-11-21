DROP TABLE IF EXISTS violations;

ALTER TABLE users
    DROP COLUMN banned_at,
    DROP COLUMN ban_reason;

ALTER TABLE exchanges
    DROP COLUMN title,
    DROP COLUMN registration_opens_at,
    DROP COLUMN registration_closes_at,
    DROP COLUMN order_checked_at,
    DROP COLUMN delivery_checked_at;
