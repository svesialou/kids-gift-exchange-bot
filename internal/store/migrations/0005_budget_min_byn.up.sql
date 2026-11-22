ALTER TABLE exchanges
    CHANGE COLUMN gift_budget_max gift_budget_min INT UNSIGNED NULL,
    DROP COLUMN gift_currency;
