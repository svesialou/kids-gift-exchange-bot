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
  ["distributed", "Подарки"],
  ["completed", "Завершён"],
];

const GIFT_STATUS = { assigned: "не отправлен", ordered: "отправлен", arrived: "в ПВЗ", received: "получен" };

const VIOLATION_KIND = {
  order_overdue: "Не отправил подарок в срок",
  delivery_overdue: "Подарок не пришёл к завершению",
  manual: "Отмечено вручную",
};

const GIFT_FILTERS = [
  ["all", "Все"], ["problems", "Требуют внимания"], ["assigned", "Не отправлено"],
  ["ordered", "Отправлено"], ["arrived", "В ПВЗ"], ["received", "Получено"],
];

const EXCHANGE_SECTIONS = [["overview", "Обзор"], ["families", "Семьи"], ["gifts", "Подарки"], ["violations", "Нарушения"]];

const PEOPLE_FILTERS = [["all", "Все"], ["flag", "🚩 С нарушениями"], ["banned", "Заблокированы"]];

// view describes what is on screen; back returns to the screen it was opened from.
let view = { section: "exchanges", exchangeId: null, sub: "overview", userId: null, back: null };
const ui = { giftFilter: "all", query: "", peopleFilter: "all", selecting: false, selected: new Set(), changedOnly: false };
const data = { exchange: null, exchanges: null, people: null, user: null, texts: null };

// ---------- helpers ----------

// h builds DOM nodes; strings always become text nodes, so user data is never parsed as HTML.
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
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(json.error || "Ошибка " + res.status);
  return json;
}

// run performs a write, shows the result and reloads the screen. Returns true on success.
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
  return (ev) => run(`/api/exchanges/${data.exchange.exchange.id}/action`, { action: name }, confirmText, ev.currentTarget);
}

function ageText(age) {
  const m10 = age % 10, m100 = age % 100;
  if (m10 === 1 && m100 !== 11) return age + " год";
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return age + " года";
  return age + " лет";
}

function fmtDate(value) {
  if (!value) return "";
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value.slice(8, 10) + "." + value.slice(5, 7);
  return new Date(value).toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" });
}

// fmtLocal shows a "YYYY-MM-DDTHH:MM" value that is already in the bot timezone.
function fmtLocal(value) {
  if (!value) return "";
  return `${value.slice(8, 10)}.${value.slice(5, 7)} ${value.slice(11, 16)}`;
}

// localDay turns an ISO timestamp into YYYY-MM-DD in the device timezone, enough to compare with deadline dates.
function localDay(value) {
  const d = new Date(value);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function countBy(list, fn) {
  const out = {};
  for (const x of list) out[fn(x)] = (out[fn(x)] || 0) + 1;
  return out;
}

function card(...children) {
  return h("div", { class: "card" }, ...children);
}

function field(label, input) {
  return h("label", { class: "field" }, h("span", { class: "muted" }, label), input);
}

function contactLine(p) {
  return h("div", { class: "row line" },
    p.username ? h("a", { href: "https://t.me/" + p.username, target: "_blank" }, "@" + p.username) : null,
    p.phone ? h("a", { href: "tel:" + p.phone.replace(/[^\d+]/g, "") }, p.phone) : null,
  );
}

function personLink(p) {
  return h("a", { href: "#", onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); openUser(p.id); } }, p.name);
}

function flags(p) {
  return [
    p.banned ? h("span", { class: "badge alert" }, "заблокирован") : null,
    p.violations ? h("span", { class: "badge flag" }, "🚩 " + p.violations) : null,
  ];
}

function empty(text) {
  return h("div", { class: "empty" }, text);
}

function statBox(n, label) {
  return h("div", { class: "stat" }, h("b", null, n || 0), h("span", { class: "muted" }, label));
}

// ---------- navigation & loading ----------

function setSection(section) {
  view = { section, exchangeId: null, sub: "overview", userId: null, back: null };
  ui.query = "";
  for (const b of document.querySelectorAll(".tab")) b.classList.toggle("active", b.dataset.section === section);
  load();
}

function openExchange(id) {
  view = { section: "exchange", exchangeId: id, sub: "overview", userId: null, back: view.section === "create" ? view.back : { ...view } };
  ui.query = "";
  load();
}

function openCreate() {
  view = { section: "create", exchangeId: null, sub: null, userId: null, back: { ...view } };
  render();
}

function openUser(id) {
  view = { section: "user", exchangeId: null, sub: null, userId: id, back: { ...view } };
  load();
}

function goBack() {
  view = view.back || { section: "exchanges", exchangeId: null, sub: "overview", userId: null, back: null };
  load();
}

