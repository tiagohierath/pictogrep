/* The Material 3 furniture, built on the phone only.

   m3.css restyles what is already in the page. Three Material components have
   no counterpart in a desktop layout and have to be built: the navigation bar
   at the bottom, the floating action button, and the ripple every Material
   control answers a touch with.

   Nothing here reimplements any behaviour. The navigation bar presses the tab
   buttons that were already there and mirrors their selected state, so search,
   folders and every plugin keep working through exactly the code they always
   did. If this file fails to load, the app is the old layout and still works.

   Loaded on every build and does nothing unless the server stamped is-phone on
   the document, which it does only in the Android build. */

(function () {
  "use strict";

  if (!document.documentElement.classList.contains("is-phone")) return;

  // Material Symbols as paths rather than a font, because a font is a few
  // hundred kilobytes to draw six shapes.
  const ICONS = {
    pictures: "M22 16V4c0-1.1-.9-2-2-2H8c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2zm-11-4l2.03 2.71L16 11l4 5H8l3-4zM2 6v14c0 1.1.9 2 2 2h14v-2H4V6H2z",
    folders: "M10 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z",
    boards: "M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z",
    commons: "M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zm6.93 6h-2.95a15.65 15.65 0 0 0-1.38-3.56A8.03 8.03 0 0 1 18.92 8zM12 4.04c.83 1.2 1.48 2.53 1.91 3.96h-3.82c.43-1.43 1.08-2.76 1.91-3.96zM4.26 14C4.1 13.36 4 12.69 4 12s.1-1.36.26-2h3.38c-.08.66-.14 1.32-.14 2s.06 1.34.14 2H4.26zm.82 2h2.95c.32 1.25.78 2.45 1.38 3.56A7.987 7.987 0 0 1 5.08 16zm2.95-8H5.08a7.987 7.987 0 0 1 4.33-3.56A15.65 15.65 0 0 0 8.03 8zM12 19.96c-.83-1.2-1.48-2.53-1.91-3.96h3.82c-.43 1.43-1.08 2.76-1.91 3.96zM14.34 14H9.66c-.09-.66-.16-1.32-.16-2s.07-1.35.16-2h4.68c.09.65.16 1.32.16 2s-.07 1.34-.16 2zm.25 5.56c.6-1.11 1.06-2.31 1.38-3.56h2.95a8.03 8.03 0 0 1-4.33 3.56zM16.36 14c.08-.66.14-1.32.14-2s-.06-1.34-.14-2h3.38c.16.64.26 1.31.26 2s-.1 1.36-.26 2h-3.38z",
    calendar: "M19 3h-1V1h-2v2H8V1H6v2H5c-1.11 0-1.99.9-1.99 2L3 19a2 2 0 0 0 2 2h14c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2zm0 16H5V8h14v11zM7 10h5v5H7z",
    add: "M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z",
  };

  function icon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("fill", "currentColor");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", ICONS[name]);
    svg.append(path);
    return svg;
  }

  const $ = selector => document.querySelector(selector);

  // --- ripple ------------------------------------------------------------

  // Everything that can be pressed, named once. The state layer under these is
  // pure CSS, so this list exists only to find the element a touch landed on.
  const PRESSABLE = "button, .m3-nav-item, .drawer-actions a, .card-menu a, .card-menu button";

  // A Material control answers a touch where it was touched. This is the whole
  // of that: a circle that grows from the point and fades.
  function rippleFrom(event) {
    const target = event.target.closest(PRESSABLE);
    if (!target) return;
    const box = target.getBoundingClientRect();
    const point = event.touches ? event.touches[0] : event;
    const size = Math.max(box.width, box.height) * 2;
    const ripple = document.createElement("span");
    ripple.className = "m3-ripple";
    ripple.style.width = ripple.style.height = `${size}px`;
    ripple.style.left = `${(point.clientX ?? box.left + box.width / 2) - box.left - size / 2}px`;
    ripple.style.top = `${(point.clientY ?? box.top + box.height / 2) - box.top - size / 2}px`;
    target.append(ripple);
    setTimeout(() => ripple.remove(), 450);
  }

  // Delegated, so a control added later ripples without anything being told
  // about it. An earlier version tagged every pressable element with a class
  // and watched the whole body for new ones, which meant running a selector
  // over every one of hundreds of picture cards as the grid filled in. The
  // grid is the one thing on this screen that has to stay fast.
  document.addEventListener("pointerdown", rippleFrom, {passive: true});

  // --- top app bar -------------------------------------------------------

  const header = $(".app-header");
  const lift = () => header?.classList.toggle("is-lifted", window.scrollY > 4);
  document.addEventListener("scroll", lift, {passive: true});
  lift();

  // --- navigation bar ----------------------------------------------------

  // The destinations are the tab buttons the app already decided to show, so a
  // plugin turning its tab on puts it here too, and nothing has to know the
  // list twice. Material allows five.
  const DESTINATIONS = [
    {tab: "#imagesTab", icon: "pictures"},
    {tab: "#commonsTab", icon: "commons"},
    {tab: "#calendarTab", icon: "calendar"},
    {tab: "#foldersTab", icon: "folders"},
  ];

  const nav = document.createElement("nav");
  nav.className = "m3-nav";
  document.body.append(nav);

  function label(tab) {
    // The tab holds its name and, for pictures, a count. Only the name belongs
    // on a 64px wide destination.
    const named = tab.querySelector("[data-i18n]");
    return (named || tab).textContent.trim();
  }

  function buildNav() {
    const shown = DESTINATIONS.filter(({tab}) => $(tab) && !$(tab).hidden);
    nav.replaceChildren();

    shown.forEach(({tab, icon: name}) => {
      const button = $(tab);
      const item = document.createElement("button");
      item.type = "button";
      item.className = "m3-nav-item";
      item.dataset.for = tab;
      const bed = document.createElement("span");
      bed.className = "m3-nav-icon";
      bed.append(icon(name));
      const text = document.createElement("span");
      text.className = "m3-nav-label";
      text.textContent = label(button);
      item.append(bed, text);
      item.onclick = () => button.click();
      nav.append(item);
    });

    // Storyboards is a page rather than a tab, and it is the other half of
    // what Pictogrep does, so it belongs down here rather than three taps deep
    // in the menu. Only when there is room inside Material's five.
    if (shown.length < 5) {
      const link = document.createElement("a");
      link.className = "m3-nav-item";
      link.href = "/practice";
      const bed = document.createElement("span");
      bed.className = "m3-nav-icon";
      bed.append(icon("boards"));
      const text = document.createElement("span");
      text.className = "m3-nav-label";
      text.textContent = $("[data-i18n='menu.open_storyboard']")?.textContent.trim() || "Storyboard";
      link.append(bed, text);
      nav.append(link);
    }

    syncNav();
  }

  /** The tab buttons stay the truth about which panel is open. */
  function syncNav() {
    nav.querySelectorAll("[data-for]").forEach(item => {
      const tab = $(item.dataset.for);
      const open = tab?.getAttribute("aria-selected") === "true";
      open ? item.setAttribute("aria-current", "page") : item.removeAttribute("aria-current");
    });
  }

  buildNav();

  /*
   * The tab strip stays the single source of truth, so it is watched rather
   * than second-guessed. Three different things happen to it and all three
   * matter here: a plugin turns a tab on or off, switching panels rewrites
   * aria-selected, and the translator rewrites every label when the language
   * loads. That last one is why this is not a listener on some language event:
   * translation finishes whenever its fetch comes back, which may be before or
   * after this file runs, and a race that leaves the wrong words in the
   * navigation bar would be invisible until someone switched to Portuguese.
   *
   * Watching text as well as attributes is only affordable because the subtree
   * is four buttons. Nothing else in this file observes the document.
   */
  const tabStrip = $(".tabs");
  if (tabStrip) {
    new MutationObserver(records => {
      const relabelled = records.some(record => record.attributeName !== "aria-selected");
      relabelled ? buildNav() : syncNav();
    }).observe(tabStrip, {
      attributes: true,
      attributeFilter: ["hidden", "aria-selected"],
      characterData: true,
      childList: true,
      subtree: true,
    });
  }

  // --- floating action button --------------------------------------------

  // Sharing into Pictogrep is the main way pictures arrive on a phone, and it
  // starts in another app. This is the one that starts here, and on a phone
  // the system picker hands over exactly the pictures that were tapped without
  // the app ever holding a media permission.
  const picker = $("#imageFiles");
  if (picker) {
    const fab = document.createElement("button");
    fab.type = "button";
    fab.className = "m3-fab";
    fab.setAttribute("aria-label", $("[data-i18n='add.choose_images']")?.textContent.trim() || "Add pictures");
    fab.dataset.i18nAriaLabel = "add.choose_images";
    fab.append(icon("add"));
    fab.onclick = () => picker.click();
    document.body.append(fab);
  }
})();
