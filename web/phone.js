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
  // hundred kilobytes to draw six shapes. The set itself lives in icons.js so
  // the desktop menus can draw from it too.
  const icon = name => window.PictogrepIcons.svg(name);

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
    "#showPremium": "saved",
  };

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
      // The label is display:none in this bar and the icon is aria-hidden, so
      // without this the destination reaches a screen reader as an unnamed
      // button. Same words either way; only the route to them differs.
      item.setAttribute("aria-label", text.textContent);
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
      // This destination opens the hamburger drawer (storyboards, settings,
      // plugins, about), not a profile screen, so it is drawn and labelled as
      // the menu it actually is rather than the person icon that used to sit
      // here and mismatch what a tap does.
      bed.append(icon("menu"));
      const text = document.createElement("span");
      text.className = "m3-nav-label";
      text.textContent = t("app.menu", "Menu");
      item.setAttribute("aria-label", text.textContent);
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
    const decorate = (row, name) => window.PictogrepIcons.decorate(row, name);
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