async function load() {
  try {
    if (view.section === "exchanges") data.exchanges = await api("/api/exchanges");
    else if (view.section === "exchange") data.exchange = await api("/api/exchanges/" + view.exchangeId);
    else if (view.section === "people") data.people = await api("/api/users");
    else if (view.section === "user") data.user = await api("/api/users/" + view.userId);
    else if (view.section === "texts") data.texts = await api("/api/texts");
    render();
  } catch (e) {
    document.getElementById("subtitle").textContent = "";
    document.getElementById("view").replaceChildren(empty(e.message));
  }
}

function render() {
  const title = document.getElementById("title");
  const subtitle = document.getElementById("subtitle");
  let nodes = [];
  if (view.section === "exchanges") {
    title.textContent = "Обмены";
    subtitle.textContent = data.exchanges.length ? "Всего: " + data.exchanges.length : "Пока ни одного";
    nodes = renderHome();
  } else if (view.section === "create") {
    title.textContent = "Новый обмен";
    subtitle.textContent = "Все поля обязательны";
    nodes = [settingsForm(null)];
  } else if (view.section === "exchange") {
    const ex = data.exchange.exchange;
    title.textContent = ex.settings.title;
    subtitle.textContent = ex.status_title;
    nodes = renderExchange();
  } else if (view.section === "people") {
    title.textContent = "Люди";
    subtitle.textContent = "Зарегистрировано: " + data.people.length;
    nodes = renderPeople();
  } else if (view.section === "texts") {
    title.textContent = "Тексты бота";
    subtitle.textContent = "Изменено: " + data.texts.filter((t) => t.changed).length + " из " + data.texts.length;
    nodes = renderTexts();
  } else if (view.section === "user") {
    title.textContent = data.user.user.name;
    subtitle.textContent = "Карточка участника";
    nodes = renderUser();
  }
  if (view.back) nodes.unshift(h("button", { class: "back", type: "button", onclick: goBack }, "← Назад"));
  document.getElementById("view").replaceChildren(...nodes);
}

// ---------- exchanges home ----------

function renderHome() {
  const all = data.exchanges;
  const active = all.filter((x) => x.active);
  const planned = all.filter((x) => x.status === "draft")
    .sort((a, b) => (a.settings.registration_opens_at || "").localeCompare(b.settings.registration_opens_at || ""));
  const done = all.filter((x) => x.status === "completed");

  const out = [h("button", { class: "btn", type: "button", onclick: openCreate }, "＋ Создать обмен")];
  if (!all.length) {
    out.push(empty("Обменов пока нет. Создайте первый: укажите название, бюджет и даты."));
    return out;
  }
  const section = (label, list, emptyText) => {
    out.push(h("h4", { class: "section-title" }, label));
    if (!list.length) out.push(h("div", { class: "muted pad" }, emptyText));
    out.push(...list.map(exchangeRow));
  };
  section("Активный", active, "Сейчас ни один обмен не идёт.");
  section("Запланированные", planned, "Нет запланированных обменов.");
  if (done.length) section("Завершённые", done, "");
  return out;
}

function exchangeRow(x) {
  const s = x.settings;
  return h("div", { class: "card clickable" + (x.active ? " active-card" : ""), onclick: () => openExchange(x.id) },
    h("div", { class: "row" },
      h("h3", null, s.title),
      h("span", { class: "spacer" }),
      h("span", { class: "badge" + (x.active ? " ordered" : "") }, x.status_title),
    ),
    h("div", { class: "muted" }, `Регистрация ${fmtLocal(s.registration_opens_at)} – ${fmtLocal(s.registration_closes_at)}`),
    h("div", { class: "muted" }, `Отправить до ${fmtDate(s.order_deadline)} · завершение ${fmtDate(s.delivery_by)} · до ${s.budget_max} ${s.currency}`),
    x.families || x.gifts || x.violations ? h("div", { class: "row line" },
      h("span", null, `${x.families} семей · ${x.children} детей`),
      x.gifts ? h("span", { class: "muted" }, `получено ${x.received}/${x.gifts}`) : null,
      x.violations ? h("span", { class: "badge flag" }, "🚩 " + x.violations) : null,
    ) : null,
  );
}

// ---------- exchange ----------

function renderExchange() {
  const d = data.exchange;
  const counts = {
    families: d.families.length,
    gifts: d.assignments.length,
    violations: d.violations.filter((v) => !v.dismissed).length,
  };
  const subtabs = h("div", { class: "chips" }, EXCHANGE_SECTIONS.map(([key, label]) =>
    h("button", {
      class: "chip" + (view.sub === key ? " active" : ""), type: "button",
      onclick: () => { view.sub = key; ui.query = ""; render(); },
    }, label + (key in counts ? " " + counts[key] : ""))));

  const body = { overview: renderOverview, families: renderFamilies, gifts: renderGifts, violations: renderViolations }[view.sub]();
  return [subtabs, ...body];
}

