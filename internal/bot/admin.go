package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

// ActionError is a refusal the organizer should see as is (wrong state, bad input).
type ActionError struct{ Msg string }

func (e *ActionError) Error() string { return e.Msg }

func refuse(format string, args ...any) error {
	return &ActionError{Msg: fmt.Sprintf(format, args...)}
}

var statusTitles = map[domain.ExchangeStatus]string{
	domain.ExchangeDraft:       "черновик",
	domain.ExchangeOpen:        "регистрация открыта",
	domain.ExchangeClosed:      "регистрация закрыта",
	domain.ExchangeDistributed: "дети распределены",
	domain.ExchangeCompleted:   "обмен завершён",
}

func StatusTitle(s domain.ExchangeStatus) string {
	if t, ok := statusTitles[s]; ok {
		return t
	}
	return string(s)
}

var transitions = map[string]struct {
	to   domain.ExchangeStatus
	from []domain.ExchangeStatus
}{
	"open":   {domain.ExchangeOpen, []domain.ExchangeStatus{domain.ExchangeDraft, domain.ExchangeClosed}},
	"close":  {domain.ExchangeClosed, []domain.ExchangeStatus{domain.ExchangeOpen}},
	"finish": {domain.ExchangeCompleted, []domain.ExchangeStatus{domain.ExchangeDistributed}},
}

// ChangeExchangeStatus applies one of the actions open, close, finish to the current exchange.
func (b *Bot) ChangeExchangeStatus(ctx context.Context, action string) (domain.ExchangeStatus, error) {
	t, ok := transitions[action]
	if !ok {
		return "", refuse("Неизвестное действие %q.", action)
	}
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return "", err
	}
	ok, err = b.store.TransitionExchange(ctx, ex.ID, t.to, t.from...)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", refuse("Нельзя: сейчас «%s».", StatusTitle(ex.Status))
	}
	return t.to, nil
}

func (b *Bot) NewExchange(ctx context.Context) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeCompleted {
		return refuse("Новый обмен можно начать только после завершения текущего.")
	}
	return b.store.CreateExchange(ctx)
}

type DistributionPreview struct {
	Families int    `json:"families"`
	Children int    `json:"children"`
	Possible bool   `json:"possible"`
	Reason   string `json:"reason,omitempty"`
}

func (b *Bot) PreviewDistribution(ctx context.Context) (DistributionPreview, error) {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return DistributionPreview{}, err
	}
	refs, err := b.store.ChildRefs(ctx, ex.ID)
	if err != nil {
		return DistributionPreview{}, err
	}
	families := map[int64]bool{}
	for _, r := range refs {
		families[r.ParentID] = true
	}
	p := DistributionPreview{Families: len(families), Children: len(refs), Possible: true}
	switch {
	case ex.Status != domain.ExchangeClosed:
		p.Possible, p.Reason = false, fmt.Sprintf("Распределять можно только после закрытия регистрации, сейчас «%s».", StatusTitle(ex.Status))
	default:
		if _, err := domain.Distribute(refs, rand.New(rand.NewSource(1))); err != nil {
			p.Possible, p.Reason = false, "Нужно минимум две семьи, и ни одна семья не может привести больше половины всех детей."
		}
	}
	return p, nil
}

// Distribute assigns givers once and sends every participant their cards.
func (b *Bot) Distribute(ctx context.Context) (int, error) {
	preview, err := b.PreviewDistribution(ctx)
	if err != nil {
		return 0, err
	}
	if !preview.Possible {
		return 0, refuse("%s", preview.Reason)
	}
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return 0, err
	}
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	n, err := b.store.Distribute(ctx, ex.ID, func(refs []domain.ChildRef) ([]domain.Pair, error) {
		return domain.Distribute(refs, rnd)
	})
	if err != nil {
		return 0, err
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return n, err
	}
	b.notifyDistribution(list)
	b.notifyAdmins(fmt.Sprintf("✅ Распределение готово: %d подарков. Участникам отправлены карточки.", n))
	return n, nil
}

