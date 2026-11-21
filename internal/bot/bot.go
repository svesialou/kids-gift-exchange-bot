package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

const (
	btnProfile  = "👤 Мой профиль"
	btnChildren = "👶 Мои дети"
	btnGive     = "🎁 Кому я дарю"
	btnReceive  = "📦 Подарки моим детям"
	btnRules    = "ℹ️ Как это работает"
	btnCancel   = "Отмена"
	btnSkip     = "Пропустить"
	btnDontKnow = "Не знаю"
	btnBoy      = "Мальчик"
	btnGirl     = "Девочка"
)

type Bot struct {
	api    *tgbotapi.BotAPI
	store  *store.Store
	cfg    config.Config
	policy domain.ReminderPolicy
	now    func() time.Time
}

func New(api *tgbotapi.BotAPI, st *store.Store, cfg config.Config) *Bot {
	return &Bot{
		api:   api,
		store: st,
		cfg:   cfg,
		policy: domain.ReminderPolicy{
			Location:         cfg.Location,
			MorningHour:      cfg.MorningHour,
			EveningHour:      cfg.EveningHour,
			QuietHour:        cfg.QuietHour,
			CodePendingAfter: cfg.CodePendingAfter,
			StuckAfter:       cfg.StuckAfter,
		},
		now: time.Now,
	}
}

func (b *Bot) Run(ctx context.Context) error {
	_, err := b.api.Request(tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Начать / главное меню"},
		tgbotapi.BotCommand{Command: "cancel", Description: "Отменить текущий шаг"},
	))
	if err != nil {
		slog.Warn("set commands failed", "error", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 50
	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return nil
		case upd, ok := <-updates:
			if !ok {
				return errors.New("updates channel closed")
			}
			b.handleUpdate(ctx, upd)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, upd tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in update handler", "panic", r)
		}
	}()

	var err error
	var chatID int64
	switch {
	case upd.CallbackQuery != nil:
		chatID = upd.CallbackQuery.From.ID
		err = b.handleCallback(ctx, upd.CallbackQuery)
	case upd.Message != nil && upd.Message.Chat.IsPrivate():
		chatID = upd.Message.Chat.ID
		err = b.handleMessage(ctx, upd.Message)
	default:
		return
	}
	if err != nil {
		slog.Error("update failed", "chat_id", chatID, "error", err)
		b.send(chatID, "Что-то пошло не так. Попробуй ещё раз или напиши организатору.", nil)
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) error {
	tgID := msg.From.ID

	if msg.IsCommand() {
		cmd := msg.Command()
		args := strings.TrimSpace(msg.CommandArguments())
		switch cmd {
		case "start":
			return b.onStart(ctx, msg, args)
		case "cancel":
			return b.cancel(ctx, tgID)
		}
		if b.cfg.IsAdmin(tgID) {
			if handled, err := b.handleAdminCommand(ctx, msg, cmd, args); handled || err != nil {
				return err
			}
		}
		return b.showMenu(ctx, tgID, "Не знаю такой команды.")
	}

	text := strings.TrimSpace(msg.Text)
	if text == btnCancel {
		return b.cancel(ctx, tgID)
	}
	if isMenuButton(text) {
		if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
			return err
		}
		return b.handleMenuButton(ctx, tgID, text)
	}

	sess, err := b.store.Session(ctx, tgID)
	if err != nil {
		return err
	}
	if sess.State != "" {
		return b.handleState(ctx, msg, sess)
	}
	return b.showMenu(ctx, tgID, "Выбери действие в меню.")
}

func (b *Bot) handleState(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	switch {
	case strings.HasPrefix(sess.State, "reg_"):
		return b.handleRegistrationStep(ctx, msg, sess)
	case strings.HasPrefix(sess.State, "child_"):
		return b.handleChildStep(ctx, msg, sess)
	case sess.State == stateOrderDate:
		return b.handleOrderDate(ctx, msg, sess)
	case sess.State == stateCode:
		return b.handleCodeInput(ctx, msg, sess)
	}
	return b.cancel(ctx, msg.From.ID)
}

func isMenuButton(text string) bool {
	switch text {
	case btnProfile, btnChildren, btnGive, btnReceive, btnRules:
		return true
	}
	return false
}

func (b *Bot) handleMenuButton(ctx context.Context, tgID int64, text string) error {
	user, err := b.store.UserByTG(ctx, tgID)
	if errors.Is(err, store.ErrNotFound) {
		b.send(tgID, "Сначала нужно зарегистрироваться: нажми /start.", tgbotapi.NewRemoveKeyboard(true))
		return nil
	}
	if err != nil {
		return err
	}
	switch text {
	case btnProfile:
		return b.showProfile(ctx, user)
	case btnChildren:
		return b.showChildren(ctx, user)
	case btnGive:
		return b.showGiverCards(ctx, user)
	case btnReceive:
		return b.showRecipientCards(ctx, user)
	case btnRules:
		b.send(tgID, b.rulesText(), nil)
	}
	return nil
}

