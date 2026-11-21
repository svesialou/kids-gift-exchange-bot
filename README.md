# kids-gift-exchange-bot

Telegram bot for a gift exchange between families with kids. One adult per family registers with a Wildberries pickup point (ПВЗ), adds their children, and after registration closes the bot assigns every child a giver from another family. Gifts are ordered on Wildberries to the recipient's ПВЗ, and the bot relays the one-day pickup code from giver to recipient.

## Flow

1. Organizer runs `/open` and shares the `/invite` link.
2. Parents register: first name, last name, phone, ПВЗ address. Then they add children: first name, last name, age, gender, optional wishes.
3. Organizer runs `/close`, checks `/families`, then `/distribute` and confirms. Distribution happens once and cannot be repeated.
4. Each family gives as many gifts as it brought children and never gets its own children. The rule fails only if one family brings more than half of all children.
5. Giver: «Заказал(а)» with expected date, then «Пришло в ПВЗ», then sends the pickup code (text or QR screenshot) when the recipient asks.
6. Recipient: «Иду забирать» asks the giver for today's code, «Код не подошёл» asks for a new one, «Забрал(а)» closes the gift.
7. Organizer runs `/finish` when everything is received.

A Wildberries pickup code is valid only for the day it was issued, so the bot treats every code as expiring at local midnight.

## Reminders

The scheduler checks assignments every `SCHEDULER_INTERVAL`. Every kind of reminder is sent at most once per local day, only between `MORNING_HOUR` and `QUIET_HOUR`.

| Reminder | To | When |
|---|---|---|
| Order the gift | giver | not ordered yet |
| Has the parcel arrived? | giver | expected date passed |
| Recipient is waiting for the code | giver | code requested more than `CODE_PENDING_REMINDER_AFTER` ago |
| Yesterday's code was not used | giver | morning after an unused code expired |
| Parcel waits at the ПВЗ | recipient | arrived, no valid code and no pending request |
| Code expires today | recipient | valid code, after `EVENING_HOUR` |
| Parcel is stuck | admins | arrived more than `STUCK_AFTER` ago |

## Admin commands

Commands are hidden from the menu and work only for ids from `ADMIN_IDS`. `/admin` prints the list: `/invite`, `/open`, `/close`, `/families`, `/distribute`, `/status`, `/remind`, `/reassign <child_id> <user_id>`, `/broadcast <text>`, `/finish`, `/new_exchange`.

## Configuration

All settings come from environment variables, see `.env.example`. Required: `TELEGRAM_BOT_TOKEN`, `DB_PASSWORD`. Budget (`GIFT_BUDGET_MAX`, `GIFT_CURRENCY`), deadlines and reminder hours can be changed with a restart. Migrations run automatically on start.

## Development

```bash
cp .env.example .env   # fill TELEGRAM_BOT_TOKEN and ADMIN_IDS with a test bot
make up                # MySQL + bot in Docker
make test              # unit tests
make test-db           # unit + MySQL integration tests against the compose database
```

## Deployment

CI publishes `ghcr.io/svesialou/kids-gift-exchange-bot:latest` on every push to `main`. The production runtime (env file, MySQL database and user, container) is managed by the `kids_gift_exchange` stack in the `management` repository.