// RemindNow sends the most relevant reminder for every unfinished gift, ignoring the daily schedule.
func (b *Bot) RemindNow(ctx context.Context) (int, error) {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return 0, err
	}
	if ex.Status != domain.ExchangeDistributed {
		return 0, refuse("Напоминания работают только после распределения.")
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return 0, err
	}
	sent := 0
	now := b.now()
	for _, a := range list {
		st := a.PickupState()
		switch {
		case a.Status == domain.AssignmentAssigned:
			b.sendReminder(domain.RemindGiverOrder, a)
		case a.Status == domain.AssignmentArrived && st.CodeRequestPending():
			b.sendReminder(domain.RemindGiverCodePending, a)
		case a.Status == domain.AssignmentArrived && !st.CodeValid(now):
			b.sendReminder(domain.RemindRecipientPickup, a)
		default:
			continue
		}
		sent++
	}
	return sent, nil
}

// Reassign hands a child's gift to another participant and notifies both givers.
func (b *Bot) Reassign(ctx context.Context, childID, giverID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if _, err := b.store.UserByID(ctx, giverID); errors.Is(err, store.ErrNotFound) {
		return refuse("Нет участника с id %d.", giverID)
	} else if err != nil {
		return err
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	var old *store.Assignment
	for i := range list {
		if list[i].ChildID == childID {
			old = &list[i]
			break
		}
	}
	if old == nil {
		return refuse("Ребёнок %d не найден в распределении.", childID)
	}
	if old.Giver.ID == giverID {
		return refuse("Этот участник уже дарит подарок этому ребёнку.")
	}
	ok, err := b.store.Reassign(ctx, ex.ID, childID, giverID)
	if err != nil {
		return err
	}
	if !ok {
		return refuse("Нельзя: подарок уже получен или новый даритель — родитель этого ребёнка.")
	}
	updated, err := b.store.Assignment(ctx, old.ID)
	if err != nil {
		return err
	}
	b.send(old.Giver.TgID, fmt.Sprintf("Организатор передал подарок для %s другому участнику. Спасибо!", esc(old.ChildFirstName)), nil)
	b.sendGiverCard(updated, "🎁 Организатор добавил тебе ещё одного ребёнка:")
	return nil
}

func (b *Bot) Broadcast(ctx context.Context, text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, refuse("Пустое сообщение.")
	}
	ids, err := b.store.AllUserTgIDs(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		b.send(id, "📣 "+esc(text), nil)
	}
	return len(ids), nil
}

