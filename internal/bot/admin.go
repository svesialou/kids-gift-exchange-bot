package bot

import (
	"context"
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

const adminHelp = `<b>Команды организатора</b>

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
	switch cmd {
	case "admin":
		b.send(tgID, adminHelp, nil)
	case "invite":
		b.adminInvite(tgID)
	case "open":
		return true, b.adminTransition(ctx, tgID, domain.ExchangeOpen, "Регистрация открыта.", domain.ExchangeDraft, domain.ExchangeClosed)
	case "close":
		return true, b.adminTransition(ctx, tgID, domain.ExchangeClosed, "Регистрация закрыта. Проверь /families и запускай /distribute.", domain.ExchangeOpen)
	case "finish":
		return true, b.adminTransition(ctx, tgID, domain.ExchangeCompleted, "Обмен завершён. Напоминания остановлены.", domain.ExchangeDistributed)
	case "new_exchange":
		return true, b.adminNewExchange(ctx, tgID)
	case "families":
		return true, b.adminFamilies(ctx, tgID)
	case "distribute":
		return true, b.adminDistributePreview(ctx, tgID)
	case "status":
		return true, b.adminStatus(ctx, tgID)
	case "remind":
		return true, b.adminRemind(ctx, tgID)
	case "reassign":
		return true, b.adminReassign(ctx, tgID, args)
	case "broadcast":
		return true, b.adminBroadcast(ctx, tgID, args)
	default:
		return false, nil
	}
	return true, nil
}

func (b *Bot) adminInvite(tgID int64) {
	if b.cfg.InviteCode == "" {
		b.send(tgID, fmt.Sprintf("INVITE_CODE не задан, бот открыт для всех: https://t.me/%s", b.api.Self.UserName), nil)
		return
	}
	b.send(tgID, fmt.Sprintf("Ссылка-приглашение:\nhttps://t.me/%s?start=%s", b.api.Self.UserName, esc(b.cfg.InviteCode)), nil)
}

func (b *Bot) adminTransition(ctx context.Context, tgID int64, to domain.ExchangeStatus, done string, from ...domain.ExchangeStatus) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	ok, err := b.store.TransitionExchange(ctx, ex.ID, to, from...)
	if err != nil {
		return err
	}
	if !ok {
		b.send(tgID, fmt.Sprintf("Нельзя: сейчас обмен в статусе «%s».", ex.Status), nil)
		return nil
	}
	b.send(tgID, done, nil)
	return nil
}

func (b *Bot) adminNewExchange(ctx context.Context, tgID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeCompleted {
		b.send(tgID, "Новый обмен можно начать только после /finish текущего.", nil)
		return nil
	}
	if err := b.store.CreateExchange(ctx); err != nil {
		return err
	}
	b.send(tgID, "Создан новый обмен в статусе draft. Открой регистрацию командой /open.", nil)
	return nil
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
		username := ""
		if p.TgUsername != "" {
			username = " @" + p.TgUsername
		}
		fmt.Fprintf(&sb, "[user %d] %s %s%s, %s\nПВЗ: %s\nДетей: %d\n\n",
			p.ID, esc(p.LastName), esc(p.FirstName), esc(username), esc(p.Phone), esc(p.PVZAddress), p.Children)
	}
	header := fmt.Sprintf("<b>Обмен #%d, статус %s</b>\nСемей: %d, детей: %d\n\n", ex.ID, ex.Status, len(list), total)
	b.send(tgID, header+sb.String(), nil)
	return nil
}

func (b *Bot) adminDistributePreview(ctx context.Context, tgID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeClosed {
		b.send(tgID, fmt.Sprintf("Распределять можно только после закрытия регистрации. Сейчас статус «%s».", ex.Status), nil)
		return nil
	}
	refs, err := b.store.ChildRefs(ctx, ex.ID)
	if err != nil {
		return err
	}
	families := map[int64]int{}
	for _, r := range refs {
		families[r.ParentID]++
	}
	if _, err := domain.Distribute(refs, rand.New(rand.NewSource(1))); err != nil {
		b.send(tgID, fmt.Sprintf("Семей: %d, детей: %d.\n❗ Распределить нельзя: %v", len(families), len(refs), err), nil)
		return nil
	}
	b.send(tgID, fmt.Sprintf(
		"Семей: %d, детей: %d.\nКаждая семья дарит столько подарков, сколько детей привела, и не дарит своим.\n\nПосле подтверждения распределение не изменить, всем участникам придут карточки.",
		len(families), len(refs)),
		inlineButtons(button("✅ Распределить", "dist", ex.ID)))
	return nil
}

func (b *Bot) confirmDistribution(ctx context.Context, tgID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	n, err := b.store.Distribute(ctx, ex.ID, func(refs []domain.ChildRef) ([]domain.Pair, error) {
		return domain.Distribute(refs, rnd)
	})
	if err != nil {
		b.send(tgID, "Распределение не выполнено: "+esc(err.Error()), nil)
		return nil
	}

	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	b.notifyDistribution(list)
	b.notifyAdmins(fmt.Sprintf("✅ Распределение готово: %d подарков. Участникам отправлены карточки.", n))
	return nil
}

func (b *Bot) notifyDistribution(list []store.Assignment) {
	byGiver := map[int64][]store.Assignment{}
	parents := map[int64]store.User{}
	var order []int64
	for _, a := range list {
		if _, seen := byGiver[a.Giver.TgID]; !seen {
			order = append(order, a.Giver.TgID)
		}
		byGiver[a.Giver.TgID] = append(byGiver[a.Giver.TgID], a)
		parents[a.Parent.TgID] = a.Parent
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
		b.send(tgID, fmt.Sprintf("Обмен #%d, статус %s. Распределения ещё нет.", ex.ID, ex.Status), nil)
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
	header := fmt.Sprintf("<b>Обмен #%d, статус %s</b>\nВсего: %d. Не заказано: %d, заказано: %d, в ПВЗ: %d, получено: %d.\n\n",
		ex.ID, ex.Status, len(list),
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

func (b *Bot) adminRemind(ctx context.Context, tgID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeDistributed {
		b.send(tgID, "Напоминания работают только после распределения.", nil)
		return nil
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
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
	b.send(tgID, fmt.Sprintf("Отправлено напоминаний: %d.", sent), nil)
	return nil
}

func (b *Bot) adminReassign(ctx context.Context, tgID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 {
		b.send(tgID, "Формат: /reassign &lt;child_id&gt; &lt;user_id&gt;. ID есть в /status и /families.", nil)
		return nil
	}
	childID, err1 := strconv.ParseInt(fields[0], 10, 64)
	giverID, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil {
		b.send(tgID, "child_id и user_id должны быть числами.", nil)
		return nil
	}
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if _, err := b.store.UserByID(ctx, giverID); errors.Is(err, store.ErrNotFound) {
		b.send(tgID, "Нет участника с таким user_id.", nil)
		return nil
	} else if err != nil {
		return err
	}

	var old *store.Assignment
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ChildID == childID {
			old = &list[i]
			break
		}
	}
	if old == nil {
		b.send(tgID, "Ребёнок не найден в распределении.", nil)
		return nil
	}

	ok, err := b.store.Reassign(ctx, ex.ID, childID, giverID)
	if err != nil {
		return err
	}
	if !ok {
		b.send(tgID, "Нельзя: подарок уже получен или новый даритель — родитель этого ребёнка.", nil)
		return nil
	}
	updated, err := b.store.Assignment(ctx, old.ID)
	if err != nil {
		return err
	}
	if old.Giver.ID != giverID {
		b.send(old.Giver.TgID, fmt.Sprintf("Организатор передал подарок для %s другому участнику. Спасибо!", esc(old.ChildFirstName)), nil)
	}
	b.sendGiverCard(updated, "🎁 Организатор добавил тебе ещё одного ребёнка:")
	b.send(tgID, "Готово, новый даритель получил карточку.", nil)
	return nil
}

func (b *Bot) adminBroadcast(ctx context.Context, tgID int64, text string) error {
	if text == "" {
		b.send(tgID, "Формат: /broadcast текст сообщения", nil)
		return nil
	}
	ids, err := b.store.AllUserTgIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		b.send(id, "📣 "+esc(text), nil)
	}
	b.send(tgID, fmt.Sprintf("Отправлено %d участникам.", len(ids)), nil)
	return nil
}
