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
	domain.ExchangeNone:        "обмена нет",
	domain.ExchangeDraft:       "черновик",
	domain.ExchangeOpen:        "регистрация открыта",
	domain.ExchangeClosed:      "регистрация закрыта",
	domain.ExchangeDistributed: "участники распределены",
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

// ChangeExchangeStatus applies open, close or finish to an exchange. Registration opens only by hand,
// only inside the exchange's registration window and only while no other exchange is running.
func (b *Bot) ChangeExchangeStatus(ctx context.Context, id int64, action string) (domain.ExchangeStatus, error) {
	t, ok := transitions[action]
	if !ok {
		return "", refuse("Неизвестное действие %q.", action)
	}
	ex, err := b.exchangeByID(ctx, id)
	if err != nil {
		return "", err
	}
	if action == "open" {
		if err := b.checkCanOpen(ctx, ex); err != nil {
			return "", err
		}
	}
	ok, err = b.store.TransitionExchange(ctx, ex.ID, t.to, t.from...)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", refuse("Нельзя: сейчас «%s».", StatusTitle(ex.Status))
	}
	if err := b.LoadSettings(ctx); err != nil {
		return t.to, err
	}
	if t.to == domain.ExchangeCompleted {
		if err := b.onExchangeCompleted(ctx, ex); err != nil {
			return t.to, err
		}
	}
	return t.to, nil
}

func (b *Bot) checkCanOpen(ctx context.Context, ex store.Exchange) error {
	now := b.now()
	if ex.RegistrationOpensAt == nil || ex.RegistrationClosesAt == nil {
		return refuse("Сначала укажите даты регистрации в условиях обмена.")
	}
	if now.Before(*ex.RegistrationOpensAt) || !now.Before(*ex.RegistrationClosesAt) {
		return refuse("Открыть регистрацию можно только с %s по %s. Чтобы открыть сейчас, поменяйте даты в условиях.",
			b.localDateTime(*ex.RegistrationOpensAt), b.localDateTime(*ex.RegistrationClosesAt))
	}
	active, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	if active.ID != 0 && active.ID != ex.ID {
		return refuse("Сейчас идёт «%s». Одновременно может идти только один обмен.", b.EffectiveSettings(active).Title)
	}
	return nil
}

const maxRecipients = 500

// SetBanned blocks users from adding children and from distributions; existing gifts stay as they are.
func (b *Bot) SetBanned(ctx context.Context, userIDs []int64, banned bool, reason string) (int, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case len(userIDs) == 0 || len(userIDs) > maxRecipients:
		return 0, refuse("Выберите от 1 до %d человек.", maxRecipients)
	case banned && reason == "":
		return 0, refuse("Укажите причину блокировки.")
	case len([]rune(reason)) > 500:
		return 0, refuse("Причина: не больше 500 символов.")
	}
	changed := 0
	for _, id := range userIDs {
		ok, err := b.store.SetBanned(ctx, id, banned, reason, b.now())
		if err != nil {
			return changed, err
		}
		if ok {
			changed++
		}
	}
	if changed == 0 {
		return 0, refuse("Пользователи не найдены.")
	}
	return changed, nil
}

// EditParent updates a family's contact details from the admin panel.
func (b *Bot) EditParent(ctx context.Context, userID int64, firstName, lastName, phone, pvz string) error {
	firstName, lastName = strings.TrimSpace(firstName), strings.TrimSpace(lastName)
	phone, pvz = strings.TrimSpace(phone), strings.TrimSpace(pvz)
	switch {
	case !validName(firstName):
		return refuse("Имя: от 1 до 100 символов.")
	case !validName(lastName):
		return refuse("Фамилия: от 1 до 100 символов.")
	}
	if err := validateParentContact(phone, pvz); err != nil {
		return err
	}
	old, err := b.store.UserByID(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return refuse("Семья не найдена.")
	}
	if err != nil {
		return err
	}
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	ok, err := b.store.UpdateUser(ctx, userID, store.User{FirstName: firstName, LastName: lastName, Phone: phone, PVZAddress: pvz})
	if err != nil {
		return err
	}
	if !ok {
		return refuse("Семья не найдена.")
	}
	if old.PVZAddress != pvz {
		b.notifyGiversAboutAddress(ctx, ex, userID, pvz)
	}
	return nil
}