func (b *Bot) InviteLink() string {
	if b.cfg.InviteCode == "" {
		return "https://t.me/" + b.api.Self.UserName
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", b.api.Self.UserName, b.cfg.InviteCode)
}

// ---------- chat commands ----------

const adminHelp = `<b>Команды организатора</b>

Удобнее управлять обменом через панель: кнопка «Панель» слева от поля ввода или /admin.

/invite — ссылка-приглашение
/open — открыть регистрацию
/close — закрыть регистрацию
/families — участники и телефоны
/distribute — распределить детей (с подтверждением, один раз)
/status — прогресс по подаркам
/remind — разослать напоминания сейчас
/reassign &lt;child_id&gt; &lt;user_id&gt; — передать ребёнка другому дарителю
/broadcast &lt;текст&gt; — сообщение всем участникам
/finish — завершить обмен
/new_exchange — начать новый обмен после завершения`

func (b *Bot) handleAdminCommand(ctx context.Context, msg *tgbotapi.Message, cmd, args string) (bool, error) {
	tgID := msg.From.ID
	var err error
	switch cmd {
	case "admin":
		b.sendAdminPanelButton(tgID, adminHelp)
	case "invite":
		b.send(tgID, "Ссылка-приглашение:\n"+esc(b.InviteLink()), nil)
	case "open", "close", "finish":
		var to domain.ExchangeStatus
		if to, err = b.ChangeExchangeStatus(ctx, cmd); err == nil {
			b.send(tgID, "Готово: "+StatusTitle(to)+".", nil)
		}
	case "new_exchange":
		if err = b.NewExchange(ctx); err == nil {
			b.send(tgID, "Создан новый обмен. Открой регистрацию командой /open.", nil)
		}
	case "families":
		err = b.adminFamilies(ctx, tgID)
	case "distribute":
		err = b.adminDistributePreview(ctx, tgID)
	case "status":
		err = b.adminStatus(ctx, tgID)
	case "remind":
		var n int
		if n, err = b.RemindNow(ctx); err == nil {
			b.send(tgID, fmt.Sprintf("Отправлено напоминаний: %d.", n), nil)
		}
	case "reassign":
		err = b.adminReassign(ctx, tgID, args)
	case "broadcast":
		var n int
		if n, err = b.Broadcast(ctx, args); err == nil {
			b.send(tgID, fmt.Sprintf("Отправлено %d участникам.", n), nil)
		}
	default:
		return false, nil
	}
	return true, b.replyRefusal(tgID, err)
}

// replyRefusal shows ActionError text to the admin and passes other errors up.
func (b *Bot) replyRefusal(tgID int64, err error) error {
	var ae *ActionError
	if errors.As(err, &ae) {
		b.send(tgID, esc(ae.Msg), nil)
		return nil
	}
	return err
}

func (b *Bot) confirmDistribution(ctx context.Context, tgID int64) error {
	_, err := b.Distribute(ctx)
	return b.replyRefusal(tgID, err)
}

func (b *Bot) adminFamilies(ctx context.Context, tgID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	list, err := b.store.Participants(ctx, ex.ID)
	if err != nil {
		return err
	}
	var sb strings.Builder
	total := 0
	for _, p := range list {
		total += p.Children
		fmt.Fprintf(&sb, "[user %d] %s %s%s, %s\nПВЗ: %s\nДетей: %d\n\n",
			p.ID, esc(p.LastName), esc(p.FirstName), usernameSuffix(p.User), esc(p.Phone), esc(p.PVZAddress), p.Children)
	}
	header := fmt.Sprintf("<b>Обмен #%d: %s</b>\nСемей: %d, детей: %d\n\n", ex.ID, StatusTitle(ex.Status), len(list), total)
	b.send(tgID, header+sb.String(), nil)
	return nil
}

func (b *Bot) adminDistributePreview(ctx context.Context, tgID int64) error {
	p, err := b.PreviewDistribution(ctx)
	if err != nil {
		return err
	}
	if !p.Possible {
		b.send(tgID, fmt.Sprintf("Семей: %d, детей: %d.\n❗ %s", p.Families, p.Children, esc(p.Reason)), nil)
		return nil
	}
	b.send(tgID, fmt.Sprintf(
		"Семей: %d, детей: %d.\nКаждая семья дарит столько подарков, сколько детей привела, и не дарит своим.\n\nПосле подтверждения распределение не изменить, всем участникам придут карточки.",
		p.Families, p.Children),
		inlineButtons(button("✅ Распределить", "dist", 0)))
	return nil
}

func (b *Bot) notifyDistribution(list []store.Assignment) {
	byGiver := map[int64][]store.Assignment{}
	parents := map[int64]bool{}
	var order []int64
	for _, a := range list {
		if _, seen := byGiver[a.Giver.TgID]; !seen {
			order = append(order, a.Giver.TgID)
		}
		byGiver[a.Giver.TgID] = append(byGiver[a.Giver.TgID], a)
		parents[a.Parent.TgID] = true
	}
	for _, giverTg := range order {
		items := byGiver[giverTg]
		b.send(giverTg, fmt.Sprintf(
			"🎄 Распределение готово! Ты даришь подарки %d %s. Ниже карточки: закажи подарок %s на указанный ПВЗ %s и нажми «Заказал(а)».",
			len(items), childrenWord(len(items)), b.budgetText(), b.orderDeadlineText()), mainMenu())
		for _, a := range items {
			b.sendGiverCard(a, "")
		}
	}
	for tg := range parents {
		b.send(tg, "Твои дети тоже получат подарки 🎁 Когда посылка придёт в ПВЗ, я напишу, а статус можно смотреть в «"+btnReceive+"».", nil)
	}
}

func childrenWord(n int) string {
	mod10, mod100 := n%10, n%100
	if mod10 == 1 && mod100 != 11 {
		return "ребёнку"
	}
	return "детям"
}

func (b *Bot) adminStatus(ctx context.Context, tgID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		b.send(tgID, fmt.Sprintf("Обмен #%d: %s. Распределения ещё нет.", ex.ID, StatusTitle(ex.Status)), nil)
		return nil
	}

	counts := map[domain.AssignmentStatus]int{}
	var sb strings.Builder
	now := b.now()
	for _, a := range list {
		counts[a.Status]++
		if a.Status == domain.AssignmentReceived {
			continue
		}
		extra := ""
		if a.Status == domain.AssignmentArrived && a.ArrivedAt != nil {
			extra = fmt.Sprintf(", в ПВЗ %d дн.", int(now.Sub(*a.ArrivedAt).Hours()/24))
		}
		fmt.Fprintf(&sb, "[child %d] %s %s → даритель [user %d] %s %s%s: %s%s\n",
			a.ChildID, esc(a.ChildFirstName), esc(a.ChildLastName),
			a.Giver.ID, esc(a.Giver.FirstName), esc(a.Giver.LastName), usernameSuffix(a.Giver), b.statusText(a), extra)
	}
	header := fmt.Sprintf("<b>Обмен #%d: %s</b>\nВсего: %d. Не заказано: %d, заказано: %d, в ПВЗ: %d, получено: %d.\n\n",
		ex.ID, StatusTitle(ex.Status), len(list),
		counts[domain.AssignmentAssigned], counts[domain.AssignmentOrdered], counts[domain.AssignmentArrived], counts[domain.AssignmentReceived])
	if sb.Len() == 0 {
		sb.WriteString("Все подарки получены 🎉")
	}
	b.send(tgID, header+sb.String(), nil)
	return nil
}

