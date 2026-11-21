ALTER TABLE exchanges
    ADD COLUMN title                   VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN registration_opens_at   DATETIME     NULL,
    ADD COLUMN registration_closes_at  DATETIME     NULL,
    ADD COLUMN order_checked_at        DATETIME     NULL,
    ADD COLUMN delivery_checked_at     DATETIME     NULL;

ALTER TABLE users
    ADD COLUMN banned_at  DATETIME     NULL,
    ADD COLUMN ban_reason VARCHAR(500) NOT NULL DEFAULT '';

CREATE TABLE violations
(
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    exchange_id   BIGINT UNSIGNED NOT NULL,
    user_id       BIGINT UNSIGNED NOT NULL,
    assignment_id BIGINT UNSIGNED NULL,
    kind          VARCHAR(32)     NOT NULL,
    comment       VARCHAR(500)    NOT NULL DEFAULT '',
    created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dismissed_at  DATETIME        NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_violations_auto (assignment_id, user_id, kind),
    KEY idx_violations_exchange (exchange_id),
    KEY idx_violations_user (user_id),
    CONSTRAINT fk_violations_exchange FOREIGN KEY (exchange_id) REFERENCES exchanges (id),
    CONSTRAINT fk_violations_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT fk_violations_assignment FOREIGN KEY (assignment_id) REFERENCES assignments (id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;
