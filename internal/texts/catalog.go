package texts

// Def describes one text participants see. Default uses the same markup organizers can use:
// **bold**, `code` and {placeholders} from Vars.
type Def struct {
	Key     string
	Group   string
	Title   string
	Default string
	Vars    []string
	// Button texts are plain single lines shown on keyboard or inline buttons.
	Button bool
	// Menu buttons are matched by their text when pressed, so they must stay unique.
	Menu bool
}

const (
	groupButtons  = "Кнопки"
	groupCommon   = "Общее"
	groupReg      = "Регистрация"
	groupChildren = "Участники"
	groupGiver    = "Дарителю"
	groupReceiver = "Получателю"
	groupStatus   = "Статусы подарка"
	groupRemind   = "Напоминания"
	groupOrg      = "Сообщения организаторов"
)

var Catalog = []Def{
	// Buttons.
	{Key: "btn.give", Group: groupButtons, Title: "Меню: кому я дарю", Default: "🎁 Кому я дарю", Button: true, Menu: true},
	{Key: "btn.receive", Group: groupButtons, Title: "Меню: подарки моей семьи", Default: "📦 Подарки моей семьи", Button: true, Menu: true},
	{Key: "btn.children", Group: groupButtons, Title: "Меню: участники", Default: "👥 Участники", Button: true, Menu: true},
	{Key: "btn.profile", Group: groupButtons, Title: "Меню: мой профиль", Default: "👤 Мой профиль", Button: true, Menu: true},
	{Key: "btn.rules", Group: groupButtons, Title: "Меню: как это работает", Default: "ℹ️ Как это работает", Button: true, Menu: true},
	{Key: "btn.cancel", Group: groupButtons, Title: "Отмена шага", Default: "Отмена", Button: true},
	{Key: "btn.skip", Group: groupButtons, Title: "Пропустить пожелания", Default: "Пропустить", Button: true},
	{Key: "btn.dont_know", Group: groupButtons, Title: "Не знаю дату доставки", Default: "Не знаю", Button: true},
	{Key: "btn.boy", Group: groupButtons, Title: "Пол: мальчик", Default: "Мальчик", Button: true},
	{Key: "btn.girl", Group: groupButtons, Title: "Пол: девочка", Default: "Девочка", Button: true},
	{Key: "btn.male", Group: groupButtons, Title: "Пол: мужчина", Default: "Мужчина", Button: true},
	{Key: "btn.female", Group: groupButtons, Title: "Пол: женщина", Default: "Женщина", Button: true},
	{Key: "btn.share_phone", Group: groupButtons, Title: "Отправить номер", Default: "📱 Отправить мой номер", Button: true},
	{Key: "btn.edit_profile", Group: groupButtons, Title: "Изменить профиль", Default: "✏️ Изменить данные", Button: true},
	{Key: "btn.add_child", Group: groupButtons, Title: "Добавить участника", Default: "➕ Добавить участника", Button: true},
	{Key: "btn.delete_child", Group: groupButtons, Title: "Удалить участника", Default: "❌ Удалить: {child}", Vars: []string{"child"}, Button: true},
	{Key: "btn.ordered", Group: groupButtons, Title: "Даритель: заказал", Default: "✅ Заказал(а)", Button: true},
	{Key: "btn.arrived", Group: groupButtons, Title: "Даритель: пришло в ПВЗ", Default: "📦 Пришло в ПВЗ", Button: true},
	{Key: "btn.send_code", Group: groupButtons, Title: "Даритель: отправить код", Default: "🔑 Отправить код получения", Button: true},
	{Key: "btn.going", Group: groupButtons, Title: "Получатель: иду забирать", Default: "🚶 Иду забирать, нужен код", Button: true},
	{Key: "btn.show_code", Group: groupButtons, Title: "Получатель: показать код", Default: "🔑 Показать код", Button: true},
	{Key: "btn.received", Group: groupButtons, Title: "Получатель: забрал", Default: "✅ Забрал(а)", Button: true},
	{Key: "btn.bad_code", Group: groupButtons, Title: "Получатель: код не подошёл", Default: "❌ Код не подошёл", Button: true},

	// Common.
	{Key: "common.error", Group: groupCommon, Title: "Ошибка", Default: "Что-то пошло не так. Попробуй ещё раз или напиши организатору."},
	{Key: "common.unknown_command", Group: groupCommon, Title: "Неизвестная команда", Default: "Не знаю такой команды."},
	{Key: "common.choose_menu", Group: groupCommon, Title: "Подсказка выбрать в меню", Default: "Выбери действие в меню."},
	{Key: "common.need_start", Group: groupCommon, Title: "Нужно зарегистрироваться", Default: "Сначала нужно зарегистрироваться: нажми /start."},
	{Key: "common.cancelled", Group: groupCommon, Title: "Шаг отменён", Default: "Отменено."},
	{Key: "common.press_start", Group: groupCommon, Title: "Приглашение нажать /start", Default: "Чтобы участвовать, нажми /start."},
	{Key: "common.no_exchange", Group: groupCommon, Title: "Обмен не идёт", Default: "Сейчас обмен подарками не идёт."},
	{Key: "common.rules", Group: groupCommon, Title: "Как это работает", Vars: []string{"btn_give", "budget", "order_deadline", "delivery"},
		Default: "**Как это работает**\n\n" +
			"1. Регистрируешься: имя, фамилия, телефон и адрес ПВЗ Wildberries, куда удобно забирать посылки.\n" +
			"2. Добавляешь участников — детей и взрослых, которые участвуют в обмене.\n" +
			"3. Когда регистрация закроется, бот распределит участников: твоя семья дарит столько подарков, сколько участников привела, и никогда не дарит своим.\n" +
			"4. В «{btn_give}» увидишь, кому даришь. Заказываешь подарок {budget} на ПВЗ получателя {order_deadline}.\n" +
			"5. Когда посылка придёт, нажимаешь «Пришло в ПВЗ». Получатель попросит код, и ты присылаешь его из приложения WB текстом или скриншотом QR.\n" +
			"6. Код живёт только один день. Если посылку не забрали, получатель запросит новый код, а бот напомнит обоим.\n" +
			"7. Цель: все участники получают подарки до {delivery}."},

	// Registration.
	{Key: "reg.welcome_back", Group: groupReg, Title: "Приветствие зарегистрированного", Default: "Привет, {name}! Главное меню ниже.", Vars: []string{"name"}},
	{Key: "reg.closed", Group: groupReg, Title: "Регистрация закрыта", Default: "Привет! Сейчас регистрация на обмен подарками закрыта."},
	{Key: "reg.need_invite", Group: groupReg, Title: "Нужна ссылка-приглашение", Default: "Привет! Чтобы участвовать, открой бота по ссылке-приглашению от организатора."},
	{Key: "reg.intro", Group: groupReg, Title: "Начало регистрации", Default: "Привет! Это обмен подарками между семьями. Сначала зарегистрируемся: это один взрослый на семью."},
	{Key: "reg.ask_first", Group: groupReg, Title: "Вопрос: имя", Default: "Как тебя зовут? Напиши **имя**."},
	{Key: "reg.bad_first", Group: groupReg, Title: "Ошибка: имя", Default: "Имя должно быть от 1 до 100 символов. Попробуй ещё раз."},
	{Key: "reg.ask_last", Group: groupReg, Title: "Вопрос: фамилия", Default: "Теперь **фамилию**."},
	{Key: "reg.bad_last", Group: groupReg, Title: "Ошибка: фамилия", Default: "Фамилия должна быть от 1 до 100 символов. Попробуй ещё раз."},
	{Key: "reg.ask_phone", Group: groupReg, Title: "Вопрос: телефон", Default: "Нужен телефон, чтобы организатор мог связаться, если с посылкой что-то пойдёт не так. Нажми кнопку или напиши номер."},
	{Key: "reg.foreign_contact", Group: groupReg, Title: "Ошибка: чужой контакт", Default: "Нужен твой номер, а не чужой контакт."},
	{Key: "reg.bad_phone", Group: groupReg, Title: "Ошибка: телефон", Default: "Не похоже на номер телефона. Нажми кнопку или напиши номер, например +375291234567."},
	{Key: "reg.ask_pvz", Group: groupReg, Title: "Вопрос: адрес ПВЗ", Default: "Последний шаг: **адрес ПВЗ Wildberries**, куда тебе будут заказывать посылку. Город, улица, дом."},
	{Key: "reg.bad_pvz", Group: groupReg, Title: "Ошибка: адрес ПВЗ", Default: "Адрес должен быть от 5 до 500 символов. Попробуй ещё раз."},
	{Key: "reg.closed_meanwhile", Group: groupReg, Title: "Регистрация закрылась во время заполнения", Default: "Пока ты заполнял(а) данные, регистрация закрылась. Напиши организатору."},
	{Key: "reg.updated", Group: groupReg, Title: "Профиль обновлён", Default: "Данные обновлены."},
	{Key: "reg.done", Group: groupReg, Title: "Регистрация завершена", Default: "Готово, ты зарегистрирован(а)! Теперь добавь участников — детей и взрослых, которые будут получать подарки."},
	{Key: "reg.profile", Group: groupReg, Title: "Карточка профиля", Default: "**Мой профиль**\n\n{first_name} {last_name}\nТелефон: {phone}\nПВЗ Wildberries: {pvz}",
		Vars: []string{"first_name", "last_name", "phone", "pvz"}},
	{Key: "reg.pvz_changed", Group: groupReg, Title: "Дарителю: получатель сменил ПВЗ", Default: "⚠️ У получателя подарка для **{child}** изменился адрес ПВЗ:\n{pvz}\n\nЕсли ещё не заказал(а), заказывай на новый адрес.",
		Vars: []string{"child", "pvz"}},

	// Children.
	{Key: "children.title", Group: groupChildren, Title: "Заголовок списка участников", Default: "**Наши участники**"},
	{Key: "children.empty", Group: groupChildren, Title: "Участников нет", Default: "Пока никого не добавлено."},
	{Key: "children.item", Group: groupChildren, Title: "Строка участника", Default: "{n}. {child_full}, {age}, {gender}", Vars: []string{"n", "child_full", "age", "gender"}},
	{Key: "children.wishes", Group: groupChildren, Title: "Пожелания участника в списке", Default: "   Пожелания: {wishes}", Vars: []string{"wishes"}},
	{Key: "children.open_hint", Group: groupChildren, Title: "Подсказка: можно менять", Default: "Пока регистрация открыта, список можно менять. Чтобы исправить данные участника, удали его и добавь заново."},
	{Key: "children.closed_hint", Group: groupChildren, Title: "Подсказка: менять нельзя", Default: "Регистрация закрыта, список участников изменить нельзя."},
	{Key: "children.add_closed", Group: groupChildren, Title: "Нельзя добавить: регистрация закрыта", Default: "Регистрация закрыта, добавить участника уже нельзя."},
	{Key: "children.banned", Group: groupChildren, Title: "Нельзя добавить: блокировка", Default: "Участие в обменах для тебя недоступно. Если это ошибка, напиши организатору."},
	{Key: "children.ask_first", Group: groupChildren, Title: "Вопрос: имя участника", Default: "Как зовут участника? Напиши **имя**."},
	{Key: "children.bad_first", Group: groupChildren, Title: "Ошибка: имя участника", Default: "Имя должно быть от 1 до 100 символов."},
	{Key: "children.ask_last", Group: groupChildren, Title: "Вопрос: фамилия участника", Default: "**Фамилия** участника?"},
	{Key: "children.bad_last", Group: groupChildren, Title: "Ошибка: фамилия участника", Default: "Фамилия должна быть от 1 до 100 символов."},
	{Key: "children.ask_age", Group: groupChildren, Title: "Вопрос: возраст", Default: "Сколько полных лет? Напиши число."},
	{Key: "children.bad_age", Group: groupChildren, Title: "Ошибка: возраст", Default: "Напиши возраст числом от 0 до 99."},
	{Key: "children.ask_gender", Group: groupChildren, Title: "Вопрос: пол ребёнка", Default: "Мальчик или девочка?"},
	{Key: "children.ask_gender_adult", Group: groupChildren, Title: "Вопрос: пол взрослого", Default: "Мужчина или женщина?"},
	{Key: "children.bad_gender", Group: groupChildren, Title: "Ошибка: пол", Default: "Выбери пол, нажав на кнопку."},
	{Key: "children.ask_wishes", Group: groupChildren, Title: "Вопрос: пожелания", Default: "Что участник любит или о чём мечтает? Это поможет дарителю выбрать подарок. Можно пропустить."},
	{Key: "children.long_wishes", Group: groupChildren, Title: "Ошибка: длинные пожелания", Default: "Слишком длинно, сократи до 1000 символов."},
	{Key: "children.closed_meanwhile", Group: groupChildren, Title: "Регистрация закрылась во время добавления", Default: "Регистрация закрылась, участника добавить не получилось."},
	{Key: "children.added", Group: groupChildren, Title: "Участник добавлен", Default: "Участник добавлен ✅"},
	{Key: "children.delete_failed", Group: groupChildren, Title: "Не удалось удалить", Default: "Удалить не получилось: регистрация уже закрыта или участник не найден."},

	// Giver.
	{Key: "give.distribution", Group: groupGiver, Title: "Распределение готово", Vars: []string{"count", "children_word", "budget", "order_deadline"},
		Default: "🎄 Распределение готово! Ты даришь подарки {count} {children_word}. Ниже карточки: закажи подарок {budget} на указанный ПВЗ {order_deadline} и нажми «Заказал(а)»."},
	{Key: "give.not_distributed", Group: groupGiver, Title: "Распределения ещё нет", Default: "Распределение ещё не проводилось. Бот напишет, когда оно будет готово."},
	{Key: "give.none", Group: groupGiver, Title: "Нет участников для подарка", Default: "Тебе не досталось участников в этом обмене."},
	{Key: "give.card", Group: groupGiver, Title: "Карточка участника",
		Vars:    []string{"child_full", "age", "gender", "wishes", "parent", "pvz", "budget", "order_deadline", "status"},
		Default: "🎁 **{child_full}**, {age}, {gender}\nПожелания: {wishes}\n\nПолучатель посылки: {parent}\nПВЗ Wildberries: {pvz}\nБюджет: {budget}. Заказать: {order_deadline}.\n\nСтатус: {status}"},
	{Key: "give.no_wishes", Group: groupGiver, Title: "Пожеланий нет (в карточке)", Default: "не указаны"},
	{Key: "give.not_yours", Group: groupGiver, Title: "Подарок не твой", Default: "Этот подарок закреплён не за тобой."},
	{Key: "give.already_ordered", Group: groupGiver, Title: "Уже заказан", Default: "Этот подарок уже отмечен как заказанный."},
	{Key: "give.ask_date", Group: groupGiver, Title: "Вопрос: дата доставки", Default: "Когда примерно доставка подарка для {child}? Напиши дату в формате ДД.ММ, например 15.12.", Vars: []string{"child"}},
	{Key: "give.bad_date", Group: groupGiver, Title: "Ошибка: дата", Default: "Не понял дату. Напиши в формате ДД.ММ, например 15.12, или нажми «Не знаю»."},
	{Key: "give.mark_failed", Group: groupGiver, Title: "Не удалось отметить", Default: "Не получилось: подарок уже отмечен или закреплён не за тобой."},
	{Key: "give.ordered_ok", Group: groupGiver, Title: "Заказ записан", Default: "Отлично, записал. Когда посылка придёт в ПВЗ, нажми «Пришло в ПВЗ»."},
	{Key: "give.arrived_ok", Group: groupGiver, Title: "Посылка в ПВЗ записана", Default: "Записал. Получатель попросит код, когда пойдёт забирать, и я сразу напишу тебе. Можно прислать код и сейчас, если получатель собирается забрать сегодня."},
	{Key: "give.already_received", Group: groupGiver, Title: "Подарок уже получен", Default: "Этот подарок уже получен 🎉"},
	{Key: "give.ask_code", Group: groupGiver, Title: "Вопрос: код получения", Default: "Пришли код получения посылки для {child} из приложения Wildberries: текстом или скриншотом QR-кода. Код действует только сегодня.", Vars: []string{"child"}},
	{Key: "give.bad_code_input", Group: groupGiver, Title: "Ошибка: код", Default: "Пришли код текстом (до 255 символов) или скриншотом QR-кода."},
	{Key: "give.code_failed", Group: groupGiver, Title: "Код не сохранён", Default: "Не получилось сохранить код: подарок уже получен или закреплён не за тобой."},
	{Key: "give.code_sent_ok", Group: groupGiver, Title: "Код отправлен", Default: "Код отправлен получателю ✅ Если сегодня не заберут, я напомню прислать новый."},
	{Key: "give.code_requested", Group: groupGiver, Title: "Получатель просит код", Default: "🔔 Получатель собирается забрать посылку для **{child}**. Пришли, пожалуйста, сегодняшний код получения из приложения Wildberries.", Vars: []string{"child"}},
	{Key: "give.code_rejected", Group: groupGiver, Title: "Код не подошёл", Default: "🔔 Код не подошёл для **{child}**. Пришли, пожалуйста, сегодняшний код получения из приложения Wildberries.", Vars: []string{"child"}},
	{Key: "give.thanks", Group: groupGiver, Title: "Подарок получен", Default: "🎉 Подарок для **{child}** получен. Спасибо, что участвуешь!", Vars: []string{"child"}},
	{Key: "give.reassigned_away", Group: groupGiver, Title: "Подарок передан другому", Default: "Организатор передал подарок для {child} другому участнику. Спасибо!", Vars: []string{"child"}},
	{Key: "give.reassigned_new", Group: groupGiver, Title: "Новый участник от организатора", Default: "🎁 Организатор добавил тебе ещё одного участника:"},

	// Receiver.
	{Key: "recv.distribution", Group: groupReceiver, Title: "Распределение готово", Default: "Твоя семья тоже получает подарки 🎁 Когда посылка придёт в ПВЗ, я напишу, а статус можно смотреть в «{btn_receive}».", Vars: []string{"btn_receive"}},
	{Key: "recv.none", Group: groupReceiver, Title: "Участников нет в обмене", Default: "От твоей семьи в этом обмене никого нет."},
	{Key: "recv.item", Group: groupReceiver, Title: "Статус подарка участника", Default: "📦 Подарок для **{child}**: {status}", Vars: []string{"child", "status"}},
	{Key: "recv.ordered", Group: groupReceiver, Title: "Подарок заказан", Default: "🎁 Подарок для **{child}** заказан, {when}. Когда посылка придёт в ПВЗ, я напишу.", Vars: []string{"child", "when"}},
	{Key: "recv.when_unknown", Group: groupReceiver, Title: "Дата доставки неизвестна", Default: "дату доставки пока не знаем"},
	{Key: "recv.when_date", Group: groupReceiver, Title: "Ожидаемая дата доставки", Default: "ожидаемая дата доставки {date}", Vars: []string{"date"}},
	{Key: "recv.arrived", Group: groupReceiver, Title: "Посылка пришла в ПВЗ", Default: "📦 Посылка для **{child}** пришла в ПВЗ:\n{pvz}\n\nКогда соберёшься забирать, нажми «Иду забирать», и даритель пришлёт код. Код действует только в день отправки.", Vars: []string{"child", "pvz"}},
	{Key: "recv.code", Group: groupReceiver, Title: "Код получения", Default: "🔑 Код получения посылки для **{child}**: `{code}`\n\nПВЗ: {pvz}\nКод действует **только сегодня**. Когда заберёшь, нажми «Забрал(а)».", Vars: []string{"child", "code", "pvz"}},
	{Key: "recv.code_photo", Group: groupReceiver, Title: "Код получения (скриншот QR)", Default: "🔑 Код получения посылки для **{child}**\n\nПВЗ: {pvz}\nКод действует **только сегодня**. Когда заберёшь, нажми «Забрал(а)».", Vars: []string{"child", "pvz"}},
	{Key: "recv.not_yours", Group: groupReceiver, Title: "Не твоя посылка", Default: "Это не посылка для участника твоей семьи."},
	{Key: "recv.already_received", Group: groupReceiver, Title: "Уже получена", Default: "Эта посылка уже отмечена как полученная 🎉"},
	{Key: "recv.not_arrived", Group: groupReceiver, Title: "Ещё не в ПВЗ", Default: "Посылка ещё не пришла в ПВЗ. Я напишу, когда даритель отметит доставку."},
	{Key: "recv.request_failed", Group: groupReceiver, Title: "Не удалось запросить код", Default: "Не получилось запросить код, попробуй ещё раз."},
	{Key: "recv.requested", Group: groupReceiver, Title: "Код запрошен", Default: "Попросил дарителя прислать код. Как только он придёт, я перешлю его сюда."},
	{Key: "recv.thanks", Group: groupReceiver, Title: "Подарок получен", Default: "🎉 Ура! Подарок для {child} получен. Спасибо!", Vars: []string{"child"}},
	{Key: "recv.mark_failed", Group: groupReceiver, Title: "Не удалось отметить получение", Default: "Эта посылка уже отмечена как полученная или ещё не отправлена."},

	// Gift statuses.
	{Key: "status.assigned", Group: groupStatus, Title: "Не заказан", Default: "ещё не заказан"},
	{Key: "status.ordered", Group: groupStatus, Title: "Заказан", Default: "заказан"},
	{Key: "status.ordered_expected", Group: groupStatus, Title: "Заказан, с датой", Default: "заказан, ожидается {date}", Vars: []string{"date"}},
	{Key: "status.arrived", Group: groupStatus, Title: "В ПВЗ", Default: "ждёт в ПВЗ"},
	{Key: "status.arrived_code_sent", Group: groupStatus, Title: "В ПВЗ, код отправлен", Default: "ждёт в ПВЗ, код на сегодня отправлен"},
	{Key: "status.arrived_code_wait", Group: groupStatus, Title: "В ПВЗ, ждут код", Default: "ждёт в ПВЗ, получатель ждёт код"},
	{Key: "status.received", Group: groupStatus, Title: "Получен", Default: "получен ✅"},

	// Reminders.
	{Key: "remind.order", Group: groupRemind, Title: "Дарителю: закажи подарок", Default: "⏰ Напоминание: подарок для **{child}** нужно заказать {order_deadline}.", Vars: []string{"child", "order_deadline"}},
	{Key: "remind.arrival_check", Group: groupRemind, Title: "Дарителю: посылка пришла?", Default: "📦 Посылка для **{child}** уже пришла в ПВЗ? Если да, нажми «Пришло в ПВЗ».", Vars: []string{"child"}},
	{Key: "remind.code_pending", Group: groupRemind, Title: "Дарителю: ждут код", Default: "🔔 Получатель ждёт код, чтобы забрать посылку для **{child}**. Пришли сегодняшний код из приложения Wildberries.", Vars: []string{"child"}},
	{Key: "remind.code_expired", Group: groupRemind, Title: "Дарителю: вчерашний код сгорел", Default: "Вчерашний код для посылки **{child}** не использовали, посылка всё ещё в ПВЗ. Когда получатель снова попросит, пришли новый код.", Vars: []string{"child"}},
	{Key: "remind.pickup", Group: groupRemind, Title: "Получателю: посылка ждёт", Default: "📦 Посылка для **{child}** ждёт в ПВЗ:\n{pvz}\n\nКогда соберёшься забирать, нажми «Иду забирать», и даритель пришлёт код на сегодня.", Vars: []string{"child", "pvz"}},
	{Key: "remind.code_today", Group: groupRemind, Title: "Получателю: код до конца дня", Default: "⏳ Код для посылки **{child}** действует только до конца дня. Не успеваешь? Завтра нажми «Иду забирать», и даритель пришлёт новый.", Vars: []string{"child"}},

	// Organizer messages.
	{Key: "org.message", Group: groupOrg, Title: "Личное сообщение", Default: "✉️ **Сообщение от организаторов**\n\n{text}", Vars: []string{"text"}},
	{Key: "org.broadcast", Group: groupOrg, Title: "Сообщение всем", Default: "📣 {text}", Vars: []string{"text"}},
	{Key: "org.cancelled", Group: groupOrg, Title: "Обмен отменён", Default: "Обмен подарками «{title}» отменён организаторами. Если ты уже что-то заказал(а), напиши организатору.", Vars: []string{"title"}},
}
