"use strict";

const tg = window.Telegram ? window.Telegram.WebApp : null;
if (tg) {
  tg.ready();
  tg.expand();
}

const STEPS = [
  ["draft", "Черновик"],
  ["registration_open", "Регистрация"],
  ["registration_closed", "Закрыта"],
  ["distributed", "Распределено"],
  ["completed", "Завершено"],
];

const GIFT_STATUS = {
  assigned: "не заказан",
  ordered: "заказан",
  arrived: "в ПВЗ",
  received: "получен",
};

const FILTERS = [
  ["all", "Все"],
  ["problems", "Требуют внимания"],
  ["assigned", "Не заказано"],
  ["ordered", "Заказано"],
  ["arrived", "В ПВЗ"],
  ["received", "Получено"],
];

let state = null;
let activeTab = "overview";
let giftFilter = "all";
let familyQuery = "";

// h builds DOM nodes; strings always go through text nodes, so user data is never parsed as HTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") el.className = v;
    // CSSOM, not the style attribute: CSP forbids inline style attributes.
    else if (k === "style") el.style.cssText = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function toast(text, isError) {
  const el = document.getElementById("toast");
  el.textContent = text;
  el.className = "toast" + (isError ? " error" : "");
  el.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { el.hidden = true; }, 3500);
}

function haptic(kind) {
  if (tg && tg.HapticFeedback) tg.HapticFeedback.notificationOccurred(kind);
}

function confirmAction(text) {
  return new Promise((resolve) => {
    if (tg && tg.showConfirm) tg.showConfirm(text, resolve);
    else resolve(window.confirm(text));
  });
}

async function api(path, body) {
  const opts = { headers: { Authorization: "tma " + (tg ? tg.initData : "") } };
  if (body !== undefined) {
    opts.method = "POST";
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "Ошибка " + res.status);
  return data;
}

async function load() {
  try {
    state = await api("/api/state");
    render();
  } catch (e) {
    document.getElementById("subtitle").textContent = "";
    showOnly(h("div", { class: "empty" }, e.message));
  }
}

async function run(path, body, confirmText, button) {
  if (confirmText && !(await confirmAction(confirmText))) return false;
  if (button) button.disabled = true;
  try {
    const res = await api(path, body);
    haptic("success");
    toast(res.message);
    await load();
    return true;
  } catch (e) {
    haptic("error");
    toast(e.message, true);
    return false;
  } finally {
    if (button) button.disabled = false;
  }
}

function action(name, confirmText) {
  return (ev) => run("/api/action", { action: name }, confirmText, ev.currentTarget);
}

function showOnly(node) {
  const panel = document.getElementById("tab-" + activeTab);
  panel.replaceChildren(node);
}

// ---------- rendering ----------

function render() {
  const ex = state.exchange;
  document.getElementById("title").textContent = "Обмен #" + ex.id;
  document.getElementById("subtitle").textContent = ex.title;
  document.querySelector('[data-tab="families"]').textContent = "Семьи · " + state.families.length;
  document.querySelector('[data-tab="gifts"]').textContent = "Подарки · " + state.assignments.length;
  document.getElementById("tab-overview").replaceChildren(...renderOverview());
  document.getElementById("tab-families").replaceChildren(...renderFamilies());
  document.getElementById("tab-gifts").replaceChildren(...renderGifts());
}

function countBy(list, fn) {
  const out = {};
  for (const x of list) {
    const k = fn(x);
    out[k] = (out[k] || 0) + 1;
  }
  return out;
}

function renderOverview() {
  const ex = state.exchange;
  const idx = STEPS.findIndex(([s]) => s === ex.status);
  const kids = state.families.reduce((n, f) => n + f.children.length, 0);

  const progress = h("div", { class: "card" },
    h("div", { class: "steps" }, STEPS.map((_, i) => h("div", { class: "step" + (i <= idx ? " done" : "") }))),
    h("div", { class: "step-labels" }, STEPS.map(([, label]) => h("span", null, label))),
  );

  const stats = h("div", { class: "card" },
    h("div", { class: "stats" },
      h("div", { class: "stat" }, h("b", null, state.families.length), h("span", { class: "muted" }, "семей")),
      h("div", { class: "stat" }, h("b", null, kids), h("span", { class: "muted" }, "детей")),
    ),
    state.without_children.length
      ? h("div", { class: "muted line" }, "Зарегистрировались без детей: " + state.without_children.length)
      : null,
  );

  return [progress, stageCard(ex.status), stats, inviteCard(), settingsCard(), broadcastCard()].filter(Boolean);
}