function renderOverview() {
  const d = data.exchange;
  const ex = d.exchange;
  const idx = STEPS.findIndex(([s]) => s === ex.status);
  const kids = d.families.reduce((n, f) => n + f.children.length, 0);
  const out = [card(
    h("div", { class: "steps" }, STEPS.map((_, i) => h("div", { class: "step" + (i <= idx ? " done" : "") }))),
    h("div", { class: "step-labels" }, STEPS.map(([, label]) => h("span", null, label))),
    scheduleLines(ex.settings),
  ), stageCard(d)];

  if (ex.status !== "draft") {
    out.push(card(
      h("div", { class: "stats" },
        statBox(d.families.length, "семей"), statBox(kids, "детей"),
        statBox(d.assignments.filter((a) => a.status === "received").length + " / " + d.assignments.length, "подарков получено"),
        statBox(d.violations.filter((v) => !v.dismissed).length, "нарушений"),
      ),
      d.without_children.length ? h("div", { class: "muted line" }, "Зарегистрировались без детей: " + d.without_children.length) : null,
    ));
  }
  if (ex.status === "registration_open") out.push(inviteCard());
  if (ex.status !== "completed") out.push(settingsForm(ex));
  if (ex.active) out.push(broadcastCard());
  out.push(deleteCard(d));
  return out.filter(Boolean);
}

function deleteCard(d) {
  const ex = d.exchange;
  const notify = h("input", { type: "checkbox", checked: ex.active });
  const kids = d.families.reduce((n, f) => n + f.children.length, 0);
  return card(
    h("h3", null, "Удалить обмен"),
    h("div", { class: "muted" }, "Удалятся все дети, распределение и нарушения этого обмена. Профили взрослых останутся."),
    ex.active ? h("label", { class: "check line" }, notify, " Сообщить участникам, что обмен отменён") : null,
    h("button", {
      class: "btn danger", type: "button",
      onclick: async (ev) => {
        // currentTarget is reset once the click handler yields, so take the button before awaiting the dialog.
        const btn = ev.currentTarget;
        const text = `Удалить «${ex.settings.title}»? Семей: ${d.families.length}, детей: ${kids}, подарков: ${d.assignments.length}. Восстановить будет нельзя.`;
        if (!(await confirmAction(text))) return;
        btn.disabled = true;
        try {
          const res = await api(`/api/exchanges/${ex.id}/delete`, { notify: ex.active && notify.checked });
          haptic("success");
          toast(res.message);
          setSection("exchanges");
        } catch (e) {
          haptic("error");
          toast(e.message, true);
          btn.disabled = false;
        }
      },
    }, "Удалить обмен"),
  );
}

function scheduleLines(s) {
  const line = (label, value) => h("div", { class: "kv" }, h("span", { class: "muted" }, label), h("span", null, value));
  return h("div", { class: "line" },
    line("Регистрация", `${fmtLocal(s.registration_opens_at)} – ${fmtLocal(s.registration_closes_at)}`),
    line("Отправить подарки до", fmtDate(s.order_deadline) || "—"),
    line("Завершение", fmtDate(s.delivery_by) || "—"),
    line("Бюджет", `до ${s.budget_max} ${s.currency}`),
  );
}

function openButton(d, label, secondary) {
  return [
    h("button", {
      class: "btn" + (secondary ? " secondary" : ""), type: "button", disabled: !d.can_open,
      onclick: action("open", "Открыть регистрацию? Новые участники смогут зарегистрироваться по ссылке."),
    }, label),
    d.open_hint ? h("div", { class: "muted line" }, d.open_hint) : null,
  ];
}

