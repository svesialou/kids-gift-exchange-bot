ALTER TABLE exchanges
    CHANGE COLUMN gift_budget_min gift_budget_max INT UNSIGNED NULL,
    ADD COLUMN gift_currency VARCHAR(10) NULL;
