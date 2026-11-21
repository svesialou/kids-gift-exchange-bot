CREATE TABLE exchanges
(
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    status         VARCHAR(32)     NOT NULL,
    created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    distributed_at DATETIME        NULL,
    completed_at   DATETIME        NULL,
    PRIMARY KEY (id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE users
(
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tg_id       BIGINT          NOT NULL,
    tg_username VARCHAR(64)     NOT NULL DEFAULT '',
    first_name  VARCHAR(100)    NOT NULL,
    last_name   VARCHAR(100)    NOT NULL,
    phone       VARCHAR(32)     NOT NULL,
    pvz_address VARCHAR(500)    NOT NULL,
    created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY ux_users_tg_id (tg_id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE children
(
    id             BIGINT UNSIGNED  NOT NULL AUTO_INCREMENT,
    exchange_id    BIGINT UNSIGNED  NOT NULL,
    parent_user_id BIGINT UNSIGNED  NOT NULL,
    first_name     VARCHAR(100)     NOT NULL,
    last_name      VARCHAR(100)     NOT NULL,
    age            TINYINT UNSIGNED NOT NULL,
    gender         VARCHAR(8)       NOT NULL,
    wishes         VARCHAR(1000)    NOT NULL DEFAULT '',
    created_at     DATETIME         NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_children_exchange_parent (exchange_id, parent_user_id),
    CONSTRAINT fk_children_exchange FOREIGN KEY (exchange_id) REFERENCES exchanges (id),
    CONSTRAINT fk_children_parent FOREIGN KEY (parent_user_id) REFERENCES users (id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE assignments
(
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    exchange_id       BIGINT UNSIGNED NOT NULL,
    child_id          BIGINT UNSIGNED NOT NULL,
    giver_user_id     BIGINT UNSIGNED NOT NULL,
    status            VARCHAR(16)     NOT NULL,
    expected_date     DATE            NULL,
    ordered_at        DATETIME        NULL,
    arrived_at        DATETIME        NULL,
    received_at       DATETIME        NULL,
    code_text         VARCHAR(255)    NOT NULL DEFAULT '',
    code_file_id      VARCHAR(255)    NOT NULL DEFAULT '',
    code_sent_at      DATETIME        NULL,
    code_valid_until  DATETIME        NULL,
    code_requested_at DATETIME        NULL,
    created_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY ux_assignments_child (child_id),
    KEY idx_assignments_exchange_status (exchange_id, status),
    KEY idx_assignments_giver (giver_user_id),
    CONSTRAINT fk_assignments_exchange FOREIGN KEY (exchange_id) REFERENCES exchanges (id),
    CONSTRAINT fk_assignments_child FOREIGN KEY (child_id) REFERENCES children (id),
    CONSTRAINT fk_assignments_giver FOREIGN KEY (giver_user_id) REFERENCES users (id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE reminders_sent
(
    assignment_id BIGINT UNSIGNED NOT NULL,
    kind          VARCHAR(32)     NOT NULL,
    sent_on       DATE            NOT NULL,
    created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (assignment_id, kind, sent_on),
    CONSTRAINT fk_reminders_assignment FOREIGN KEY (assignment_id) REFERENCES assignments (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;

CREATE TABLE sessions
(
    tg_id      BIGINT       NOT NULL,
    state      VARCHAR(64)  NOT NULL,
    data       TEXT         NOT NULL,
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (tg_id)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci;