function stageCard(d) {
  const ex = d.exchange;
  const s = ex.settings;
  const p = d.preview;
  switch (ex.status) {
    case "draft":
      return card(
        h("h3", null, "Обмен запланирован"),
        h("div", { class: "muted" }, `Регистрацию открываете вы, в любой момент с ${fmtLocal(s.registration_opens_at)} до ${fmtLocal(s.registration_closes_at)}.`),
        openButton(d, "Открыть регистрацию", false),
      );
    case "registration_open":
      return card(
        h("h3", null, "Идёт регистрация"),
        h("div", { class: "muted" }, `Закроется ${fmtLocal(s.registration_closes_at)}, сразу после этого бот распределит детей и разошлёт карточки.`),
        h("button", { class: "btn secondary", type: "button", onclick: action("close", "Закрыть регистрацию сейчас? Новые участники не смогут присоединиться.") }, "Закрыть сейчас"),
      );
    case "registration_closed":
      return card(
        h("h3", null, "Регистрация закрыта"),
        h("div", null, `Семей: ${p.families}, детей: ${p.children}.`),
        p.possible
          ? h("div", { class: "muted line" }, "Распределение делается один раз, всем участникам придут карточки.")
          : h("div", { class: "line danger-text" }, p.reason),
        h("button", {
          class: "btn", type: "button", disabled: !p.possible,
          onclick: action("distribute", "Распределить детей? Отменить будет нельзя, участникам сразу придут карточки."),
        }, "Распределить детей"),
        openButton(d, "Открыть регистрацию снова", true),
      );
    case "distributed": {
      const total = d.assignments.length;
      const c = countBy(d.assignments, (a) => a.status);
      const done = c.received || 0;
      return card(
        h("h3", null, `Получено ${done} из ${total}`),
        h("div", { class: "progress" }, h("div", { style: `width: ${total ? (done * 100) / total : 0}%` })),
        h("div", { class: "stats" },
          statBox(c.assigned, "не отправлено"), statBox(c.ordered, "отправлено"),
          statBox(c.arrived, "в ПВЗ"), statBox(d.assignments.filter(isProblem).length, "требуют внимания"),
        ),
        h("button", {
          class: "btn secondary", type: "button",
          onclick: (ev) => run("/api/remind", {}, "Напомнить всем, кто ещё не отправил или не забрал посылку?", ev.currentTarget),
        }, "Напомнить отстающим"),
        h("button", { class: "btn danger", type: "button", onclick: action("finish", "Завершить обмен сейчас? Неполученные подарки запишутся в нарушения.") }, "Завершить сейчас"),
      );
    }
    case "completed":
      return card(h("h3", null, "Обмен завершён"), h("div", { class: "muted" }, "Он остаётся в списке обменов вместе с распределением и нарушениями."));
  }
  return null;
}

function inviteText(s) {
  return [
    `Приглашаем детей в обмен подарками «${s.title}» 🎁`,
    `Каждая семья дарит подарки детям из других семей: бот подскажет, кому и на какой ПВЗ Wildberries заказать. Бюджет подарка до ${s.budget_max} ${s.currency}.`,
    "Регистрация до " + fmtLocal(s.registration_closes_at) + ".",
    "Отправить подарок нужно до " + fmtDate(s.order_deadline) + ", все подарки дети получат до " + fmtDate(s.delivery_by) + ".",
    "Чтобы участвовать, откройте бота по ссылке и зарегистрируйтесь:",
  ].join("\n");
}

function inviteCard() {
  const d = data.exchange;
  const text = h("textarea", null, inviteText(d.exchange.settings));
  const input = h("input", { value: d.invite, readonly: true });
  return card(
    h("h3", null, "Приглашение"),
    h("div", { class: "muted" }, "Текст можно поправить, ссылка добавится сама. Откроется выбор чатов Telegram."),
    h("div", { class: "line" }, text),
    h("button", {
      class: "btn", type: "button",
      onclick: () => {
        const value = text.value.trim();
        if (!value) return toast("Введите текст приглашения", true);
        // The bot cannot message people who never started it, so invitations go from the admin's account.
        const link = "https://t.me/share/url?url=" + encodeURIComponent(d.invite) + "&text=" + encodeURIComponent(value);
        if (tg && tg.openTelegramLink) tg.openTelegramLink(link);
        else window.open(link, "_blank");
      },
    }, "Отправить приглашение"),
    h("div", { class: "line" }, input),
    h("button", {
      class: "btn small secondary", type: "button",
      onclick: async () => {
        try {
          await navigator.clipboard.writeText(d.invite);
          toast("Ссылка скопирована");
        } catch {
          input.select();
          toast("Выделите ссылку и скопируйте");
        }
      },
    }, "Скопировать только ссылку"),
  );
}

