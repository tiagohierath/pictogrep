// Pictogrep onboarding.
//
// The flow is the `steps` array at the bottom of this file. Each step is one
// object with an `id` and a `render(ctx)` returning the element to show. Adding,
// removing, or reordering screens means editing that array and nothing else:
// the shell, the close button, Escape, and the step dots are all driven from it.
// There is one step today, so the dots stay hidden until a second one exists.
//
// A step reaches the rest of the application only through `ctx.app`, the small
// bridge app.js publishes as window.PictogrepApp, so nothing here depends on
// library internals. All copy lives in web/i18n/locales/*.json under
// `onboarding.`.

window.PictogrepOnboarding = (function () {
  "use strict";

  const t = (key, values) => window.PictogrepI18n.t(key, values);
  const app = () => window.PictogrepApp || {};

  function el(tag, props = {}, children = []) {
    const node = document.createElement(tag);
    Object.entries(props).forEach(([key, value]) => {
      if (value === null || value === undefined) return;
      if (key === "class") node.className = value;
      else if (key === "text") node.textContent = value;
      else if (key.startsWith("on")) node[key] = value;
      else node.setAttribute(key, value);
    });
    (Array.isArray(children) ? children : [children]).filter(Boolean).forEach(child => node.append(child));
    return node;
  }

  let index = 0;
  let running = false;
  let bound = false;

  const dialog = () => document.querySelector("#onboardingDialog");

  const ctx = {
    el, t,
    get app() { return app(); },
    next: () => go(index + 1),
    go: target => go(target),
    finish: () => finish(),
    close: () => close(),
  };

  // Language comes first, before any of the copy has to be understood. Picking
  // one here is the same choice as the one in Settings, so the whole interface
  // follows and stays that way after onboarding closes.
  const languages = [{code: "en", label: "EN"}, {code: "pt-BR", label: "PT"}];

  function renderLanguages() {
    const host = document.querySelector("#onboardingLangs");
    if (!host) return;
    const active = window.PictogrepI18n.locale();
    host.replaceChildren(...languages.map(language => el("button", {
      type: "button",
      class: language.code === active ? "is-current" : null,
      text: language.label,
      lang: language.code,
      "aria-pressed": String(language.code === active),
      onclick: async () => {
        if (language.code === active) return;
        await app().setLanguage?.(language.code);
        renderLanguages();
        go(index);
      },
    })));
  }

  function renderDots() {
    const dots = document.querySelector("#onboardingDots");
    if (!dots) return;
    dots.hidden = steps.length < 2;
    if (dots.hidden) return;
    dots.replaceChildren(...steps.map((step, position) => el("i", {class: position === index ? "is-current" : null})));
    dots.setAttribute("aria-label", t("onboarding.progress", {current: index + 1, total: steps.length}));
  }

  function go(target) {
    const position = typeof target === "string" ? steps.findIndex(step => step.id === target) : target;
    if (position < 0 || position >= steps.length) return finish();
    index = position;
    const body = document.querySelector("#onboardingBody");
    if (!body) return;
    body.replaceChildren(steps[index].render(ctx));
    body.scrollTop = 0;
    renderDots();
    body.querySelector("[data-autofocus]")?.focus();
  }

  // Escape, the close button, and finishing all land here. Pictogrep will not
  // ask again, and a folder scan the flow already started keeps running in the
  // background exactly as it would have.
  function teardown() {
    if (!running) return;
    running = false;
    document.body.classList.remove("onboarding-open");
    Promise.resolve(app().completeOnboarding?.()).catch(() => {});
  }

  function close() {
    dialog()?.close();
  }

  function finish() {
    close();
    app().enterLibrary?.();
  }

  function start() {
    const host = dialog();
    if (!host || running) return;
    if (!bound) {
      bound = true;
      host.addEventListener("close", teardown);
      document.querySelector("#onboardingClose")?.addEventListener("click", close);
    }
    running = true;
    document.body.classList.add("onboarding-open");
    host.showModal();
    renderLanguages();
    go(0);
  }

  // ---------------------------------------------------------------------
  // Where the pictures are.
  // ---------------------------------------------------------------------

  function choice(label, badge, onclick) {
    return el("button", {type: "button", class: "onboarding-choice", onclick}, [
      el("span", {text: label}),
      badge ? el("span", {class: "onboarding-badge", text: badge}) : null,
    ]);
  }

  function renderSourceStep(context) {
    const wrap = el("div", {class: "onboarding-step"});
    wrap.append(
      el("h2", {class: "onboarding-title", text: t("onboarding.source.title")}),
      el("div", {class: "onboarding-choices"}, [
        choice(t("onboarding.source.folder"), t("onboarding.recommended"), () => renderFolderPicker(context, wrap)),
        choice(t("onboarding.source.pinterest"), null, () => {
          context.close();
          context.app.startPinterest?.();
        }),
      ]),
      el("p", {class: "onboarding-creds", text: t("onboarding.source.privacy")}),
    );
    return wrap;
  }

  // The folder browser itself lives in app.js, because the Add drawer offers the
  // same picker and Pictogrep should only have one of them. This step just gives
  // it somewhere to draw and says what to do with the answer.
  function renderFolderPicker(context, wrap) {
    wrap.replaceChildren(el("h2", {class: "onboarding-title", text: t("onboarding.folder.title")}));
    context.app.renderFolderBrowser(wrap, {
      onChoose: async folder => {
        await context.app.indexFolder(folder);
        context.finish();
      },
    });
  }

  // ---------------------------------------------------------------------
  // The flow. Edit this array to change the onboarding.
  // ---------------------------------------------------------------------

  const steps = [
    {id: "source", render: renderSourceStep},
  ];

  return {start, close, go, steps, isRunning: () => running};
})();
