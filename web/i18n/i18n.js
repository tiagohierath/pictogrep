(function () {
  "use strict";

  const fallbackLocale = "en";
  let locale = fallbackLocale;
  let fallback = {};
  let messages = {};

  function supportedLocale(value) {
    if (value === "pt-BR" || String(value || "").toLowerCase().startsWith("pt")) return "pt-BR";
    return "en";
  }

  async function load(value) {
    const response = await fetch(`/i18n/locales/${encodeURIComponent(value)}.json`);
    if (!response.ok) throw new Error(`Could not load locale ${value}`);
    return response.json();
  }

  async function init(configuredLocale) {
    locale = supportedLocale(configuredLocale || navigator.language || fallbackLocale);
    fallback = await load(fallbackLocale);
    messages = locale === fallbackLocale ? fallback : {...fallback, ...await load(locale)};
    apply();
    return locale;
  }

  function t(key, values = {}) {
    const template = messages[key] ?? fallback[key] ?? key;
    return String(template).replace(/\{(\w+)\}/g, (_, name) => values[name] ?? `{${name}}`);
  }

  function apply(root = document) {
    root.querySelectorAll("[data-i18n]").forEach(element => { element.textContent = t(element.dataset.i18n); });
    const properties = {placeholder: "i18nPlaceholder", "aria-label": "i18nAriaLabel", title: "i18nTitle"};
    for (const attribute of Object.keys(properties)) {
      root.querySelectorAll(`[data-i18n-${attribute}]`).forEach(element => {
        element.setAttribute(attribute, t(element.dataset[properties[attribute]]));
      });
    }
    document.documentElement.lang = locale;
  }

  window.PictogrepI18n = {init, t, apply, locale: () => locale};
})();