function stageCard(status) {
  const p = state.preview;
  switch (status) {
    case "draft":
      return h("div", { class: "card" },
        h("h3", null, "Регистрация ещё не открыта"),
        h("div", { class: "muted" }, "После открытия родители смогут зарегистрироваться по ссылке-приглашению."),
        h("button", { class: "btn", type: "button", onclick: action("open") }, "Открыть регистрацию"),
      );
    case "registration_open":
      return h("div", { class: "card" },
        h("h3", null, "Идёт регистрация"),
        h("div", { class: "muted" }, "Когда все семьи добавят детей, закройте регистрацию. Новых участников после этого не будет."),
        h("button", { class: "btn", type: "button", onclick: action("close", "Закрыть регистрацию? Новые участники не смогут присоединиться.") }, "Закрыть регистрацию"),
      );
    case "registration_closed":
      return h("div", { class: "card" },
        h("h3", null, "Готово к распределению"),
        h("div", null, `Семей: ${p.families}, детей: ${p.children}.`),
        p.possible
          ? h("div", { class: "muted line" }, "Каждая семья дарит столько подарков, сколько детей привела, и не дарит своим. Распределение делается один раз, всем участникам придут карточки.")
          : h("div", { class: "line", style: "color: var(--danger)" }, p.reason),
        h("button", {
          class: "btn", type: "button", disabled: !p.possible,
          onclick: action("distribute", "Распределить детей? Отменить будет нельзя, участникам сразу придут карточки."),
        }, "Распределить детей"),
        h("button", { class: "btn secondary", type: "button", onclick: action("open") }, "Открыть регистрацию снова"),
      );
    case "distributed": {
      const total = state.assignments.length;
      const c = countBy(state.assignments, (a) => a.status);
      const problems = state.assignments.filter(isProblem).length;
      const done = c.received || 0;
      return h("div", { class: "card" },
        h("h3", null, `Получено ${done} из ${total}`),
        h("div", { class: "progress" }, h("div", { style: `width: ${total ? (done * 100) / total : 0}%` })),
        h("div", { class: "stats" },
          statBox(c.assigned, "не заказано"), statBox(c.ordered, "заказано"),
          statBox(c.arrived, "в ПВЗ"), statBox(problems, "требуют внимания"),
        ),
        h("button", { class: "btn secondary", type: "button", onclick: action("remind", "Отправить напоминания всем, кто ещё не заказал или не забрал посылку?") }, "Напомнить отстающим"),
        h("button", { class: "btn danger", type: "button", onclick: action("finish", "Завершить обмен? Напоминания прекратятся.") }, "Завершить обмен"),
      );
    }
    case "completed":
      return h("div", { class: "card" },
        h("h3", null, "Обмен завершён"),
        h("button", { class: "btn", type: "button", onclick: action("new_exchange", "Начать новый обмен? Текущий останется в истории.") }, "Начать новый обмен"),
      );
  }
  return null;
}

function statBox(n, label) {
  return h("div", { class: "stat" }, h("b", null, n || 0), h("span", { class: "muted" }, label));
}

function budgetText(s) {
  return `до ${s.budget_max} ${s.currency}`;
}

function inviteText() {
  const s = state.settings;
  const lines = [
    "Приглашаем детей в новогодний обмен подарками 🎁",
    "Каждая семья дарит подарки детям из других семей: бот подскажет, кому и на какой ПВЗ Wildberries заказать. Бюджет подарка " + budgetText(s) + ".",
  ];
  if (s.order_deadline && s.delivery_by) {
    lines.push("Заказать подарок нужно до " + fmtDate(s.order_deadline) + ", все подарки дети получат до " + fmtDate(s.delivery_by) + ".");
  }
  lines.push("Чтобы участвовать, откройте бота по ссылке и зарегистрируйтесь:");
  return lines.join("\n");
}

// shareInvite opens Telegram's own chat picker: the admin chooses contacts or groups and the message goes from their account.
// The bot cannot message people who never started it, so this is the only way to reach new parents.
function shareInvite(text) {
  const link = "https://t.me/share/url?url=" + encodeURIComponent(state.invite) + "&text=" + encodeURIComponent(text);
  if (tg && tg.openTelegramLink) tg.openTelegramLink(link);
  else window.open(link, "_blank");
}