func usernameSuffix(u store.User) string {
	if u.TgUsername == "" {
		return ""
	}
	return " @" + esc(u.TgUsername)
}

func (b *Bot) adminReassign(ctx context.Context, tgID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 {
		return refuse("Формат: /reassign <child_id> <user_id>. ID есть в /status и /families.")
	}
	childID, err1 := strconv.ParseInt(fields[0], 10, 64)
	giverID, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil {
		return refuse("child_id и user_id должны быть числами.")
	}
	if err := b.Reassign(ctx, childID, giverID); err != nil {
		return err
	}
	b.send(tgID, "Готово, новый даритель получил карточку.", nil)
	return nil
}

// ---------- admin panel entry points ----------

// tgbotapi v5.5.1 predates Web Apps, so these two calls go through raw requests.

// SetupAdminMenuButtons puts the panel button next to the input field in each admin's chat.
func (b *Bot) SetupAdminMenuButtons() {
	if b.cfg.WebAppURL == "" {
		return
	}
	menu, _ := json.Marshal(map[string]any{
		"type":    "web_app",
		"text":    "Панель",
		"web_app": map[string]string{"url": b.cfg.WebAppURL},
	})
	for _, id := range b.cfg.AdminIDs {
		params := tgbotapi.Params{"chat_id": strconv.FormatInt(id, 10), "menu_button": string(menu)}
		if _, err := b.api.MakeRequest("setChatMenuButton", params); err != nil {
			// Fails until the admin opens the bot at least once; /admin retries.
			continue
		}
	}
}

func (b *Bot) sendAdminPanelButton(chatID int64, text string) {
	if b.cfg.WebAppURL == "" {
		b.send(chatID, text, nil)
		return
	}
	b.SetupAdminMenuButtons()
	markup, _ := json.Marshal(map[string]any{
		"inline_keyboard": [][]map[string]any{{{
			"text":    "🛠 Открыть панель",
			"web_app": map[string]string{"url": b.cfg.WebAppURL},
		}}},
	})
	params := tgbotapi.Params{
		"chat_id":      strconv.FormatInt(chatID, 10),
		"text":         text,
		"parse_mode":   tgbotapi.ModeHTML,
		"reply_markup": string(markup),
	}
	if _, err := b.api.MakeRequest("sendMessage", params); err != nil {
		b.send(chatID, text, nil)
	}
}
