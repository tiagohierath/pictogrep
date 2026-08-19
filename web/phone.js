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
  //
  // Symbols, not the older Material Icons: the two sets draw the same names in
  // different hands, and mixing them is visible. They are also drawn in a
  // different box, 960 units tall with the baseline at zero rather than 24
  // square, which is what ICON_BOX below is.
  const ICON_BOX = "0 -960 960 960";
  const ICONS = {
    pictures: "M360-400h400L622-580l-92 120-62-80-108 140Zm-40 160q-33 0-56.5-23.5T240-320v-480q0-33 23.5-56.5T320-880h480q33 0 56.5 23.5T880-800v480q0 33-23.5 56.5T800-240H320Zm0-80h480v-480H320v480ZM160-80q-33 0-56.5-23.5T80-160v-560h80v560h560v80H160Zm160-720v480-480Z",
    folders: "M160-160q-33 0-56.5-23.5T80-240v-480q0-33 23.5-56.5T160-800h240l80 80h320q33 0 56.5 23.5T880-640v400q0 33-23.5 56.5T800-160H160Zm0-80h640v-400H447l-80-80H160v480Zm0 0v-480 480Z",
    boards: "M520-600v-240h320v240H520ZM120-440v-400h320v400H120Zm400 320v-400h320v400H520Zm-400 0v-240h320v240H120Zm80-400h160v-240H200v240Zm400 320h160v-240H600v240Zm0-480h160v-80H600v80ZM200-200h160v-80H200v80Zm160-320Zm240-160Zm0 240ZM360-280Z",
    commons: "M324-111.5Q251-143 197-197t-85.5-127Q80-397 80-480t31.5-156Q143-709 197-763t127-85.5Q397-880 480-880t156 31.5Q709-817 763-763t85.5 127Q880-563 880-480t-31.5 156Q817-251 763-197t-127 85.5Q563-80 480-80t-156-31.5ZM440-162v-78q-33 0-56.5-23.5T360-320v-40L168-552q-3 18-5.5 36t-2.5 36q0 121 79.5 212T440-162Zm276-102q41-45 62.5-100.5T800-480q0-98-54.5-179T600-776v16q0 33-23.5 56.5T520-680h-80v80q0 17-11.5 28.5T400-560h-80v80h240q17 0 28.5 11.5T600-440v120h40q26 0 47 15.5t29 40.5Z",
    calendar: "M200-80q-33 0-56.5-23.5T120-160v-560q0-33 23.5-56.5T200-800h40v-80h80v80h320v-80h80v80h40q33 0 56.5 23.5T840-720v560q0 33-23.5 56.5T760-80H200Zm0-80h560v-400H200v400Zm0-480h560v-80H200v80Zm0 0v-80 80Zm280 240q-17 0-28.5-11.5T440-440q0-17 11.5-28.5T480-480q17 0 28.5 11.5T520-440q0 17-11.5 28.5T480-400Zm-188.5-11.5Q280-423 280-440t11.5-28.5Q303-480 320-480t28.5 11.5Q360-457 360-440t-11.5 28.5Q337-400 320-400t-28.5-11.5ZM640-400q-17 0-28.5-11.5T600-440q0-17 11.5-28.5T640-480q17 0 28.5 11.5T680-440q0 17-11.5 28.5T640-400ZM480-240q-17 0-28.5-11.5T440-280q0-17 11.5-28.5T480-320q17 0 28.5 11.5T520-280q0 17-11.5 28.5T480-240Zm-188.5-11.5Q280-263 280-280t11.5-28.5Q303-320 320-320t28.5 11.5Q360-297 360-280t-11.5 28.5Q337-240 320-240t-28.5-11.5ZM640-240q-17 0-28.5-11.5T600-280q0-17 11.5-28.5T640-320q17 0 28.5 11.5T680-280q0 17-11.5 28.5T640-240Z",
    profile: "M367-527q-47-47-47-113t47-113q47-47 113-47t113 47q47 47 47 113t-47 113q-47 47-113 47t-113-47ZM160-160v-112q0-34 17.5-62.5T224-378q62-31 126-46.5T480-440q66 0 130 15.5T736-378q29 15 46.5 43.5T800-272v112H160Zm80-80h480v-32q0-11-5.5-20T700-306q-54-27-109-40.5T480-360q-56 0-111 13.5T260-306q-9 5-14.5 14t-5.5 20v32Zm296.5-343.5Q560-607 560-640t-23.5-56.5Q513-720 480-720t-56.5 23.5Q400-673 400-640t23.5 56.5Q447-560 480-560t56.5-23.5ZM480-640Zm0 400Z",
    add: "M440-440H200v-80h240v-240h80v240h240v80H520v240h-80v-240Z",
  };

  function icon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", ICON_BOX);
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("fill", "currentColor");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", ICONS[name]);
    svg.append(path);
    return svg;
  }

  const $ = selector => document.querySelector(selector);

  /* A translated string, or the English one until the locale has arrived.
     i18n.t answers with the key itself when it has nothing, and a navigation
     bar reading "nav.feed" for the first few hundred milliseconds is worse than
     one reading English for them. */
  function t(key, english) {
    const value = window.PictogrepI18n?.t(key);
    return !value || value === key ? english : value;
  }

  // --- ripple ------------------------------------------------------------

  // Everything that can be pressed, named once. The state layer under these is
  // pure CSS, so this list exists only to find the element a touch landed on.
  // Menu controls left out on purpose: a ripple on the bottom destinations or
  // a drawer row is a 450ms effect on the exact thing that is supposed to feel
  // instant, changing where you are.
  const PRESSABLE = "button, .card-menu a, .card-menu button";

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
  // A destination may name its own label. The pictures tab is called Pictures
  // in a window with a tab strip in it, and Feed at the bottom of a phone,
  // where it is the place you land rather than one view of several.
  const DESTINATIONS = [
    {tab: "#imagesTab", icon: "pictures", key: "nav.feed", english: "Feed"},
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

    shown.forEach(({tab, icon: name, key, english}) => {
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
      text.textContent = key ? t(key, english) : label(button);
      item.append(bed, text);
      item.onclick = () => button.click();
      nav.append(item);
    });

    // Everything that is not the library itself: storyboards, settings,
    // plugins, importing, about. On a desktop that is a menu behind an icon in
    // the corner, which on a phone is the hardest place on the screen to
    // reach. As the last destination it is under a thumb, and the drawer it
    // opens is unchanged, so nothing moved except the way in. Only when there
    // is room inside Material's five.
    const menu = $("#menuButton");
    if (menu && shown.length < 5) {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "m3-nav-item";
      const bed = document.createElement("span");
      bed.className = "m3-nav-icon";
      bed.append(icon("profile"));
      const text = document.createElement("span");
      text.className = "m3-nav-label";
      text.textContent = t("nav.profile", "Profile");
      item.append(bed, text);
      item.onclick = () => menu.click();
      nav.append(item);
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