function inviteCard() {
  const status = state.exchange.status;
  if (status !== "registration_open" && status !== "draft") return null;
  if (status === "draft") {
    return h("div", { class: "card" },
      h("h3", null, "Приглашение"),
      h("div", { class: "muted" }, "Откройте регистрацию, чтобы отправлять приглашения: до этого бот не пустит новых участников."),
    );
  }
  const text = h("textarea", null, inviteText());
  const input = h("input", { value: state.invite, readonly: true });
  return h("div", { class: "card" },
    h("h3", null, "Приглашение"),
    h("div", { class: "muted" }, "Текст можно поправить. Ссылка добавится к сообщению автоматически."),
    h("div", { class: "line" }, text),
    h("button", {
      class: "btn", type: "button",
      onclick: () => {
        const value = text.value.trim();
        if (!value) return toast("Введите текст приглашения", true);
        shareInvite(value);
      },
    }, "Отправить приглашение"),
    h("div", { class: "muted line" }, "Откроется выбор чатов Telegram: можно отметить несколько контактов или родительский чат."),
    h("div", { class: "line" }, input),
    h("button", {
      class: "btn small secondary", type: "button",
      onclick: async () => {
        try {
          await navigator.clipboard.writeText(state.invite);
          toast("Ссылка скопирована");
        } catch {
          input.select();
          toast("Выделите ссылку и скопируйте");
        }
      },
    }, "Скопировать только ссылку"),
  );
}

function settingsCard() {
  const s = state.settings;
  const budget = h("input", { type: "number", min: "1", max: "100000", step: "1", inputmode: "numeric", value: s.budget_max });
  const currency = h("input", { type: "text", maxlength: "10", value: s.currency });
  const order = h("input", { type: "date", value: s.order_deadline });
  const delivery = h("input", { type: "date", value: s.delivery_by });
  const field = (label, input) => h("label", { class: "field" }, h("span", { class: "muted" }, label), input);

  return h("div", { class: "card" },
    h("h3", null, "Условия"),
    h("div", { class: "field-row" }, field("Бюджет подарка, до", budget), field("Валюта", currency)),
    field("Заказать подарок до", order),
    field("Дети получают подарки до", delivery),
    state.exchange.status === "distributed"
      ? h("div", { class: "muted line" }, "Карточки уже разосланы. Новые условия попадут в следующие сообщения и напоминания, старые сообщения не изменятся.")
      : null,
    h("button", {
      class: "btn secondary", type: "button",
      onclick: (ev) => {
        if (!order.value || !delivery.value) return toast("Укажите обе даты", true);
        run("/api/settings", {
          budget_max: Number(budget.value), currency: currency.value.trim(),
          order_deadline: order.value, delivery_by: delivery.value,
        }, null, ev.currentTarget);
      },
    }, "Сохранить условия"),
  );
}

function broadcastCard() {
  const text = h("textarea", { placeholder: "Сообщение всем зарегистрированным участникам" });
  return h("div", { class: "card" },
    h("h3", null, "Сообщение всем"),
    text,
    h("button", {
      class: "btn secondary", type: "button",
      onclick: (ev) => {
        const value = text.value.trim();
        if (!value) return toast("Введите текст сообщения", true);
        run("/api/broadcast", { text: value }, "Отправить сообщение всем участникам?", ev.currentTarget)
          .then((ok) => { if (ok) text.value = ""; });
      },
    }, "Отправить"),
  );
}

function contactLine(p) {
  return h("div", { class: "row line" },
    p.username ? h("a", { href: "https://t.me/" + p.username, target: "_blank" }, "@" + p.username) : null,
    p.phone ? h("a", { href: "tel:" + p.phone.replace(/[^\d+]/g, "") }, p.phone) : null,
  );
}

function ageText(age) {
  const m10 = age % 10, m100 = age % 100;
  if (m10 === 1 && m100 !== 11) return age + " год";
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return age + " года";
  return age + " лет";
}

function kidLine(c) {
  return h("div", { class: "kid" },
    h("div", null, `${c.name}, ${ageText(c.age)}, ${c.gender === "girl" ? "девочка" : "мальчик"}`),
    c.wishes ? h("div", { class: "muted" }, "Пожелания: " + c.wishes) : null,
  );
}

function renderFamilies() {
  const search = h("input", {
    type: "search", placeholder: "Поиск по имени, телефону, ребёнку", value: familyQuery,
    oninput: (ev) => {
      familyQuery = ev.target.value;
      const list = document.getElementById("family-list");
      list.replaceChildren(...familyCards());
    },
  });
  const out = [h("div", { class: "card" }, search), h("div", { id: "family-list" }, familyCards())];
  if (state.without_children.length) {
    out.push(h("div", { class: "card" },
      h("h3", null, "Зарегистрировались, но не добавили детей"),
      state.without_children.map((p) => h("div", { class: "kid" }, p.name, contactLine(p))),
    ));
  }
  return out;
}