// EditChild updates a child's profile from the admin panel.
func (b *Bot) EditChild(ctx context.Context, childID int64, firstName, lastName string, age int, gender, wishes string) error {
	firstName, lastName = strings.TrimSpace(firstName), strings.TrimSpace(lastName)
	gender, wishes = strings.TrimSpace(gender), strings.TrimSpace(wishes)
	if err := validateChild(firstName, lastName, age, gender, wishes); err != nil {
		return err
	}
	ok, err := b.store.UpdateChild(ctx, childID, store.Child{
		FirstName: firstName,
		LastName:  lastName,
		Age:       age,
		Gender:    gender,
		Wishes:    wishes,
	})
	if err != nil {
		return err
	}
	if !ok {
		return refuse("Участник не найден.")
	}
	return nil
}

// DeleteChild hides a child unless a gift has already been assigned to them.
func (b *Bot) DeleteChild(ctx context.Context, childID int64) error {
	if _, err := b.store.ChildByID(ctx, childID); errors.Is(err, store.ErrNotFound) {
		return refuse("Участник не найден.")
	} else if err != nil {
		return err
	}
	assigned, err := b.store.ChildHasAssignment(ctx, childID)
	if err != nil {
		return err
	}
	if assigned {
		return refuse("Подарок для этого участника уже распределён — удалить нельзя.")
	}
	ok, err := b.store.SoftDeleteChild(ctx, childID)
	if err != nil {
		return err
	}
	if !ok {
		// The atomic UPDATE lost to a concurrent Distribute; re-check to pick the right refusal.
		assigned, err := b.store.ChildHasAssignment(ctx, childID)
		if err != nil {
			return err
		}
		if assigned {
			return refuse("Подарок для этого участника уже распределён — удалить нельзя.")
		}
		return refuse("Участник не найден.")
	}
	return nil
}

// SendMessage delivers an organizer's message to the chosen users from the bot.
func (b *Bot) SendMessage(ctx context.Context, userIDs []int64, text string) (int, error) {
	text = strings.TrimSpace(text)
	switch {
	case len(userIDs) == 0 || len(userIDs) > maxRecipients:
		return 0, refuse("Выберите от 1 до %d получателей.", maxRecipients)
	case text == "" || len([]rune(text)) > 3500:
		return 0, refuse("Сообщение: от 1 до 3500 символов.")
	}
	users, err := b.store.UsersByIDs(ctx, userIDs)
	if err != nil {
		return 0, err
	}
	for _, u := range users {
		b.send(u.TgID, b.tx("org.message", "text", text), nil)
	}
	return len(users), nil
}

// DeleteExchange removes an exchange with everything registered in it. With notify, parents of an
// exchange that was still running get a message that it was cancelled.
func (b *Bot) DeleteExchange(ctx context.Context, id int64, notify bool) error {
	ex, err := b.store.ExchangeByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return refuse("Обмен не найден.")
	}
	if err != nil {
		return err
	}
	title := b.EffectiveSettings(ex).Title
	var parents []int64
	if notify && ex.Status != domain.ExchangeDraft && ex.Status != domain.ExchangeCompleted {
		if parents, err = b.store.ParentTgIDs(ctx, id); err != nil {
			return err
		}
	}
	ok, err := b.store.DeleteExchange(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return refuse("Обмен не найден.")
	}
	if err := b.LoadSettings(ctx); err != nil {
		return err
	}
	for _, tg := range parents {
		b.send(tg, b.tx("org.cancelled", "title", title), nil)
	}
	b.notifyAdmins(fmt.Sprintf("🗑 Обмен «%s» удалён.", esc(title)))
	return nil
}

func (b *Bot) AddViolation(ctx context.Context, exchangeID, userID int64, comment string) error {
	comment = strings.TrimSpace(comment)
	if comment == "" || len([]rune(comment)) > 500 {
		return refuse("Опишите нарушение (до 500 символов).")
	}
	if _, err := b.store.ExchangeByID(ctx, exchangeID); errors.Is(err, store.ErrNotFound) {
		return refuse("Обмен не найден.")
	} else if err != nil {
		return err
	}
	if _, err := b.store.UserByID(ctx, userID); errors.Is(err, store.ErrNotFound) {
		return refuse("Пользователь не найден.")
	} else if err != nil {
		return err
	}
	return b.store.AddManualViolation(ctx, exchangeID, userID, comment)
}

func (b *Bot) SetViolationDismissed(ctx context.Context, id int64, dismissed bool) error {
	ok, err := b.store.SetViolationDismissed(ctx, id, dismissed, b.now())
	if err != nil {
		return err
	}
	if !ok {
		return refuse("Нарушение не найдено.")
	}
	return nil
}

type DistributionPreview struct {
	Families int    `json:"families"`
	Children int    `json:"children"`
	Possible bool   `json:"possible"`
	Reason   string `json:"reason,omitempty"`
}

