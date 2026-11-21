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

## Exchanges and schedule

Organizers create every exchange by hand in the panel and fill all of its fields: title, gift budget and currency, registration start and end (date and time), the date gifts must be sent by, and the end date. Nothing is created automatically and there are no default values.

- Several exchanges can be planned; only one runs at a time (registration open, closed, or gifts distributed).
- Registration opens only when an organizer presses the button, and only between the exchange's registration start and end, while no other exchange is running.
- At the registration end the bot closes it and distributes the children right away.
- After the end of the send-by day givers who have not marked «Заказал(а)» get an `order_overdue` violation.
- After the end date the exchange completes, reminders stop, and gifts that never reached the pickup point get a `delivery_overdue` violation.

Any exchange can be deleted together with its children, distribution and violations; for a running one organizers can notify its participants. Adult profiles carry over between exchanges; children are registered per exchange. While no exchange runs, the bot tells newcomers that registration is closed.

## Admin panel

Organizers manage everything from a Telegram Mini App: the «Панель» menu button in their chat with the bot, or `/admin`.

- «Обмены»: the running exchange, planned ones and completed ones, plus «Создать обмен». Each exchange opens with its overview (stage, actions, invite, schedule and conditions, delete), «Семьи», «Подарки» with filters and reassigning, «Нарушения» with automatic and manual entries that can be dismissed.
- «Люди»: the user base across all exchanges with participation counts, gifts given and active violations. A user with violations or a ban gets a red flag. The user card shows the whole history, sends a personal message from the bot, and bans or unbans: a banned user cannot add children and is left out of distributions. In selection mode organizers tick several people to message, ban or unban them at once.

The bot serves the panel and its API on `HTTP_ADDR` when `WEBAPP_URL` is set. Every API call carries Telegram `initData`; the server checks its HMAC signature with the bot token, rejects data older than 24 hours and lets through only ids from `ADMIN_IDS`. Panel actions go through the same code as the chat commands, so participants get the same Telegram notifications.

## Admin commands

Commands are hidden from the menu and work only for ids from `ADMIN_IDS`. `/admin` prints the list: `/invite`, `/open`, `/close`, `/families`, `/distribute`, `/status`, `/remind`, `/reassign <child_id> <user_id>`, `/broadcast <text>`, `/finish`, `/new_exchange`.

## Configuration

All settings come from environment variables, see `.env.example`. Required: `TELEGRAM_BOT_TOKEN`, `DB_PASSWORD`. Reminder hours can be changed with a restart.

Title, budget, currency and dates are set per exchange in the panel. Migrations run automatically on start.

## Development

```bash
cp .env.example .env   # fill TELEGRAM_BOT_TOKEN and ADMIN_IDS with a test bot
make up                # MySQL + bot in Docker
make test              # unit tests
make test-db           # unit + MySQL integration tests against the compose database
```

## Deployment

Every push to `main` runs tests, publishes `ghcr.io/svesialou/kids-gift-exchange-bot:latest` and deploys it. The deploy job connects over SSH with a key that the server restricts to `deploy.sh`: it pulls the image, recreates the container and waits until the bot logs `migrations applied` and `bot started`. Migrations run on bot start, so a failing migration fails the deploy job and the log tail shows up in the job output.

GitHub environment `production` needs the secrets `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY` and `DEPLOY_KNOWN_HOSTS`. The production runtime (env file, MySQL database and user, container) is managed by the `kids_gift_exchange` stack in the `management` repository.
