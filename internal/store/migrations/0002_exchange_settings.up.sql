ALTER TABLE exchanges
    ADD COLUMN gift_budget_max   INT UNSIGNED NULL,
    ADD COLUMN gift_currency     VARCHAR(10)  NULL,
    ADD COLUMN order_deadline    DATE         NULL,
    ADD COLUMN delivery_deadline DATE         NULL;