// settingsForm edits an exchange, or creates one when ex is null. A new exchange starts with empty fields.
function settingsForm(ex) {
  const s = ex ? ex.settings : {};
  const regLocked = !!ex && !["draft", "registration_open"].includes(ex.status);
  const title = h("input", { type: "text", maxlength: "100", value: s.title || "", placeholder: "Например, Новый год 2026" });
  const budget = h("input", { type: "number", min: "1", max: "100000", step: "1", inputmode: "numeric", value: s.budget_max || "", placeholder: "25" });
  const currency = h("input", { type: "text", maxlength: "10", value: s.currency || "", placeholder: "BYN" });
  const opens = h("input", { type: "datetime-local", value: s.registration_opens_at || "", disabled: regLocked || (!!ex && ex.status === "registration_open") });
  const closes = h("input", { type: "datetime-local", value: s.registration_closes_at || "", disabled: regLocked });
  const order = h("input", { type: "date", value: s.order_deadline || "" });
  const delivery = h("input", { type: "date", value: s.delivery_by || "" });

  const submit = async (ev) => {
    const btn = ev.currentTarget;
    const body = {
      title: title.value.trim(), budget_max: Number(budget.value), currency: currency.value.trim(),
      registration_opens_at: opens.value, registration_closes_at: closes.value,
      order_deadline: order.value, delivery_by: delivery.value,
    };
    if (!body.title || !body.budget_max || !body.currency || !opens.value || !closes.value || !order.value || !delivery.value) {
      return toast("Заполните все поля", true);
    }
    if (ex) return run(`/api/exchanges/${ex.id}/settings`, body, null, btn);
    btn.disabled = true;
    try {
      const res = await api("/api/exchanges", body);
      haptic("success");
      toast(res.message);
      openExchange(res.id);
    } catch (e) {
      haptic("error");
      toast(e.message, true);
      btn.disabled = false;
    }
  };

  return card(
    ex ? h("h3", null, "Условия и расписание") : null,
    field("Название", title),
    h("div", { class: "field-row" }, field("Бюджет подарка, до", budget), field("Валюта", currency)),
    h("div", { class: "field-row even" }, field("Регистрация с", opens), field("Регистрация до", closes)),
    h("div", { class: "muted" }, regLocked
      ? "Регистрация уже прошла, её даты не меняются."
      : "Регистрацию открываете вы кнопкой в эти даты. В конце регистрации бот сам её закроет и распределит детей."),
    h("div", { class: "field-row even" }, field("Отправить подарки до", order), field("Завершение обмена", delivery)),
    h("div", { class: "muted" }, "После срока отправки бот запишет в нарушения тех, кто не отправил подарок. После дня завершения обмен закроется, недоставленные подарки тоже попадут в нарушения."),
    ex && ex.status === "distributed"
      ? h("div", { class: "muted line" }, "Карточки уже разосланы: новые условия попадут только в следующие сообщения.")
      : null,
    h("button", { class: "btn" + (ex ? " secondary" : ""), type: "button", onclick: submit }, ex ? "Сохранить" : "Создать обмен"),
  );
}

