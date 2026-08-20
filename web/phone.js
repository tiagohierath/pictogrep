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
    // The drawer's own list. Same 960 box as the destinations above.
    storyboard: "M320-200v-560l440 280-440 280Zm80-280Zm0 134 210-134-210-134v268Z",
    saved: "M200-120v-640q0-33 23.5-56.5T280-840h400q33 0 56.5 23.5T760-760v640L480-240 200-120Zm80-122 200-86 200 86v-518H280v518Zm0-518h400-400Z",
    plugins: "M760-600v-80h-80v-80h80v-80h80v80h80v80h-80v80h-80ZM280-80q-33 0-56.5-23.5T200-160v-160H80v-80q0-66 47-113t113-47h240q66 0 113 47t47 113v80H520v160q0 33-23.5 56.5T440-80H280Zm-120-320h440q0-33-23.5-56.5T520-480H280q-33 0-56.5 23.5T200-400h-40Zm120 240h160v-160H280v160Zm120-480q-50 0-85-35t-35-85q0-50 35-85t85-35q50 0 85 35t35 85q0 50-35 85t-85 35Zm0-80q17 0 28.5-11.5T440-760q0-17-11.5-28.5T400-800q-17 0-28.5 11.5T360-760q0 17 11.5 28.5T400-720Zm0-40Zm-40 440Z",
    settings: "m370-80-16-128q-13-5-24.5-12T307-235l-119 50L78-375l103-78q-1-7-1-13.5v-27q0-6.5 1-13.5L78-585l110-190 119 50q11-8 23-15t24-12l16-128h220l16 128q13 5 24.5 12t22.5 15l119-50 110 190-103 78q1 7 1 13.5v27q0 6.5-2 13.5l103 78-110 190-118-50q-11 8-23 15t-24 12L590-80H370Zm70-80h79l14-106q31-8 57.5-23.5T639-327l99 41 39-68-86-65q5-14 7-29.5t2-31.5q0-16-2-31.5t-7-29.5l86-65-39-68-99 42q-22-23-48.5-38.5T533-694l-13-106h-79l-14 106q-31 8-57.5 23.5T321-633l-99-41-39 68 86 64q-5 15-7 30t-2 32q0 16 2 31t7 30l-86 65 39 68 99-42q22 23 48.5 38.5T427-266l13 106Zm42-180q58 0 99-41t41-99q0-58-41-99t-99-41q-59 0-99.5 41T342-480q0 58 40.5 99t99.5 41Zm-2-140Z",
    about: "M440-280h80v-240h-80v240Zm40-320q17 0 28.5-11.5T520-640q0-17-11.5-28.5T480-680q-17 0-28.5 11.5T440-640q0 17 11.5 28.5T480-600Zm0 520q-83 0-156-31.5T197-197q-54-54-85.5-127T80-480q0-83 31.5-156T197-763q54-54 127-85.5T480-880q83 0 156 31.5T763-763q54 54 85.5 127T880-480q0 83-31.5 156T763-197q-54 54-127 85.5T480-80Zm0-80q134 0 227-93t93-227q0-134-93-227t-227-93q-134 0-227 93t-93 227q0 134 93 227t227 93Zm0-320Z",
    link: "M440-280H280q-83 0-141.5-58.5T80-480q0-83 58.5-141.5T280-680h160v80H280q-50 0-85 35t-35 85q0 50 35 85t85 35h160v80ZM320-440v-80h320v80H320Zm200 160v-80h160q50 0 85-35t35-85q0-50-35-85t-85-35H520v-80h160q83 0 141.5 58.5T880-480q0 83-58.5 141.5T680-280H520Z",
    connect: "M160-160q-33 0-56.5-23.5T80-240v-480q0-33 23.5-56.5T160-800h240q33 0 56.5 23.5T480-720v480q0 33-23.5 56.5T400-160H160Zm0-80h240v-480H160v480Zm400 80v-80h80v80h-80Zm120-120v-80h80v80h-80Zm-120 0v-80h80v80h-80Zm120-120v-80h80v80h-80Zm-120 0v-80h80v80h-80Zm120-120v-80h80v80h-80Zm-120 0v-80h80v80h-80Zm120-120v-80h80v80h-80Zm-120 0v-80h80v80h-80ZM280-440Z",
  };

  /* Which glyph belongs to which drawer entry. Icons are a scanning aid in a
     list people use constantly, so every row gets one: a list where only some
     rows have an icon reads as a list with things missing from it. */
  const MENU_ICONS = {
    "#showBoards": "saved",
    "#showAdd": "add",
    "#showSync": "connect",
    "#showSyncPhone": "connect",
    "#showPinterest": "link",
    "#showPlugins": "plugins",
    "#showSettings": "settings",
    "#showAbout": "about",
    "#openNavylily": "link",
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
      // The drawer is a sheet drawn over everything, not one more tab: closing
      // it is nobody's job but this one, because none of the four panels below
      // know it is open, let alone that a tap here is a request to leave it.
      // Without this, tapping Feed while the drawer covers the screen (from
      // Menu, or from Connect phone, or from any drawer panel) reloads the
      // pictures underneath a sheet that is still up, and looks like the
      // button silently did nothing.
      item.onclick = () => { closeMenu(); button.click(); };
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
      item.dataset.drawer = "true";
      item.onclick = () => menu.click();
      nav.append(item);
    }

    syncNav();
  }

  /**
   * The tab buttons stay the truth about which panel is open, except for the
   * last destination, which opens the drawer rather than a tab. It has no tab
   * to ask, so it reads the drawer itself: without this it was the one item in
   * the bar that could never light up, because syncNav only ever looked at
   * items carrying data-for.
   */
  function syncNav() {
    const drawerOpen = $("#drawer")?.classList.contains("open");
    nav.querySelectorAll("[data-for]").forEach(item => {
      const tab = $(item.dataset.for);
      const open = !drawerOpen && tab?.getAttribute("aria-selected") === "true";
      open ? item.setAttribute("aria-current", "page") : item.removeAttribute("aria-current");
    });
    const drawerItem = nav.querySelector("[data-drawer]");
    if (drawerItem) {
      drawerOpen
        ? drawerItem.setAttribute("aria-current", "page")
        : drawerItem.removeAttribute("aria-current");
    }
  }

  /*
   * A pull-down gap that moves the photos, not the header.
   *
   * The spacer goes inside .main-content, right after the header rather than
   * before it. That is the whole fix for the header dragging along with the
   * content on the first try: the header is sticky, and a sticky element only
   * releases from top:0 while something before it in the document is still
   * scrolling past. With nothing above the header any more, it is stuck from
   * scrollTop 0 onward and never moves, and the spacer's own scroll is what
   * carries the photos down when it is pulled into view.
   */
  const main = $(".main-content");
  if (main) {
    const spacer = document.createElement("div");
    spacer.className = "pull-space";
    spacer.setAttribute("aria-hidden", "true");
    main.prepend(spacer);
    const hideSpacer = () => window.scrollTo(0, spacer.getBoundingClientRect().height);
    hideSpacer();
    // Only while the gap is still at or near the top of the screen: once the
    // person has scrolled on into the library, a resize (the keyboard opening
    // for a search, most often) must not snap them back out of what they were
    // doing.
    window.addEventListener("resize", () => {
      if (window.scrollY < spacer.getBoundingClientRect().height) hideSpacer();
    });
  }

  /*
   * Swipe to move between pictures, replacing the ‹ › buttons this build
   * hides. Left click and right click were the only way; this is the same
   * move, made with a thumb instead of a target the width of a fingertip
   * sitting on top of every photo.
   */
  const viewerPicture = $(".viewer-picture");
  if (viewerPicture) {
    let startX = null;
    let startY = null;
    viewerPicture.addEventListener("touchstart", event => {
      const touch = event.touches[0];
      startX = touch.clientX;
      startY = touch.clientY;
    }, { passive: true });
    viewerPicture.addEventListener("touchend", event => {
      if (startX === null) return;
      const touch = event.changedTouches[0];
      const dx = touch.clientX - startX;
      const dy = touch.clientY - startY;
      startX = null;
      // Mostly horizontal and past a real flick, not a stray thumb wobble
      // while pinching or just reading the picture.
      if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) {
        $(dx < 0 ? "#viewerNext" : "#viewerPrevious")?.click();
      }
    }, { passive: true });
  }

  buildNav();

  /*
   * Icons down the drawer's own list.
   *
   * Prepended to the existing rows rather than written into index.html,
   * because that page is also the desktop's, where this list is a plain text
   * menu. The label stays whatever it already was, translated or not, so
   * nothing here has to know what any row says. The storyboard link is matched
   * by href since it is the one row with no id.
   */
  const menuList = $(".drawer-actions");
  if (menuList) {
    const decorate = (row, name) => {
      if (!row || row.querySelector("svg")) return;
      // The label has to move into a span first. i18n's apply() writes
      // translations with element.textContent, which replaces everything inside
      // the element it targets, icon included: a row translated after this ran
      // would silently lose its icon, and the first render happens either side
      // of that fetch depending on the network. Moving data-i18n onto a span
      // gives the translator its own box to overwrite.
      if (row.dataset.i18n) {
        const label = document.createElement("span");
        label.dataset.i18n = row.dataset.i18n;
        label.textContent = row.textContent.trim();
        row.removeAttribute("data-i18n");
        row.replaceChildren(label);
      }
      row.prepend(icon(name));
    };
    decorate(menuList.querySelector('a[href="/practice"]'), "storyboard");
    for (const [selector, name] of Object.entries(MENU_ICONS)) {
      decorate(menuList.querySelector(selector), name);
    }
  }

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
  /*
   * The drawer opening is not a change to the tab strip, so the observer below
   * never hears about it. Watching the drawer's own class is what keeps the
   * last destination lit while its sheet is up, and unlit the moment any of
   * the ways out of it (the close button, the scrim, another destination,
   * anything that calls closeMenu) takes it down.
   */
  const drawer = $("#drawer");
  if (drawer) {
    // One spacer, placed once, right after the header and before whichever
    // panel the drawer opens with: the header above it is what to keep
    // pinned, and everything after it is one of the panels this shares
    // between.
    const drawerSpacer = document.createElement("div");
    drawerSpacer.className = "pull-space";
    drawerSpacer.setAttribute("aria-hidden", "true");
    $(".drawer-header")?.after(drawerSpacer);
    const hideDrawerSpacer = () => {
      drawer.scrollTop = drawerSpacer.getBoundingClientRect().height;
    };

    new MutationObserver(() => {
      syncNav();
      // Reset every time the sheet opens, not once at page load: the drawer
      // starts hidden off-screen and unscrolled, and each new open is a fresh
      // reason to hide the gap again, whichever panel it opens to.
      if (drawer.classList.contains("open")) hideDrawerSpacer();
    }).observe(drawer, {
      attributes: true,
      attributeFilter: ["class"],
    });
  }

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
    fab.append(icon("add"));

    // What "add" means depends on what is being looked at. Standing in
    // Folders, the thing to add is a folder; the picture picker there offers
    // to import into a list that does not hold pictures. Material calls this
    // the action of the current screen rather than of the app, which is the
    // same reason this button is one button and not a row of them.
    const onFolders = () => $("#foldersTab")?.getAttribute("aria-selected") === "true";
    const newFolder = $("#newFolderButton");

    // Both the label and what a tap does are decided at tap time, not here:
    // the destination changes under this button every time somebody moves
    // between the two, and the tab strip is what knows, so nothing is cached.
    const describe = () => {
      const key = onFolders() && newFolder ? "folders.new_aria" : "add.choose_images";
      fab.dataset.i18nAriaLabel = key;
      fab.setAttribute("aria-label", t(key, onFolders() && newFolder ? "New folder" : "Add pictures"));
    };
    describe();
    fab.onclick = () => (onFolders() && newFolder ? newFolder.click() : picker.click());

    // The same observer that already keeps the navigation bar in step. The
    // tab strip is the one thing that knows which destination is showing, and
    // watching it is cheaper than every panel telling this button it changed.
    if ($(".tabs")) {
      new MutationObserver(describe).observe($(".tabs"), {
        attributes: true,
        attributeFilter: ["aria-selected"],
        subtree: true,
      });
    }

    document.body.append(fab);
  }
})();