func (b *Bot) PreviewDistribution(ctx context.Context, id int64) (DistributionPreview, error) {
	ex, err := b.exchangeByID(ctx, id)
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
			p.Possible, p.Reason = false, "Нужно минимум две семьи, и ни одна семья не может привести больше половины всех участников."
		}
	}
	return p, nil
}

// Distribute assigns givers once and sends every participant their cards.
func (b *Bot) Distribute(ctx context.Context, id int64) (int, error) {
	preview, err := b.PreviewDistribution(ctx, id)
	if err != nil {
		return 0, err
	}
	if !preview.Possible {
		return 0, refuse("%s", preview.Reason)
	}
	ex, err := b.exchangeByID(ctx, id)
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
	ex, err := b.store.ActiveExchange(ctx)
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
	ex, err := b.store.ActiveExchange(ctx)
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
		return refuse("Участник %d не найден в распределении.", childID)
	}
	if old.Giver.ID == giverID {
		return refuse("Этот участник уже дарит подарок этому участнику.")
	}
	ok, err := b.store.Reassign(ctx, ex.ID, childID, giverID)
	if err != nil {
		return err
	}
	if !ok {
		return refuse("Нельзя: подарок уже получен или новый даритель — из семьи получателя.")
	}
	updated, err := b.store.Assignment(ctx, old.ID)
	if err != nil {
		return err
	}
	b.send(old.Giver.TgID, b.tx("give.reassigned_away", "child", old.ChildFirstName), nil)
	b.sendGiverCard(updated, b.tx("give.reassigned_new"))
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
		b.send(id, b.tx("org.broadcast", "text", text), nil)
	}
	return len(ids), nil
}

func (b *Bot) InviteLink(ex store.Exchange) string {
	if ex.InviteCode == "" {
		return "https://t.me/" + b.api.Self.UserName
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", b.api.Self.UserName, ex.InviteCode)
}

// ---------- chat commands ----------

const adminHelp = `<b>Организатору</b>

Обмены создаются, открываются, распределяются и завершаются в панели: кнопка «Панель» слева от поля ввода или ниже.

Команды для активного обмена:
/invite — ссылка-приглашение
/families — участники и телефоны
/status — прогресс по подаркам
/remind — разослать напоминания сейчас
/reassign &lt;child_id&gt; &lt;user_id&gt; — передать участника другому дарителю
/broadcast &lt;текст&gt; — сообщение всем участникам`

func (b *Bot) handleAdminCommand(ctx context.Context, msg *tgbotapi.Message, cmd, args string) (bool, error) {
	tgID := msg.From.ID
	var err error
	switch cmd {
	case "admin":
		b.sendAdminPanelButton(tgID, adminHelp)
	case "invite":
		ex, err := b.store.ActiveExchange(ctx)
		if err != nil {
			return false, err
		}
		b.send(tgID, "Ссылка-приглашение:\n"+esc(b.InviteLink(ex)), nil)
	case "open", "close", "finish", "distribute", "new_exchange":
		b.sendAdminPanelButton(tgID, "Обмены создаются, открываются, распределяются и завершаются в панели.")
	case "families":
		err = b.adminFamilies(ctx, tgID)
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

func (b *Bot) adminFamilies(ctx context.Context, tgID int64) error {
	ex, err := b.store.ActiveExchange(ctx)
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
		fmt.Fprintf(&sb, "[user %d] %s %s%s, %s\nПВЗ: %s\nУчастников: %d\n\n",
			p.ID, esc(p.LastName), esc(p.FirstName), usernameSuffix(p.User), esc(p.Phone), esc(p.PVZAddress), p.Children)
	}
	header := fmt.Sprintf("<b>Обмен #%d: %s</b>\nСемей: %d, участников: %d\n\n", ex.ID, StatusTitle(ex.Status), len(list), total)
	b.send(tgID, header+sb.String(), nil)
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
		b.send(giverTg, b.tx("give.distribution", "count", strconv.Itoa(len(items)), "children_word", childrenWord(len(items)),
			"budget", b.budgetText(), "order_deadline", b.orderDeadlineText()), b.mainMenu())
		for _, a := range items {
			b.sendGiverCard(a, "")
		}
	}
	for tg := range parents {
		b.send(tg, b.tx("recv.distribution", "btn_receive", b.label("btn.receive")), nil)
	}
}

func childrenWord(n int) string {
	mod10, mod100 := n%10, n%100
	if mod10 == 1 && mod100 != 11 {
		return "участнику"
	}
	return "участникам"
}

func (b *Bot) adminStatus(ctx context.Context, tgID int64) error {
	ex, err := b.store.ActiveExchange(ctx)
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