func (b *Bot) handleCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_, _ = b.api.Request(tgbotapi.NewCallback(cq.ID, ""))

	action, rawID, _ := strings.Cut(cq.Data, ":")
	id, _ := strconv.ParseInt(rawID, 10, 64)
	tgID := cq.From.ID

	if action == "dist" {
		if !b.cfg.IsAdmin(tgID) {
			return nil
		}
		return b.confirmDistribution(ctx, tgID)
	}

	user, err := b.store.UserByTG(ctx, tgID)
	if errors.Is(err, store.ErrNotFound) {
		b.send(tgID, "Сначала нужно зарегистрироваться: нажми /start.", nil)
		return nil
	}
	if err != nil {
		return err
	}

	switch action {
	case "prof":
		return b.startRegistration(ctx, tgID, true)
	case "addc":
		return b.startChild(ctx, user)
	case "delc":
		return b.deleteChild(ctx, user, id)
	case "ord":
		return b.startOrdered(ctx, user, id)
	case "arr":
		return b.markArrived(ctx, user, id)
	case "code":
		return b.startCode(ctx, user, id)
	case "pick":
		return b.requestCode(ctx, user, id, false)
	case "bad":
		return b.requestCode(ctx, user, id, true)
	case "got":
		return b.markReceived(ctx, user, id)
	}
	return nil
}

func (b *Bot) cancel(ctx context.Context, tgID int64) error {
	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}
	return b.showMenu(ctx, tgID, "Отменено.")
}

func (b *Bot) showMenu(ctx context.Context, tgID int64, text string) error {
	_, err := b.store.UserByTG(ctx, tgID)
	if errors.Is(err, store.ErrNotFound) {
		b.send(tgID, text+"\nЧтобы участвовать, нажми /start.", tgbotapi.NewRemoveKeyboard(true))
		return nil
	}
	if err != nil {
		return err
	}
	b.send(tgID, text, mainMenu())
	return nil
}

func mainMenu() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnGive), tgbotapi.NewKeyboardButton(btnReceive)),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnChildren), tgbotapi.NewKeyboardButton(btnProfile)),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnRules)),
	)
	kb.ResizeKeyboard = true
	return kb
}

func stepKeyboard(buttons ...string) tgbotapi.ReplyKeyboardMarkup {
	var row []tgbotapi.KeyboardButton
	for _, t := range buttons {
		row = append(row, tgbotapi.NewKeyboardButton(t))
	}
	kb := tgbotapi.NewReplyKeyboard(row, tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnCancel)))
	kb.ResizeKeyboard = true
	return kb
}

func cancelKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnCancel)))
	kb.ResizeKeyboard = true
	return kb
}

func inlineButtons(buttons ...tgbotapi.InlineKeyboardButton) any {
	if len(buttons) == 0 {
		return nil
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, btn := range buttons {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func button(text, action string, id int64) tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData(text, fmt.Sprintf("%s:%d", action, id))
}

// send delivers an HTML message and logs failures: a blocked bot or a deleted chat must not break the flow for others.
func (b *Bot) send(chatID int64, text string, markup any) {
	for _, part := range splitMessage(text, 4000) {
		msg := tgbotapi.NewMessage(chatID, part)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true
		if markup != nil {
			msg.ReplyMarkup = markup
		}
		if _, err := b.api.Send(msg); err != nil {
			slog.Warn("send message failed", "chat_id", chatID, "error", err)
			return
		}
		// Stay under Telegram's ~30 messages/second limit during mass sends such as distribution.
		time.Sleep(40 * time.Millisecond)
	}
}

func (b *Bot) sendPhoto(chatID int64, fileID, caption string, markup any) {
	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(fileID))
	msg.Caption = caption
	msg.ParseMode = tgbotapi.ModeHTML
	if markup != nil {
		msg.ReplyMarkup = markup
	}
	if _, err := b.api.Send(msg); err != nil {
		slog.Warn("send photo failed", "chat_id", chatID, "error", err)
	}
}

func (b *Bot) notifyAdmins(text string) {
	for _, id := range b.cfg.AdminIDs {
		b.send(id, text, nil)
	}
}

func splitMessage(text string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	var parts []string
	var cur strings.Builder
	curLen := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		n := utf8.RuneCountInString(line)
		if curLen+n > limit && curLen > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
			curLen = 0
		}
		cur.WriteString(line)
		curLen += n
	}
	if curLen > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func esc(s string) string { return html.EscapeString(s) }

func ageText(age int) string {
	mod10, mod100 := age%10, age%100
	switch {
	case mod10 == 1 && mod100 != 11:
		return fmt.Sprintf("%d год", age)
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return fmt.Sprintf("%d года", age)
	}
	return fmt.Sprintf("%d лет", age)
}

func genderText(g string) string {
	if g == "girl" {
		return "девочка"
	}
	return "мальчик"
}

func (b *Bot) budgetText() string {
	return fmt.Sprintf("до %d %s", b.cfg.GiftBudgetMax, b.cfg.GiftCurrency)
}

func (b *Bot) orderDeadlineText() string {
	if b.cfg.OrderDeadline == "" {
		return "как можно скорее"
	}
	return "до " + b.cfg.OrderDeadline
}

func (b *Bot) rulesText() string {
	return fmt.Sprintf(`<b>Как это работает</b>

1. Регистрируешься: имя, фамилия, телефон и адрес ПВЗ Wildberries, куда удобно забирать посылки.
2. Добавляешь детей, которые участвуют в обмене.
3. Когда регистрация закроется, бот распределит детей: твоя семья дарит столько подарков, сколько детей привела, и никогда не дарит своим.
4. В «%s» увидишь, кому даришь. Заказываешь подарок %s на ПВЗ получателя %s.
5. Когда посылка придёт, нажимаешь «Пришло в ПВЗ». Получатель попросит код, и ты присылаешь его из приложения WB текстом или скриншотом QR.
6. Код живёт только один день. Если посылку не забрали, получатель запросит новый код, а бот напомнит обоим.
7. Цель: все дети получают подарки до %s.`,
		btnGive, b.budgetText(), b.orderDeadlineText(), esc(b.cfg.DeliveryTarget))
}

func (b *Bot) localDate(t time.Time) string {
	return t.In(b.cfg.Location).Format("02.01")
}