function familyCards() {
  const q = familyQuery.trim().toLowerCase();
  const list = state.families.filter((f) => !q || JSON.stringify([f.name, f.username, f.phone, f.children.map((c) => c.name)]).toLowerCase().includes(q));
  if (!list.length) return [h("div", { class: "empty" }, q ? "Никого не нашли" : "Пока никто не зарегистрировался")];
  return list.map((f) => h("div", { class: "card" },
    h("div", { class: "row" },
      h("h3", null, f.name),
      h("span", { class: "spacer" }),
      state.assignments.length ? h("span", { class: "badge" }, "дарит " + f.gives) : null,
    ),
    contactLine(f),
    h("div", { class: "muted line" }, "ПВЗ: " + f.pvz),
    h("div", { class: "line" }, f.children.map(kidLine)),
  ));
}

function isProblem(a) {
  return a.stuck || a.code_pending || (a.status === "ordered" && a.expected_date && a.expected_date < today());
}

function today() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function fmtDate(value) {
  if (!value) return "";
  const d = new Date(value.length === 10 ? value + "T00:00:00" : value);
  return d.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" });
}

function renderGifts() {
  if (!state.assignments.length) {
    return [h("div", { class: "empty" }, "Распределения ещё не было")];
  }
  const counts = countBy(state.assignments, (a) => a.status);
  counts.all = state.assignments.length;
  counts.problems = state.assignments.filter(isProblem).length;

  const chips = h("div", { class: "chips" }, FILTERS.map(([key, label]) =>
    h("button", {
      class: "chip" + (giftFilter === key ? " active" : ""), type: "button",
      onclick: () => { giftFilter = key; render(); },
    }, `${label} ${counts[key] || 0}`)));

  const list = state.assignments.filter((a) =>
    giftFilter === "all" || (giftFilter === "problems" ? isProblem(a) : a.status === giftFilter));

  return [chips, ...(list.length ? list.map(giftCard) : [h("div", { class: "empty" }, "Здесь пусто")])];
}

function giftCard(a) {
  const notes = [];
  if (a.status === "ordered") notes.push(a.expected_date ? "ожидается " + fmtDate(a.expected_date) : "дата доставки неизвестна");
  if (a.status === "arrived") {
    notes.push(`в ПВЗ ${a.days_in_pvz} дн.`);
    if (a.code_valid) notes.push("код на сегодня отправлен");
    if (a.code_pending) notes.push("получатель ждёт код");
  }
  if (a.status === "received") notes.push("получен " + fmtDate(a.received_at));

  const card = h("div", { class: "card" },
    h("div", { class: "row" },
      h("h3", null, `${a.child.name}, ${ageText(a.child.age)}`),
      h("span", { class: "spacer" }),
      h("span", { class: "badge " + a.status }, GIFT_STATUS[a.status] || a.status),
      isProblem(a) ? h("span", { class: "badge alert" }, "!") : null,
    ),
    notes.length ? h("div", { class: "muted" }, notes.join(" · ")) : null,
    h("div", { class: "line" }, h("span", { class: "muted" }, "Получатель: "), a.parent.name),
    contactLine(a.parent),
    h("div", { class: "line" }, h("span", { class: "muted" }, "Даритель: "), a.giver.name),
    contactLine(a.giver),
  );

  if (a.status !== "received" && state.exchange.status === "distributed") {
    card.append(reassignControl(a));
  }
  return card;
}

function reassignControl(a) {
  const wrap = h("div", null);
  const open = h("button", {
    class: "btn small secondary", type: "button",
    onclick: () => {
      const candidates = state.families.filter((f) => f.id !== a.parent.id && f.id !== a.giver.id);
      const select = h("select", null,
        h("option", { value: "" }, "Кому передать подарок?"),
        candidates.map((f) => h("option", { value: f.id }, `${f.name} (дарит ${f.gives})`)));
      wrap.replaceChildren(
        h("div", { class: "line" }, select),
        h("button", {
          class: "btn small", type: "button",
          onclick: (ev) => {
            const giverId = Number(select.value);
            if (!giverId) return toast("Выберите участника", true);
            const name = select.options[select.selectedIndex].text;
            run("/api/reassign", { child_id: a.child.id, giver_id: giverId },
              `Передать подарок для ${a.child.name} участнику ${name}? Прогресс по подарку сбросится.`, ev.currentTarget);
          },
        }, "Передать"),
      );
    },
  }, "Передать другому дарителю");
  wrap.append(open);
  return wrap;
}

// ---------- tabs ----------

for (const btn of document.querySelectorAll(".tab")) {
  btn.addEventListener("click", () => {
    activeTab = btn.dataset.tab;
    for (const b of document.querySelectorAll(".tab")) b.classList.toggle("active", b === btn);
    for (const p of document.querySelectorAll(".tab-panel")) p.hidden = p.id !== "tab-" + activeTab;
  });
}
document.getElementById("refresh").addEventListener("click", load);

load();