function broadcastCard() {
  const text = h("textarea", { placeholder: "Сообщение всем зарегистрированным участникам" });
  return card(
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

function searchBox(placeholder, onChange) {
  return card(h("input", {
    type: "search", placeholder, value: ui.query,
    oninput: (ev) => { ui.query = ev.target.value; onChange(); },
  }));
}

function matches(values) {
  const q = ui.query.trim().toLowerCase();
  return !q || JSON.stringify(values).toLowerCase().includes(q);
}

function kidLine(c) {
  return h("div", { class: "kid" },
    h("div", null, `${c.name}, ${ageText(c.age)}, ${c.gender === "girl" ? "девочка" : "мальчик"}`),
    c.wishes ? h("div", { class: "muted" }, "Пожелания: " + c.wishes) : null,
  );
}

function renderFamilies() {
  const d = data.exchange;
  const list = h("div", null);
  const fill = () => {
    const items = d.families.filter((f) => matches([f.name, f.username, f.phone, f.children.map((c) => c.name)]));
    list.replaceChildren(...(items.length ? items.map(familyCard) : [empty(ui.query ? "Никого не нашли" : "Пока никто не зарегистрировался")]));
  };
  fill();
  const out = [searchBox("Поиск по имени, телефону, ребёнку", fill), list];
  if (d.without_children && d.without_children.length) {
    out.push(card(
      h("h3", null, "Зарегистрировались, но не добавили детей"),
      d.without_children.map((p) => h("div", { class: "kid" }, personLink(p), contactLine(p))),
    ));
  }
  return out;
}

function familyCard(f) {
  return card(
    h("div", { class: "row" },
      h("h3", null, personLink(f)),
      h("span", { class: "spacer" }),
      flags(f),
      data.exchange.assignments.length ? h("span", { class: "badge" }, "дарит " + f.gives) : null,
    ),
    contactLine(f),
    h("div", { class: "muted line" }, "ПВЗ: " + f.pvz),
    h("div", { class: "line" }, f.children.map(kidLine)),
  );
}

function orderedLate(a) {
  const deadline = data.exchange.exchange.settings.order_deadline;
  return !!deadline && !!a.ordered_at && localDay(a.ordered_at) > deadline;
}

function isProblem(a) {
  const today = localDay(new Date().toISOString());
  return a.stuck || a.code_pending || orderedLate(a)
    || (a.status === "ordered" && !!a.expected_date && a.expected_date < today);
}

function renderGifts() {
  const d = data.exchange;
  if (!d.assignments.length) return [empty("Распределения ещё не было")];
  const counts = countBy(d.assignments, (a) => a.status);
  counts.all = d.assignments.length;
  counts.problems = d.assignments.filter(isProblem).length;

  const chips = h("div", { class: "chips" }, GIFT_FILTERS.map(([key, label]) =>
    h("button", {
      class: "chip" + (ui.giftFilter === key ? " active" : ""), type: "button",
      onclick: () => { ui.giftFilter = key; render(); },
    }, `${label} ${counts[key] || 0}`)));

  const list = d.assignments.filter((a) =>
    ui.giftFilter === "all" || (ui.giftFilter === "problems" ? isProblem(a) : a.status === ui.giftFilter));
  return [chips, ...(list.length ? list.map(giftCard) : [empty("Здесь пусто")])];
}

function giftCard(a) {
  const notes = [];
  if (a.ordered_at) notes.push("отправлен " + fmtDate(a.ordered_at) + (orderedLate(a) ? " (с опозданием)" : ""));
  if (a.status === "ordered" && a.expected_date) notes.push("ожидается " + fmtDate(a.expected_date));
  if (a.arrived_at) notes.push("в ПВЗ с " + fmtDate(a.arrived_at));
  if (a.status === "arrived") {
    if (a.code_valid) notes.push("код на сегодня отправлен");
    if (a.code_pending) notes.push("получатель ждёт код");
  }
  if (a.received_at) notes.push("получен " + fmtDate(a.received_at));

  const c = card(
    h("div", { class: "row" },
      h("h3", null, `${a.child.name}, ${ageText(a.child.age)}`),
      h("span", { class: "spacer" }),
      h("span", { class: "badge " + a.status }, GIFT_STATUS[a.status] || a.status),
      isProblem(a) ? h("span", { class: "badge alert" }, "!") : null,
    ),
    notes.length ? h("div", { class: "muted" }, notes.join(" · ")) : null,
    h("div", { class: "line" }, h("span", { class: "muted" }, "Получатель: "), personLink(a.parent)),
    contactLine(a.parent),
    h("div", { class: "line" }, h("span", { class: "muted" }, "Даритель: "), personLink(a.giver)),
    contactLine(a.giver),
  );
  const ex = data.exchange.exchange;
  if (ex.active && ex.status === "distributed" && a.status !== "received") c.append(reassignControl(a));
  return c;
}

function reassignControl(a) {
  const wrap = h("div", null);
  wrap.append(h("button", {
    class: "btn small secondary", type: "button",
    onclick: () => {
      const candidates = data.exchange.families.filter((f) => f.id !== a.parent.id && f.id !== a.giver.id && !f.banned);
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
  }, "Передать другому дарителю"));
  return wrap;
}

function violationCard(v, withExchange) {
  return card(
    h("div", { class: "row" },
      h("h3", null, VIOLATION_KIND[v.kind] || v.kind),
      h("span", { class: "spacer" }),
      v.dismissed ? h("span", { class: "badge" }, "снято") : h("span", { class: "badge flag" }, "🚩"),
    ),
    h("div", { class: "line" }, personLink({ id: v.user_id, name: v.user_name }),
      v.child_name ? h("span", { class: "muted" }, " · подарок для " + v.child_name) : null),
    withExchange ? h("div", { class: "muted" }, v.exchange_title) : null,
    v.comment ? h("div", { class: "line" }, v.comment) : null,
    h("div", { class: "muted line" }, "Записано " + fmtDate(v.created_at)),
    h("button", {
      class: "btn small secondary", type: "button",
      onclick: (ev) => run(`/api/violations/${v.id}/dismiss`, { dismissed: !v.dismissed },
        v.dismissed ? "Вернуть нарушение?" : "Снять нарушение? Оно перестанет учитываться в красных флагах.", ev.currentTarget),
    }, v.dismissed ? "Вернуть" : "Снять"),
  );
}

function renderViolations() {
  const d = data.exchange;
  const select = h("select", null,
    h("option", { value: "" }, "Участник"),
    d.families.map((f) => h("option", { value: f.id }, f.name)));
  const comment = h("textarea", { placeholder: "Что случилось: например, подарок не по бюджету" });
  const out = [card(
    h("h3", null, "Добавить нарушение"),
    select,
    h("div", { class: "line" }, comment),
    h("button", {
      class: "btn secondary", type: "button",
      onclick: (ev) => {
        if (!select.value) return toast("Выберите участника", true);
        if (!comment.value.trim()) return toast("Опишите нарушение", true);
        run("/api/violations", { exchange_id: d.exchange.id, user_id: Number(select.value), comment: comment.value.trim() }, null, ev.currentTarget);
      },
    }, "Добавить"),
  )];
  if (!d.violations.length) {
    out.push(empty(["distributed", "completed"].includes(d.exchange.status)
      ? "Нарушений нет 🎉" : "Нарушения появятся после срока отправки подарков"));
  }
  return out.concat(d.violations.map((v) => violationCard(v, false)));
}

// ---------- people ----------

function filteredPeople() {
  return data.people.filter((u) =>
    (ui.peopleFilter === "all" || (ui.peopleFilter === "flag" ? u.violations > 0 : u.banned))
    && matches([u.name, u.username, u.phone]));
}

function renderPeople() {
  const chips = h("div", { class: "chips" },
    PEOPLE_FILTERS.map(([key, label]) =>
      h("button", {
        class: "chip" + (ui.peopleFilter === key ? " active" : ""), type: "button",
        onclick: () => { ui.peopleFilter = key; render(); },
      }, label)),
    h("button", {
      class: "chip" + (ui.selecting ? " active" : ""), type: "button",
      onclick: () => { ui.selecting = !ui.selecting; ui.selected.clear(); render(); },
    }, ui.selecting ? "✕ Отменить выбор" : "☑ Выбрать"),
  );
  const list = h("div", null);
  const fill = () => {
    const items = filteredPeople();
    list.replaceChildren(...(items.length ? items.map(personRow) : [empty("Никого не нашли")]));
  };
  fill();
  return [chips, ui.selecting ? bulkCard() : null, searchBox("Поиск по имени, телефону, @username", fill), list].filter(Boolean);
}

function bulkCard() {
  const ids = [...ui.selected];
  const text = h("textarea", { placeholder: "Сообщение выбранным: придёт от бота" });
  const reason = h("input", { type: "text", maxlength: "500", placeholder: "Причина блокировки" });
  const need = () => { if (!ids.length) { toast("Сначала отметьте людей", true); return false; } return true; };
  return card(
    h("div", { class: "row" },
      h("h3", null, "Выбрано: " + ids.length),
      h("span", { class: "spacer" }),
      h("button", { class: "btn small secondary", type: "button", onclick: () => { filteredPeople().forEach((u) => ui.selected.add(u.id)); render(); } }, "Всех найденных"),
      h("button", { class: "btn small secondary", type: "button", onclick: () => { ui.selected.clear(); render(); } }, "Снять"),
    ),
    h("div", { class: "line" }, text),
    h("button", {
      class: "btn", type: "button",
      onclick: (ev) => {
        if (!need()) return;
        if (!text.value.trim()) return toast("Введите текст сообщения", true);
        run("/api/messages", { user_ids: ids, text: text.value.trim() }, `Отправить сообщение ${ids.length} получателям?`, ev.currentTarget)
          .then((ok) => { if (ok) { ui.selecting = false; ui.selected.clear(); render(); } });
      },
    }, "Написать выбранным"),
    h("div", { class: "line" }, reason),
    h("div", { class: "field-row even" },
      h("button", {
        class: "btn danger", type: "button",
        onclick: (ev) => {
          if (!need()) return;
          if (!reason.value.trim()) return toast("Укажите причину блокировки", true);
          run("/api/users/ban", { user_ids: ids, banned: true, reason: reason.value.trim() },
            `Заблокировать ${ids.length}? Они не смогут добавлять детей и не попадут в распределение.`, ev.currentTarget);
        },
      }, "Заблокировать"),
      h("button", {
        class: "btn secondary", type: "button",
        onclick: (ev) => {
          if (!need()) return;
          run("/api/users/ban", { user_ids: ids, banned: false }, `Разблокировать ${ids.length}?`, ev.currentTarget);
        },
      }, "Разблокировать"),
    ),
  );
}

function personRow(u) {
  const selected = ui.selected.has(u.id);
  return h("div", {
    class: "card clickable" + (u.red_flag ? " flagged" : "") + (selected ? " selected" : ""),
    onclick: () => {
      if (!ui.selecting) return openUser(u.id);
      if (selected) ui.selected.delete(u.id); else ui.selected.add(u.id);
      render();
    },
  },
  h("div", { class: "row" },
    ui.selecting ? h("span", { class: "checkmark" }, selected ? "☑" : "☐") : null,
    h("h3", null, u.name), h("span", { class: "spacer" }), flags(u)),
  h("div", { class: "muted" }, `обменов: ${u.exchanges} · подарил: ${u.gifts_given} · нарушений: ${u.violations}`),
  contactLine(u),
  );
}

function banControls(u) {
  if (u.banned) {
    return [
      h("div", { class: "line danger-text" }, "Заблокирован: " + u.ban_reason),
      h("button", {
        class: "btn secondary", type: "button",
        onclick: (ev) => run("/api/users/ban", { user_ids: [u.id], banned: false }, "Разблокировать? Человек снова сможет участвовать.", ev.currentTarget),
      }, "Разблокировать"),
    ];
  }
  const reason = h("textarea", { placeholder: "Причина блокировки" });
  return [
    h("div", { class: "line" }, reason),
    h("button", {
      class: "btn danger", type: "button",
      onclick: (ev) => {
        if (!reason.value.trim()) return toast("Укажите причину", true);
        run("/api/users/ban", { user_ids: [u.id], banned: true, reason: reason.value.trim() },
          "Заблокировать? Человек не сможет добавлять детей и не попадёт в следующие распределения.", ev.currentTarget);
      },
    }, "Заблокировать"),
  ];
}

function messageCard(u) {
  const text = h("textarea", { placeholder: "Сообщение придёт от бота" });
  return card(
    h("h3", null, "Написать"),
    text,
    h("button", {
      class: "btn secondary", type: "button",
      onclick: (ev) => {
        if (!text.value.trim()) return toast("Введите текст сообщения", true);
        run("/api/messages", { user_ids: [u.id], text: text.value.trim() }, null, ev.currentTarget)
          .then((ok) => { if (ok) text.value = ""; });
      },
    }, "Отправить"),
  );
}

function renderUser() {
  const d = data.user;
  const u = d.user;
  const out = [card(
    h("div", { class: "row" }, h("h3", null, u.name), h("span", { class: "spacer" }), flags(u)),
    contactLine(u),
    h("div", { class: "muted line" }, "ПВЗ: " + (u.pvz || "—")),
    h("div", { class: "muted" }, `Зарегистрирован ${fmtDate(u.created_at)} · обменов: ${u.exchanges} · подарил: ${u.gifts_given}`),
    banControls(u),
  ), messageCard(u)];

  if (d.violations.length) {
    out.push(h("h4", { class: "section-title" }, "Нарушения"));
    out.push(...d.violations.map((v) => violationCard(v, true)));
  }

  out.push(h("h4", { class: "section-title" }, "Участие в обменах"));
  if (!d.exchanges.length) out.push(empty("Ещё не участвовал"));
  for (const e of d.exchanges) {
    out.push(h("div", { class: "card clickable", onclick: () => openExchange(e.exchange_id) },
      h("div", { class: "row" }, h("h3", null, e.title), h("span", { class: "spacer" }), h("span", { class: "badge" }, e.status_title)),
      e.children.length ? h("div", { class: "line" }, h("span", { class: "muted" }, "Дети: "), e.children.map((c) => c.name).join(", ")) : null,
      e.gave.length ? h("div", { class: "line" }, h("span", { class: "muted" }, "Дарил: "),
        e.gave.map((a) => `${a.child.name} (${GIFT_STATUS[a.status]}${a.ordered_at ? ", отправлен " + fmtDate(a.ordered_at) : ""})`).join("; ")) : null,
      e.received.length ? h("div", { class: "line" }, h("span", { class: "muted" }, "Его детям: "),
        e.received.map((a) => `${a.child.name} — ${GIFT_STATUS[a.status]}`).join("; ")) : null,
    ));
  }
  return out;
}

// ---------- texts ----------

function renderTexts() {
  const help = card(
    h("h3", null, "Как редактировать"),
    h("div", { class: "muted" }, "Это всё, что бот пишет участникам. **текст** выделяется жирным, `текст` моноширинным. Подстановки в фигурных скобках бот заменит сам, у каждого текста свой список. Кнопки меню должны отличаться друг от друга."),
  );
  const chips = h("div", { class: "chips" },
    h("button", {
      class: "chip" + (ui.changedOnly ? " active" : ""), type: "button",
      onclick: () => { ui.changedOnly = !ui.changedOnly; render(); },
    }, "Только изменённые"));
  const list = h("div", null);
  const fill = () => {
    const items = data.texts.filter((t) => (!ui.changedOnly || t.changed) && matches([t.title, t.value, t.group]));
    const nodes = [];
    let group = "";
    for (const t of items) {
      if (t.group !== group) {
        group = t.group;
        nodes.push(h("h4", { class: "section-title" }, group));
      }
      nodes.push(textCard(t));
    }
    list.replaceChildren(...(nodes.length ? nodes : [empty(ui.changedOnly ? "Изменённых текстов нет" : "Ничего не нашли")]));
  };
  fill();
  return [help, chips, searchBox("Поиск по названию или тексту", fill), list];
}

function textCard(t) {
  const input = t.button
    ? h("input", { type: "text", maxlength: "64", value: t.value })
    : h("textarea", { class: "text-edit" }, t.value);
  const key = encodeURIComponent(t.key);
  return card(
    h("div", { class: "row" }, h("h3", null, t.title), h("span", { class: "spacer" }), t.changed ? h("span", { class: "badge ordered" }, "изменён") : null),
    t.vars.length ? h("div", { class: "muted" }, "Подстановки: " + t.vars.map((v) => "{" + v + "}").join(" ")) : null,
    h("div", { class: "line" }, input),
    t.changed ? h("div", { class: "muted line" }, "Исходный: " + t.default) : null,
    h("div", { class: "row" },
      h("button", {
        class: "btn small", type: "button",
        onclick: (ev) => run(`/api/texts/${key}`, { value: input.value }, null, ev.currentTarget),
      }, "Сохранить"),
      t.changed ? h("button", {
        class: "btn small secondary", type: "button",
        onclick: (ev) => run(`/api/texts/${key}/reset`, {}, "Вернуть исходный текст?", ev.currentTarget),
      }, "Вернуть исходный") : null,
    ),
  );
}

// ---------- boot ----------

for (const btn of document.querySelectorAll(".tab")) {
  btn.addEventListener("click", () => setSection(btn.dataset.section));
}
document.getElementById("refresh").addEventListener("click", load);

load();
