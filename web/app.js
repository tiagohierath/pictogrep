const $ = selector => document.querySelector(selector);
const t = (key, values) => window.PictogrepI18n.t(key, values);

let appState = null;
let currentTag = "";
let currentSource = "";
let currentFolderName = "";
let currentQuery = "";
let commandPaletteIndex = 0;
let commandPaletteItems = [];
let sidebarMode = "random";
let vimPendingG = false;
let messageTimer = null;
let pollTimer = null;
let lastJobState = "idle";
let indexJobAnnounce = false;
let forceIndexAfterRefresh = false;
let lastLibraryRefreshAt = 0;
let aiWorker = null;
let aiRequestId = 0;
let imageLoadId = 0;
let viewerImageLoadId = 0;
let viewerPreviewTimer = null;
let semanticIndexPromise = null;
let semanticIndexTimer = null;
let semanticIndexQueued = false;
let semanticIndexForced = false;
let semanticIndexPaused = false;
let fullReindexRunning = false;
let quietImageIndexing = false;
let semanticIndexStatus = {state: "idle", indexed: 0, total: 0, error: ""};
let semanticWarmupPromise = null;
let textWarmupPromise = null;
let quietTextWarmup = false;
let foregroundTextRequests = 0;
let pinterestImportController = null;
let lastPinterestFolder = "";
let queryPrimeTimer = null;
let currentViewerItem = null;
let browserImages = [];
let relatedLoadId = 0;
let canvasPositions = new Map();
let canvasImages = [];
let canvasPan = {x: 0, y: 0};
let canvasZoom = 1;
let canvasSaveTimer = null;
let canvasPointer = null;
let pendingDeleteItem = null;
let externalDragDepth = 0;
let importProgressTimer = null;
let importQueue = Promise.resolve();
const failedSemanticPaths = new Set();
const aiRequests = new Map();
const semanticEmbeddingPromises = new Map();
const semanticVectors = new Map();
const semanticResults = new Map();
const viewerPreviewPromises = new Map();
const loadedImageURLs = new Set();
const recentSearchesKey = "pictogrep.recentSearches";

function rememberLoadedImage(url) {
  if (!url) return;
  loadedImageURLs.delete(url);
  loadedImageURLs.add(url);
  while (loadedImageURLs.size > 2048) loadedImageURLs.delete(loadedImageURLs.values().next().value);
}

function loadImage(image, url, {fallback = "", label = "", onload = null} = {}) {
  let activeURL = url;
  let finished = false;
  let slow = false;
  let slowTimer = null;

  image.alt = "";
  image.classList.add("loading-image");
  image.classList.remove("is-slow-loading", "is-revealing");

  const finish = () => {
    if (finished || !image.naturalWidth) return;
    finished = true;
    clearTimeout(slowTimer);
    rememberLoadedImage(activeURL);
    image.classList.remove("is-slow-loading");
    if (slow) {
      image.classList.add("is-revealing");
      image.addEventListener("animationend", () => image.classList.remove("is-revealing"), {once: true});
    }
    image.alt = label;
    onload?.(image);
  };

  const assign = nextURL => {
    activeURL = nextURL;
    finished = false;
    slow = false;
    clearTimeout(slowTimer);
    image.classList.remove("is-slow-loading", "is-revealing");
    image.onload = finish;
    image.onerror = () => {
      if (fallback && activeURL !== fallback) {
        assign(fallback);
        return;
      }
      clearTimeout(slowTimer);
      image.classList.remove("is-slow-loading", "is-revealing");
    };
    image.src = nextURL;
    if (image.complete && image.naturalWidth) {
      finish();
    } else if (!loadedImageURLs.has(nextURL)) {
      slowTimer = setTimeout(() => {
        if (finished) return;
        const bounds = image.getBoundingClientRect();
        if (!image.isConnected || bounds.bottom < 0 || bounds.top > window.innerHeight) return;
        slow = true;
        image.classList.add("is-slow-loading");
      }, 140);
    }
  };

  assign(url);
}

async function request(url, options = {}) {
  const response = await fetch(url, options);
  const body = await response.text();
  let data;
  try {
    data = JSON.parse(body);
  } catch (_) {
    throw new Error(`Pictogrep returned an invalid response (${response.status})`);
  }
  if (!response.ok || data.ok === false) {
    const error = new Error(data.error || `Request failed (${response.status})`);
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

function logBackground(event, message, path = "", level = "warning") {
  fetch("/api/app/log", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({level, event, message: String(message || ""), path}),
  }).catch(() => {});
}

function getAIWorker() {
  if (aiWorker) return aiWorker;
  aiWorker = new Worker("/assets/ai-worker.js", {type: "module"});
  aiWorker.onmessage = event => {
    const message = event.data;
    if (message.type === "progress") {
      if (message.kind === "text" && quietTextWarmup && foregroundTextRequests === 0) return;
      if (message.kind === "image" && quietImageIndexing) return;
      const detail = message.detail || {};
      if (detail.status === "progress" && Number.isFinite(detail.progress)) {
        showMessage(t("ai.preparing_percent", {progress: Math.round(detail.progress)}), false, true);
      } else if (detail.status === "initiate") {
        showMessage(t("ai.preparing_first"), false, true);
      }
      return;
    }
    const pending = aiRequests.get(message.id);
    if (!pending) return;
    aiRequests.delete(message.id);
    if (message.type === "error") {
      const error = new Error(message.error);
      error.kind = message.kind;
      pending.reject(error);
    } else pending.resolve(message.result);
  };
  aiWorker.onerror = event => {
    const error = new Error(event.message || "Search could not start");
    error.kind = "model";
    for (const pending of aiRequests.values()) pending.reject(error);
    aiRequests.clear();
    aiWorker.terminate();
    aiWorker = null;
  };
  return aiWorker;
}

function runAI(type, values = {}) {
  const id = ++aiRequestId;
  return new Promise((resolve, reject) => {
    aiRequests.set(id, {resolve, reject});
    try {
      getAIWorker().postMessage({id, type, model: appState?.embeddingModel, ...values});
    } catch (error) {
      aiRequests.delete(id);
      const workerError = error instanceof Error ? error : new Error(String(error));
      workerError.kind ??= "model";
      reject(workerError);
    }
  });
}

function remember(cache, key, value, limit) {
  cache.delete(key);
  cache.set(key, value);
  while (cache.size > limit) cache.delete(cache.keys().next().value);
  return value;
}

function normalizedQuery(query) {
  return query.trim().replace(/\s+/g, " ").toLowerCase();
}

function hasSearchScope() {
  return Boolean(currentTag || currentSource);
}

function renderSearchScope() {
  const scope = $("#searchScope");
  if (!hasSearchScope()) {
    scope.hidden = true;
    scope.textContent = "";
    return;
  }
  scope.hidden = false;
  const value = currentFolderName || currentTag || currentSource;
  scope.textContent = t("search.scope", {name: value.split(/[\\/]/).filter(Boolean).pop() || value});
}

function clearSearchScope() {
  currentTag = "";
  currentSource = "";
  currentFolderName = "";
  renderSearchScope();
  loadImages();
}

function cleanSearchTerm(raw) {
  return String(raw || "").trim().replace(/\s+/g, " ");
}

function searchQueryValue() {
  return cleanSearchTerm($("#searchQuery")?.value || "");
}

function resizeSearchDraft() {
  const input = $("#searchQuery");
  $("#searchDraftSizer").textContent = input.value || input.placeholder;
}

function setSearchInput(value) {
  $("#searchQuery").value = cleanSearchTerm(value);
  resizeSearchDraft();
}

function clearSearchInput() {
  $("#searchQuery").value = "";
  resizeSearchDraft();
}

function performSearch(remember = false) {
  clearTimeout(queryPrimeTimer);
  currentQuery = searchQueryValue();
  if (remember) rememberSearch(currentQuery);
  $("#recentSearches").hidden = true;
  if (!$("#commonsPanel").hidden) loadCommons();
  else if (!$("#calendarPanel").hidden) loadCalendar();
  else loadImages();
}

function recentSearches() {
  try {
    const values = JSON.parse(localStorage.getItem(recentSearchesKey) || "[]");
    return Array.isArray(values) ? values.filter(value => typeof value === "string").slice(0, 5) : [];
  } catch (_) { return []; }
}

function rememberSearch(query) {
  query = query.trim();
  if (!query) return;
  const values = [query, ...recentSearches().filter(value => value.toLowerCase() !== query.toLowerCase())].slice(0, 5);
  localStorage.setItem(recentSearchesKey, JSON.stringify(values));
}

function renderRecentSearches() {
  const list = $("#recentSearches");
  const values = recentSearches();
  list.replaceChildren(...values.map(value => {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = value;
    button.onclick = () => {
      setSearchInput(value);
      list.hidden = true;
      currentQuery = value;
      if (!$("#commonsPanel").hidden) loadCommons();
      else if (!$("#calendarPanel").hidden) loadCalendar();
      else loadImages();
    };
    return button;
  }));
  list.hidden = !values.length;
}

function semanticVector(query) {
  const key = normalizedQuery(query);
  const existing = semanticVectors.get(key);
  if (existing) return remember(semanticVectors, key, existing, 48);
  const pending = (async () => {
    const cached = await request(`/api/app/ai/query?q=${encodeURIComponent(key)}`);
    if (cached.cached && cached.model === appState.embeddingModel.key && cached.vector?.length === appState.embeddingModel.dimensions) return cached.vector;
    quietTextWarmup = false;
    foregroundTextRequests++;
    let vector;
    try {
      vector = await runAI("search", {query: key});
    } finally {
      foregroundTextRequests--;
    }
    await request("/api/app/ai/query", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({model: appState.embeddingModel.key, query: key, vector}),
    });
    return vector;
  })().catch(error => {
    semanticVectors.delete(key);
    throw error;
  });
  return remember(semanticVectors, key, pending, 48);
}

function warmTextSearch() {
  if (textWarmupPromise) return textWarmupPromise;
  quietTextWarmup = true;
  textWarmupPromise = runAI("warmText").catch(() => {}).finally(() => {
    quietTextWarmup = false;
    textWarmupPromise = null;
  });
  return textWarmupPromise;
}

async function saveSemanticEmbedding(item) {
  const existing = semanticEmbeddingPromises.get(item.path);
  if (existing) return existing;
  const pending = (async () => {
    const vector = await runAI("embed", {item});
    await request("/api/app/ai/embeddings", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({model: appState.embeddingModel.key, items: [{path: item.path, mtime: item.mtime, vector}]}),
    });
  })().finally(() => semanticEmbeddingPromises.delete(item.path));
  semanticEmbeddingPromises.set(item.path, pending);
  return pending;
}

function semanticResultKey(query) {
  return JSON.stringify([normalizedQuery(query), currentTag, currentSource]);
}

async function requestSemanticResults(query, refresh = false) {
  const key = semanticResultKey(query);
  if (!refresh && semanticResults.has(key)) {
    return remember(semanticResults, key, semanticResults.get(key), 24);
  }
  const vector = await semanticVector(query);
  const data = await request("/api/app/ai/search", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({model: appState.embeddingModel.key, vector, limit: 120, tag: currentTag, source: currentSource}),
  });
  return remember(semanticResults, key, data, 24);
}

async function refreshSemanticResults() {
  const query = currentQuery;
  if (!query || !appState?.aiAvailable) return;
  try {
    const data = await requestSemanticResults(query, true);
    if (query === currentQuery) renderImages(data.images, data.images.length);
  } catch (_) {
    // The active search already reports errors. Background refreshes stay quiet.
  }
}

function automaticIndexingEnabled() {
  return appState?.searchIndex?.automatic !== false;
}

function updateSemanticIndexStatus(state, indexed, total, error = "") {
  semanticIndexStatus = {state, indexed, total, error};
  if (appState) {
    appState.searchIndex ||= {};
    appState.searchIndex.indexed = indexed;
    appState.searchIndex.total = total;
    appState.searchIndex.pending = Math.max(0, total - indexed);
  }
  renderSearchIndexSettings();
}

function scheduleSemanticIndex(delay = 500, force = false) {
  semanticIndexQueued = true;
  semanticIndexForced ||= force;
  if (semanticIndexPaused || (!force && !automaticIndexingEnabled())) {
    const state = appState?.searchIndex || {};
    updateSemanticIndexStatus("paused", state.indexed || 0, state.total || 0);
    return;
  }
  if (semanticIndexPromise) return;
  clearTimeout(semanticIndexTimer);
  semanticIndexTimer = setTimeout(runSemanticIndex, delay);
}

function continueSemanticIndex(_items = [], completed = 0, total = 0) {
  if (total) updateSemanticIndexStatus("queued", completed, total);
  scheduleSemanticIndex();
}

function runSemanticIndex() {
  if (semanticIndexPromise || semanticIndexPaused) return semanticIndexPromise;
  semanticIndexPromise = (async () => {
    let force = semanticIndexForced;
    semanticIndexForced = false;
    quietImageIndexing = true;
    while (true) {
      semanticIndexQueued = false;
      force ||= semanticIndexForced;
      semanticIndexForced = false;
      if (semanticIndexPaused || (!force && !automaticIndexingEnabled())) {
        const status = appState?.searchIndex || {};
        updateSemanticIndexStatus("paused", status.indexed || 0, status.total || 0);
        break;
      }
      const state = await request("/api/app/ai");
      let ready = state.indexed;
      const items = state.missing.filter(item => !failedSemanticPaths.has(item.path));
      if (!state.missing.length) {
        updateSemanticIndexStatus("ready", state.total, state.total);
        break;
      }
      if (!items.length) {
        updateSemanticIndexStatus("partial", ready, state.total);
        break;
      }
      updateSemanticIndexStatus("indexing", ready, state.total);
      for (const item of items) {
        if (semanticIndexPaused || (!force && !automaticIndexingEnabled())) break;
        try {
          await saveSemanticEmbedding(item);
          ready++;
        } catch (error) {
          if (error.kind === "image") {
            failedSemanticPaths.add(item.path);
            logBackground("search-index-image", `${item.name}: ${error.message}`, item.path);
            continue;
          }
          if (error.status === 409) {
            semanticIndexQueued = true;
            continue;
          }
          throw error;
        }
        updateSemanticIndexStatus("indexing", ready, state.total);
        if (ready % 24 === 0) {
          await refreshSemanticResults();
          await refreshRelatedResults();
        }
      }
      await refreshSemanticResults();
      await refreshRelatedResults();
      if (semanticIndexPaused || (!force && !automaticIndexingEnabled())) continue;
      if (!semanticIndexQueued && !semanticIndexForced) {
        const unreadable = state.total - ready;
        updateSemanticIndexStatus(unreadable ? "partial" : "ready", ready, state.total);
        break;
      }
    }
  })().catch(error => {
    const status = appState?.searchIndex || {};
    logBackground("search-index-paused", error.message);
    updateSemanticIndexStatus("error", status.indexed || 0, status.total || 0, error.message);
  }).finally(() => {
    quietImageIndexing = false;
    semanticIndexPromise = null;
    if (semanticIndexQueued && !semanticIndexPaused && (semanticIndexForced || automaticIndexingEnabled())) {
      scheduleSemanticIndex(500, semanticIndexForced);
    }
  });
  return semanticIndexPromise;
}

async function semanticSearch(query) {
  // Prepare text and pictures together. On a brand-new library, return fast
  // filename matches immediately and replace them with semantic results as
  // soon as the first image vector is ready.
  const vectorPromise = semanticVector(query);
  vectorPromise.catch(() => {});
  let state = await request("/api/app/ai");

  if (state.indexed === 0 && state.missing.length) {
    if (!semanticWarmupPromise) {
      const first = state.missing.slice(0, 8);
      semanticWarmupPromise = (async () => {
        showMessage(t("ai.downloading"), false, true);
        for (let index = 0; index < first.length; index++) {
          try {
            await saveSemanticEmbedding(first[index]);
            return;
          } catch (error) {
            if (error.kind !== "image") throw error;
            failedSemanticPaths.add(first[index].path);
            logBackground("search-index-image", `${first[index].name}: ${error.message}`, first[index].path);
          }
        }
        throw new Error("The first pictures could not be read. Try adding a JPG or PNG image.");
      })().then(async () => {
        const updated = await request("/api/app/ai");
        continueSemanticIndex(updated.missing, updated.indexed, updated.total);
        await refreshSemanticResults();
      }).catch(error => {
        showMessage(t("ai.start_failed", {error: error.message}), true, true);
      }).finally(() => {
        semanticWarmupPromise = null;
      });
    }
    const fallback = await request(`/api/app/search?q=${encodeURIComponent(query)}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}&limit=120`);
    fallback.preparing = true;
    return fallback;
  }

  continueSemanticIndex(state.missing, state.indexed, state.total);
  await vectorPromise;
  return requestSemanticResults(query);
}

function showMessage(text, error = false, persist = false) {
  if (!error) {
    logBackground("ui-status", text, "", "info");
    return;
  }
  const box = $("#message");
  clearTimeout(messageTimer);
  $("#messageText").textContent = text;
  box.classList.add("error");
  box.hidden = false;
  if (!persist) messageTimer = setTimeout(() => { box.hidden = true; }, 3500);
}

function closeMessage() {
  clearTimeout(messageTimer);
  $("#message").hidden = true;
}

function showResultState(title = "", text = "", action = "", onclick = null) {
  const state = $("#resultState");
  state.hidden = !title;
  $("#resultStateTitle").textContent = title;
  $("#resultStateText").textContent = text;
  const button = $("#resultStateAction");
  button.hidden = !action;
  button.textContent = action;
  button.onclick = onclick;
}

function openMenu(pageTitle = "") {
  const drawer = $("#drawer");
  drawer.classList.toggle("page", Boolean(pageTitle));
  $("#drawerTitle").textContent = pageTitle || t("app.menu");
  drawer.classList.add("open");
  $("#drawer").setAttribute("aria-hidden", "false");
  $("#drawerScrim").hidden = false;
  $("#menuButton").setAttribute("aria-expanded", "true");
}

function closeMenu() {
  $("#drawer").classList.remove("open", "page");
  $("#drawerTitle").textContent = t("app.menu");
  $("#drawer").setAttribute("aria-hidden", "true");
  $("#drawerScrim").hidden = true;
  $("#menuButton").setAttribute("aria-expanded", "false");
}

function showMenuHome() {
  $("#addSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#boardsSection").hidden = true;
  openMenu();
}

function switchTab(tab) {
  const images = tab === "images";
  const commons = tab === "commons";
  const calendar = tab === "calendar";
  const folders = tab === "folders";
  $("#imagesTab").classList.toggle("active", images);
  $("#commonsTab").classList.toggle("active", commons);
  $("#calendarTab").classList.toggle("active", calendar);
  $("#foldersTab").classList.toggle("active", folders);
  $("#imagesTab").setAttribute("aria-selected", String(images));
  $("#commonsTab").setAttribute("aria-selected", String(commons));
  $("#calendarTab").setAttribute("aria-selected", String(calendar));
  $("#foldersTab").setAttribute("aria-selected", String(folders));
  $("#imagesPanel").hidden = !images;
  $("#commonsPanel").hidden = !commons;
  $("#calendarPanel").hidden = !calendar;
  $("#foldersPanel").hidden = images || commons || calendar;
}

function renderImageSkeletons(grid, count = 12) {
  grid.replaceChildren(...Array.from({length: count}, (_, index) => {
    const card = document.createElement("div");
    card.className = "image-card image-card-skeleton";
    const block = document.createElement("span");
    block.style.aspectRatio = ["4 / 3", "3 / 4", "1 / 1", "5 / 4"][index % 4];
    card.append(block);
    return card;
  }));
}

function setLoading() {
  const grid = $("#imageGrid");
  grid.classList.remove("is-loading");
  renderImageSkeletons(grid);
  showResultState();
  $("#imagesEmpty").hidden = true;
}

function pictureCard(item) {
  const card = document.createElement("article");
  card.className = "image-card";
  card.tabIndex = 0;
  card.setAttribute("aria-label", item.name);
  card.draggable = true;
  card.ondragstart = event => {
    event.dataTransfer.effectAllowed = "copy";
    event.dataTransfer.setData("application/x-pictogrep-image-id", String(item.id));
    event.dataTransfer.setData("text/plain", String(item.id));
  };

  const image = document.createElement("img");
  image.loading = "lazy";
  image.decoding = "async";
  if (item.width && item.height) {
    image.width = item.width;
    image.height = item.height;
  } else {
    image.style.aspectRatio = "4 / 3";
  }
  loadImage(image, thumbnailURL(item), {fallback: item.url, label: item.name});
  image.onclick = () => openImageViewer(item);
  card.append(image);

  const info = document.createElement("div");
  info.className = "image-info";
  const meta = document.createElement("div");
  meta.className = "image-meta";
  const filename = document.createElement("span");
  filename.className = "image-filename";
  filename.textContent = item.name;
  filename.title = item.name;
  (item.tags || []).slice(0, 2).forEach(value => {
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = `#${value}`;
    meta.append(tag);
  });
  info.append(filename, meta);
  card.append(info);

  const menuButton = document.createElement("button");
  menuButton.className = "card-menu-button";
  menuButton.type = "button";
  menuButton.textContent = "⋯";
  menuButton.setAttribute("aria-label", t("image.options", {name: item.name}));
  menuButton.setAttribute("aria-expanded", "false");

  const menu = document.createElement("div");
  menu.className = "card-menu";
  menu.hidden = true;
  const menuName = document.createElement("span");
  menuName.className = "card-menu-name";
  menuName.textContent = item.name;
  menuName.title = item.path || item.name;
  const draw = document.createElement("a");
  draw.href = `/practice?image=${encodeURIComponent(item.id)}`;
  draw.textContent = t("image.draw");
  const tagButton = document.createElement("button");
  tagButton.type = "button";
  tagButton.textContent = t("image.add_folder");
  tagButton.onclick = () => {
    closeCardMenus();
    openTagDialog(item.id);
  };
  const download = document.createElement("a");
  download.href = item.url;
  download.download = item.name;
  download.textContent = t("image.save_as");
  const deleteButton = document.createElement("button");
  deleteButton.type = "button";
  deleteButton.className = "menu-danger";
  deleteButton.textContent = t("image.delete");
  deleteButton.onclick = () => openDeleteImageDialog(item);
  menu.append(menuName, download, draw, tagButton, deleteButton);
  menuButton.onclick = event => {
    event.stopPropagation();
    const willOpen = menu.hidden;
    closeCardMenus();
    menu.hidden = !willOpen;
    menuButton.setAttribute("aria-expanded", String(willOpen));
  };
  bindImageContextMenu(card, item);
  info.append(menuButton, menu);
  return card;
}

function bindImageContextMenu(element, item) {
  element.oncontextmenu = event => openImageContextMenu(event, item);
}

function openImageContextMenu(event, item) {
  event.preventDefault();
  event.stopPropagation();
  closeCardMenus();
  const menu = $("#imageContextMenu");
  const dialog = event.currentTarget.closest("dialog[open]");
  (dialog || document.body).append(menu);
  menu.classList.add("cursor-menu");
  $("#contextImageName").textContent = item.name;
  $("#contextImageName").title = item.path || item.name;
  $("#contextOpenImage").onclick = () => {
    closeCardMenus();
    openImageViewer(item);
  };
  $("#contextDownloadImage").href = item.url;
  $("#contextDownloadImage").download = item.name;
  $("#contextDraw").href = `/practice?image=${encodeURIComponent(item.id)}`;
  $("#contextAddFolder").onclick = () => {
    closeCardMenus();
    openTagDialog(item.id);
  };
  $("#contextDeleteImage").onclick = () => openDeleteImageDialog(item);
  menu.hidden = false;
  positionContextMenu(menu, event);
}

function positionContextMenu(menu, event) {
  const margin = 8;
  const left = Math.max(margin, Math.min(event.clientX, window.innerWidth - menu.offsetWidth - margin));
  const top = Math.max(margin, Math.min(event.clientY, window.innerHeight - menu.offsetHeight - margin));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
}

function closeCardMenus() {
  document.querySelectorAll(".card-menu").forEach(menu => {
    menu.hidden = true;
    menu.classList.remove("cursor-menu");
    menu.style.removeProperty("left");
    menu.style.removeProperty("top");
  });
  document.querySelectorAll('.card-menu-button[aria-expanded="true"]').forEach(button => {
    button.setAttribute("aria-expanded", "false");
  });
}

function focusImageCard(delta) {
  const cards = [...document.querySelectorAll("#imageGrid .image-card, #calendarGroups .image-card")];
  if (!cards.length) return;
  const current = cards.indexOf(document.activeElement);
  const next = current < 0 ? (delta > 0 ? 0 : cards.length - 1) : Math.max(0, Math.min(cards.length - 1, current + delta));
  cards[next].focus();
  cards[next].scrollIntoView({block: "nearest"});
}

function openFocusedImage() {
  const card = document.activeElement?.closest?.(".image-card");
  card?.querySelector("img")?.click();
}

function showShortcuts() {
  $("#vimShortcutHelp").hidden = !appState?.plugins?.vim?.enabled;
  $("#shortcutsDialog").showModal();
}

function viewerURL(id = "") {
  const url = new URL(location.href);
  if (id) url.searchParams.set("image", id);
  else url.searchParams.delete("image");
  return url;
}

function openImageViewer(item, updateHistory = true) {
  const viewer = $("#imageViewer");
  showViewerImage(item, false);
  if (!viewer.open) viewer.showModal();
  if (updateHistory && new URL(location.href).searchParams.get("image") !== item.id) {
    history.pushState({pictogrepViewer: true}, "", viewerURL(item.id));
  }
}

function closeImageViewer() {
  if (!$("#imageViewer").open) return;
  if (new URL(location.href).searchParams.has("image")) {
    if (history.state?.pictogrepViewer) history.back();
    else {
      history.replaceState(history.state, "", viewerURL());
      $("#imageViewer").close();
    }
  } else $("#imageViewer").close();
}

async function syncViewerFromHistory() {
  const id = new URL(location.href).searchParams.get("image");
  if (!id) {
    if ($("#imageViewer").open) $("#imageViewer").close();
    return;
  }
  if (currentViewerItem?.id === id && $("#imageViewer").open) return;
  let item = browserImages.find(candidate => candidate.id === id);
  try {
    if (!item) item = (await request(`/api/app/images/${encodeURIComponent(id)}`)).image;
    openImageViewer(item, false);
  } catch (error) {
    history.replaceState(history.state, "", viewerURL());
    showMessage(error.message, true, true);
  }
}

function thumbnailURL(item, size = null) {
  size ||= {small: 640, medium: 960, large: 1280}[appState?.browser?.thumbnailSize] || 960;
  return `/thumbnail/${encodeURIComponent(item.id)}?size=${size}`;
}

function preloadViewerPreview(item) {
  const existing = viewerPreviewPromises.get(item.id);
  if (existing) return existing;
  // A long-edge thumbnail becomes extremely narrow for scrollable portraits
  // and then looks pixelated when displayed at the viewer's content width.
  const veryTall = item.width > 0 && item.height / item.width > 3.25;
  const url = veryTall && item.url ? item.url : thumbnailURL(item, 1920);
  const pending = new Promise(resolve => {
    const preview = new Image();
    preview.decoding = "async";
    preview.onload = () => resolve(url);
    preview.onerror = () => {
      viewerPreviewPromises.delete(item.id);
      resolve("");
    };
    preview.src = url;
  });
  return remember(viewerPreviewPromises, item.id, pending, 96);
}

function showViewerImage(item, showOriginal = false) {
  const viewer = $("#imageViewer");
  const image = $("#viewerImage");
  const picture = image.parentElement;
  const changed = currentViewerItem?.id !== item.id;
  const viewerLoadID = ++viewerImageLoadId;
  let upgradingUnknownPortrait = false;
  currentViewerItem = item;
  picture.classList.remove("is-scrollable-portrait");
  picture.setAttribute("aria-label", item.name);
  const setPortraitMode = (width, height) => {
    if (currentViewerItem?.id !== item.id) return;
    picture.classList.toggle("is-scrollable-portrait", width > 0 && height / width > 3.25);
  };
  const viewerImageLoaded = () => {
    if (viewerLoadID !== viewerImageLoadId || currentViewerItem?.id !== item.id) return;
    clearTimeout(picture._loadingTimer);
    const wasSlow = picture.classList.contains("is-slow-loading");
    picture.classList.remove("is-image-loading", "is-slow-loading");
    if (wasSlow) {
      image.classList.add("is-revealing");
      image.addEventListener("animationend", () => image.classList.remove("is-revealing"), {once: true});
    }
    image.alt = item.name;
    setPortraitMode(image.naturalWidth, image.naturalHeight);
    // Canvas and legacy records may not include dimensions. Detect those after
    // the cached preview paints and upgrade an extreme portrait to its original.
    if (!showOriginal && !item.width && !upgradingUnknownPortrait && item.url && image.naturalHeight / image.naturalWidth > 3.25) {
      upgradingUnknownPortrait = true;
      const original = new Image();
      original.decoding = "async";
      original.onload = () => {
        if (viewerLoadID === viewerImageLoadId && currentViewerItem?.id === item.id) image.src = item.url;
      };
      original.src = item.url;
    }
  };
  const originalButton = $("#showOriginal");
  const usesOriginalPreview = item.width > 0 && item.height / item.width > 3.25;
  originalButton.hidden = !item.url || usesOriginalPreview;
  originalButton.textContent = t(showOriginal ? "viewer.show_preview" : "viewer.show_original");
  originalButton.onclick = () => showViewerImage(item, !showOriginal);
  const initialURL = showOriginal ? item.url : thumbnailURL(item);
  if (changed) {
    clearTimeout(picture._loadingTimer);
    picture.classList.add("is-image-loading");
    picture.classList.remove("is-slow-loading");
    image.classList.remove("is-revealing");
    image.alt = "";
    image.removeAttribute("src");
    picture._loadingTimer = setTimeout(() => {
      if (viewerLoadID === viewerImageLoadId && currentViewerItem?.id === item.id && !image.complete) {
        picture.classList.add("is-slow-loading");
      }
    }, 140);
  }
  image.onload = viewerImageLoaded;
  image.src = initialURL;
  if (image.complete && image.naturalWidth) viewerImageLoaded();
  if (!showOriginal) {
    // The grid/related-card preview is normally already decoded and cached, so
    // paint it immediately. Upgrade to the large preview only after it is
    // ready; generating that preview must never block image-to-image browsing.
    preloadViewerPreview(item).then(url => {
      if (url && !upgradingUnknownPortrait && viewerLoadID === viewerImageLoadId && currentViewerItem?.id === item.id) image.src = url;
    });
  }
  bindImageContextMenu(image, item);
  if (item.width && item.height) setPortraitMode(item.width, item.height);
  if (changed) {
    viewer.scrollTop = 0;
    renderViewerTags(item.tags || []);
    updateViewerNavigation();
    loadRelatedImages(item);
  }
}

function updateViewerNavigation() {
  const index = browserImages.findIndex(item => item.id === currentViewerItem?.id);
  $("#viewerPrevious").disabled = index <= 0;
  $("#viewerNext").disabled = index < 0 || index >= browserImages.length - 1;
  $("#viewerPosition").textContent = index >= 0 ? t("viewer.position", {current: index + 1, total: browserImages.length}) : "";
  $("#viewerPosition").hidden = index < 0;
}

function moveViewer(delta) {
  const index = browserImages.findIndex(item => item.id === currentViewerItem?.id);
  const next = browserImages[index + delta];
  if (next) openImageViewer(next);
}

function renderViewerTags(tags) {
  const list = $("#viewerTagsList");
  const children = tags.map(value => {
    const tag = document.createElement("span");
    tag.className = "viewer-tag";
    tag.textContent = `#${value}`;
    return tag;
  });
  if (!tags.length) {
    const empty = document.createElement("span");
    empty.className = "viewer-tags-empty";
    empty.textContent = t("viewer.no_tags");
    children.push(empty);
  }
  const add = document.createElement("button");
  add.className = "viewer-add-tag";
  add.type = "button";
  add.textContent = t("viewer.add_tag");
  add.onclick = () => currentViewerItem && openTagDialog(currentViewerItem.id);
  children.push(add);
  list.replaceChildren(...children);
}

function renderRelatedImages(data) {
  const grid = $("#relatedGrid");
  grid.replaceChildren(...data.images.map(item => {
    const button = document.createElement("button");
    button.className = "related-card";
    button.type = "button";
    button.title = item.name;
    button.setAttribute("aria-label", item.name);
    const image = document.createElement("img");
    image.loading = "lazy";
    image.decoding = "async";
    if (item.width && item.height) {
      image.width = item.width;
      image.height = item.height;
    }
    loadImage(image, thumbnailURL(item), {fallback: item.url, label: ""});
    button.append(image);
    button.onclick = () => openImageViewer(item);
    button.onpointerenter = () => {
      clearTimeout(viewerPreviewTimer);
      viewerPreviewTimer = setTimeout(() => { preloadViewerPreview(item); }, 180);
    };
    button.onpointerleave = () => clearTimeout(viewerPreviewTimer);
    button.onpointerdown = () => {
      clearTimeout(viewerPreviewTimer);
      preloadViewerPreview(item);
    };
    bindImageContextMenu(button, item);
    return button;
  }));
  const status = $("#relatedStatus");
  if (!data.ready) status.textContent = t("viewer.preparing_similar");
  else if (!data.images.length && data.indexed < data.total) status.textContent = t("viewer.similar_later");
  else if (!data.images.length) status.textContent = t("viewer.no_similar");
  else if (data.indexed < data.total) status.textContent = t("viewer.similar_progress", {indexed: data.indexed, total: data.total});
  else status.textContent = "";
  status.hidden = !status.textContent;
}

async function refreshRelatedResults() {
  const item = currentViewerItem;
  if (!item || !$("#imageViewer").open) return;
  const loadId = relatedLoadId;
  try {
    const data = await request(`/api/app/related/${encodeURIComponent(item.id)}?limit=18`);
    if (loadId === relatedLoadId && currentViewerItem?.id === item.id) renderRelatedImages(data);
  } catch (_) {
    // Background refreshes stay quiet; the initial load reports useful errors.
  }
}

async function loadRelatedImages(item) {
  const loadId = ++relatedLoadId;
  $("#relatedGrid").replaceChildren();
  $("#relatedStatus").hidden = false;
  $("#relatedStatus").textContent = t("viewer.finding_similar");
  try {
    let data = await request(`/api/app/related/${encodeURIComponent(item.id)}?limit=18`);
    let state = null;
    if (!data.ready) {
      state = await request("/api/app/ai");
      const missing = state.missing.find(candidate => candidate.path === item.path);
      if (missing) {
        await saveSemanticEmbedding(missing);
        data = await request(`/api/app/related/${encodeURIComponent(item.id)}?limit=18`);
      }
    }
    if (loadId !== relatedLoadId || currentViewerItem?.id !== item.id) return;
    renderRelatedImages(data);
    if (data.indexed < data.total) {
      state ||= await request("/api/app/ai");
      continueSemanticIndex(state.missing, state.indexed, state.total);
    }
  } catch (error) {
    if (loadId !== relatedLoadId) return;
    $("#relatedStatus").hidden = false;
    $("#relatedStatus").textContent = t("viewer.similar_failed", {error: error.message});
  }
}

function renderImages(images, total = images.length) {
  const grid = $("#imageGrid");
  grid.classList.remove("is-loading");
  grid.removeAttribute("aria-busy");
  browserImages = images;
  grid.replaceChildren(...images.map(pictureCard));
  $("#imageCount").textContent = total ? `(${total})` : "";
  const libraryEmpty = !appState?.index || appState.index.count === 0;
  $("#imagesEmpty").hidden = !libraryEmpty || images.length !== 0;
  if (images.length || libraryEmpty) showResultState();
  else if (currentQuery) showResultState(t("search.no_results", {query: currentQuery}), t("search.try_fewer"), t("search.clear"), showAllImages);
  else if (currentTag || currentSource) showResultState(t("state.empty_folder"), t("state.empty_folder_text"), t("state.add_pictures"), () => { showMenuHome(); $("#showAdd").click(); });
  else showResultState(t("state.no_pictures"), t("state.try_view"), t("state.show_all"), showAllImages);
}

function showAllImages() {
  if (!currentQuery && !currentTag && !currentSource) {
    switchTab("images");
    return;
  }
  currentQuery = "";
  currentTag = "";
  currentSource = "";
  currentFolderName = "";
  clearSearchInput();
  renderSearchScope();
  loadImages();
}

function showLocalImages() {
  currentTag = "";
  currentSource = "";
  currentFolderName = "";
  currentQuery = searchQueryValue();
  renderSearchScope();
  loadImages();
}

async function loadImages() {
  const loadId = ++imageLoadId;
  renderSearchScope();
  switchTab("images");
  const preserveResults = Boolean($("#imageGrid .image-card:not(.image-card-skeleton)"));
  if (!preserveResults) setLoading();
  $("#imageGrid").setAttribute("aria-busy", "true");
  try {
    if (currentQuery) {
      const data = appState?.aiAvailable
        ? await semanticSearch(currentQuery)
        : await request(`/api/app/search?q=${encodeURIComponent(currentQuery)}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}&limit=120`);
      if (loadId !== imageLoadId) return;
      renderImages(data.images, data.images.length);
      if (data.preparing) {
        $("#imagesEmpty").hidden = true;
      } else if (!semanticIndexPromise && !semanticWarmupPromise) closeMessage();
    } else {
      const mode = currentTag || currentSource ? "recent" : sidebarMode;
      const count = sidebarMode === "random" ? 50 : 300;
      const data = await request(`/api/app/images?mode=${mode}&count=${count}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}`);
      if (loadId !== imageLoadId) return;
      if (sidebarMode === "unsorted" && !currentTag && !currentSource) {
        data.images = data.images.filter(item => !(item.tags || []).length);
        data.total = data.images.length;
      }
      renderImages(data.images, data.total);
    }
  } catch (error) {
    if (loadId !== imageLoadId) return;
    $("#imageGrid").removeAttribute("aria-busy");
    if (!preserveResults) {
      browserImages = [];
      $("#imageGrid").replaceChildren();
    }
    showResultState(t("state.load_failed"), error.message, t("state.try_again"), loadImages);
    showMessage(error.message, true, true);
  }
}

function openSidebar() {
  if (!appState?.plugins?.sidebar?.enabled) return;
  renderSidebar();
  $("#pluginSidebar").hidden = false;
  $("#pluginSidebar").setAttribute("aria-hidden", "false");
  document.body.classList.add("sidebar-open");
  $("#sidebarButton").setAttribute("aria-expanded", "true");
}

function closeSidebar() {
  $("#pluginSidebar").hidden = true;
  $("#pluginSidebar").setAttribute("aria-hidden", "true");
  document.body.classList.remove("sidebar-open");
  $("#sidebarButton").setAttribute("aria-expanded", "false");
}

function sidebarCollectionRow(tag) {
  const row = document.createElement("button");
  row.className = "sidebar-item sidebar-collection";
  row.type = "button";
  row.dataset.name = tag.name.toLowerCase();
  row.dataset.tag = tag.name;
  row.innerHTML = `<span>✎</span><span>${tag.name} <small>(${tag.count})</small></span>`;
  row.onclick = () => {
    sidebarMode = "recent";
    currentTag = tag.name;
    currentSource = "";
    currentQuery = searchQueryValue();
    loadImages();
    if (window.matchMedia("(max-width: 760px)").matches) closeSidebar();
  };
  row.ondragover = event => { event.preventDefault(); row.classList.add("is-drop-target"); };
  row.ondragleave = () => row.classList.remove("is-drop-target");
  row.ondrop = async event => {
    event.preventDefault();
    row.classList.remove("is-drop-target");
    const rawID = event.dataTransfer.getData("application/x-pictogrep-image-id") || event.dataTransfer.getData("text/plain");
    if (!/^[a-f0-9]{64}$/.test(rawID)) return;
    const imageId = rawID;
    try {
      await request("/api/app/tags", {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({action: "add", tag: tag.name, imageId}),
      });
      await refreshState();
      showMessage(t("sidebar.added_to", {name: tag.name}));
    } catch (error) { showMessage(error.message, true); }
  };
  return row;
}

async function renderSidebar() {
  const collections = $("#sidebarCollections");
  collections.replaceChildren(...(appState?.tags || []).map(sidebarCollectionRow));
  if (!appState?.tags?.length) {
    const empty = document.createElement("div");
    empty.className = "sidebar-empty";
    empty.textContent = t("sidebar.no_collections");
    collections.append(empty);
  }
  const boards = $("#sidebarBoards");
  try {
    const data = await request("/api/app/boards");
    boards.replaceChildren(...data.boards.map(board => {
      const link = document.createElement("a");
      link.className = "sidebar-item";
      link.href = board.url;
      link.target = "_blank";
      link.textContent = `▸  ${board.name}`;
      return link;
    }));
  } catch (_) { boards.replaceChildren(); }
  if (!boards.children.length) {
    const empty = document.createElement("div");
    empty.className = "sidebar-empty";
    empty.textContent = t("sidebar.no_storyboards");
    boards.append(empty);
  }
}

function showSidebarSmart(mode) {
  sidebarMode = mode;
  currentQuery = "";
  currentTag = "";
  currentSource = "";
  clearSearchInput();
  loadImages();
  if (window.matchMedia("(max-width: 760px)").matches) closeSidebar();
}

function renderCommonsImages(images) {
  const grid = $("#commonsGrid");
  grid.replaceChildren(...images.map(item => {
    const card = document.createElement("figure");
    card.className = "commons-image-card";
    const image = document.createElement("img");
    image.loading = "lazy";
    image.decoding = "async";
    loadImage(image, item.thumbnailUrl, {fallback: item.originalUrl, label: ""});
    const caption = document.createElement("figcaption");
    const source = document.createElement("a");
    source.href = item.descriptionUrl;
    source.target = "_blank";
    source.rel = "noopener";
    source.textContent = item.title;
    const sourceLabel = document.createElement("span");
    sourceLabel.className = "commons-source";
    sourceLabel.textContent = t("commons.view_source");
    caption.append(source, sourceLabel);
    card.append(image, caption);
    card.oncontextmenu = event => openCommonsContextMenu(event, item);
    return card;
  }));
  $("#commonsEmpty").hidden = images.length !== 0;
}

function openCommonsContextMenu(event, item) {
  event.preventDefault();
  event.stopPropagation();
  closeCardMenus();
  const menu = $("#commonsContextMenu");
  document.body.append(menu);
  menu.classList.add("cursor-menu");
  $("#commonsContextName").textContent = item.title;
  $("#commonsContextName").title = item.title;
  $("#commonsContextSource").href = item.descriptionUrl;
  $("#commonsContextOriginal").href = item.originalUrl;
  $("#commonsContextDownload").href = item.originalUrl;
  $("#commonsContextDownload").download = item.title;
  $("#commonsContextAdd").onclick = () => {
    closeCardMenus();
    const destination = {folder: "", source: "", label: "Pictogrep library"};
    queueImport(() => importDroppedURLs([item.originalUrl], destination));
  };
  menu.hidden = false;
  positionContextMenu(menu, event);
}

async function loadCommons() {
  switchTab("commons");
  const query = searchQueryValue();
  const grid = $("#commonsGrid");
  if (!grid.querySelector(".commons-image-card")) renderImageSkeletons(grid);
  grid.setAttribute("aria-busy", "true");
  $("#commonsEmpty").hidden = true;
  try {
    const data = await request(`/api/app/plugins/wikimedia/search?q=${encodeURIComponent(query)}&limit=40`);
    renderCommonsImages(data.images || []);
    grid.removeAttribute("aria-busy");
  } catch (error) {
    renderCommonsImages([]);
    grid.removeAttribute("aria-busy");
    showMessage(error.message, true);
  }
}

function calendarMonthQuery(value) {
  const match = value.trim().match(/^(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{4})$/i);
  if (!match) return "";
  const months = ["january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"];
  return `${match[2]}-${String(months.indexOf(match[1].toLowerCase()) + 1).padStart(2, "0")}`;
}

async function loadCalendar() {
  switchTab("calendar");
  const month = calendarMonthQuery(searchQueryValue());
  const calendarGroups = $("#calendarGroups");
  if (!calendarGroups.querySelector(".calendar-group")) {
    const grid = document.createElement("div");
    grid.className = "image-grid";
    renderImageSkeletons(grid);
    calendarGroups.replaceChildren(grid);
  }
  calendarGroups.setAttribute("aria-busy", "true");
  $("#calendarEmpty").hidden = true;
  try {
    const data = await request(`/api/app/plugins/calendar${month ? `?month=${month}` : ""}`);
    browserImages = (data.groups || []).flatMap(group => group.images || []);
    const groups = $("#calendarGroups");
    groups.removeAttribute("aria-busy");
    groups.replaceChildren(...(data.groups || []).map(group => {
      const section = document.createElement("section");
      section.className = "calendar-group";
      const heading = document.createElement("h2");
      heading.textContent = group.label + " ";
      const count = document.createElement("span");
      count.textContent = `(${t(group.count === 1 ? "calendar.count_one" : "calendar.count", {count: group.count})})`;
      heading.append(count);
      const grid = document.createElement("div");
      grid.className = "image-grid";
      grid.replaceChildren(...group.images.map(pictureCard));
      section.append(heading, grid);
      return section;
    }));
    $("#calendarEmpty").hidden = Boolean((data.groups || []).length);
  } catch (error) {
    $("#calendarGroups").replaceChildren();
    $("#calendarGroups").removeAttribute("aria-busy");
    $("#calendarEmpty").hidden = false;
    showMessage(error.message, true);
  }
}

function folderCard(folder) {
  const card = document.createElement("button");
  card.className = "folder-card";
  card.type = "button";
  const depth = Number(folder.depth) || 0;
  card.dataset.depth = String(depth);
  card.dataset.folderKind = folder.kind;
  card.dataset.folderValue = folder.value;
  card.dataset.folderName = folder.name;
  card.style.setProperty("--folder-depth", depth);
  card.title = folder.kind === "source" ? folder.value : folder.name;
  const preview = document.createElement("span");
  preview.className = `folder-preview images-${Math.min(4, folder.images.length)}`;
  folder.images.forEach(item => {
    const image = document.createElement("img");
    image.loading = "lazy";
    image.decoding = "async";
    loadImage(image, thumbnailURL(item, 640));
    preview.append(image);
  });
  if (!folder.images.length) {
    const placeholder = document.createElement("span");
    placeholder.className = "folder-placeholder";
    preview.append(placeholder);
  }
  const name = document.createElement("strong");
  name.textContent = folder.name;
  const count = document.createElement("small");
  count.textContent = t(folder.count === 1 ? "folders.count_one" : "folders.count", {count: folder.count});
  const details = document.createElement("span");
  details.className = "folder-details";
  details.append(name, count);
  card.append(preview, details);
  card.onclick = () => openFolder(folder);
  card.oncontextmenu = event => openFolderContextMenu(event, folder);
  return card;
}

function setFolderScope(folder) {
  currentTag = folder.kind === "tag" ? folder.value : "";
  currentSource = folder.kind === "source" ? folder.value : "";
  currentFolderName = folder.kind === "source" ? folder.value : folder.name;
  currentQuery = searchQueryValue();
  renderSearchScope();
}

function openFolder(folder) {
  closeCardMenus();
  setFolderScope(folder);
  loadImages();
}

function folderDestination(folder) {
  return folder.kind === "tag"
    ? {folder: folder.value, source: "", label: folder.name}
    : {folder: "", source: folder.value, label: folder.name};
}

function chooseImagesForFolder(folder) {
  closeCardMenus();
  const input = document.createElement("input");
  input.type = "file";
  input.accept = "image/*";
  input.multiple = true;
  input.onchange = () => {
    if (input.files?.length) queueImport(() => uploadFiles(input.files, {
      destination: folderDestination(folder), openMenu: false, showLibrary: !$("#imagesPanel").hidden,
    }));
  };
  input.click();
}

function openFolderContextMenu(event, folder) {
  event.preventDefault();
  event.stopPropagation();
  closeCardMenus();
  const menu = $("#folderContextMenu");
  $("#folderContextName").textContent = folder.name;
  $("#folderContextName").title = folder.kind === "source" ? folder.value : folder.name;
  $("#folderContextOpen").onclick = () => openFolder(folder);
  $("#folderContextAdd").onclick = () => chooseImagesForFolder(folder);
  $("#folderContextCanvas").onclick = () => {
    closeCardMenus();
    setFolderScope(folder);
    openFolderCanvas();
  };
  const newSubfolder = $("#folderContextNewSubfolder");
  newSubfolder.hidden = folder.kind !== "tag";
  newSubfolder.onclick = () => {
    closeCardMenus();
    openCreateFolder(folder.value);
  };
  const refresh = $("#folderContextRefresh");
  refresh.hidden = folder.kind !== "source";
  refresh.onclick = () => {
    closeCardMenus();
    failedSemanticPaths.clear();
    refreshLibraryIndex(folder.value, {announce: true, forceSemantic: true}).catch(() => {});
  };
  const copy = $("#folderContextCopyPath");
  copy.hidden = folder.kind !== "source";
  copy.onclick = async () => {
    closeCardMenus();
    try {
      await navigator.clipboard.writeText(folder.value);
      showMessage(t("folders.path_copied"));
    } catch (_) {
      window.prompt(t("folders.copy_path_prompt"), folder.value);
    }
  };
  menu.hidden = false;
  positionContextMenu(menu, event);
}

function newFolderCard() {
  const card = document.createElement("button");
  card.className = "folder-card new-folder";
  card.type = "button";
  card.dataset.depth = "0";
  const preview = document.createElement("span");
  preview.className = "folder-preview";
  const create = document.createElement("span");
  create.className = "folder-create-button";
  create.textContent = "+";
  const details = document.createElement("span");
  details.className = "folder-details";
  const name = document.createElement("strong");
  name.textContent = t("folders.create");
  details.append(name);
  preview.append(create);
  card.append(preview, details);
  card.onclick = () => openCreateFolder();
  return card;
}

async function loadFolders() {
  const list = $("#folderList");
  try {
    const data = await request("/api/app/folders");
    const parent = $("#newFolderParent");
    parent.replaceChildren(new Option(t("folders.top_level"), ""), ...(appState?.tags || []).map(tag => new Option(tag.name, tag.name)));
    list.replaceChildren(...data.folders.map(folderCard), newFolderCard());
    $("#foldersEmpty").hidden = true;
  } catch (error) {
    $("#foldersEmpty").hidden = false;
    showMessage(error.message, true);
  }
}

function canvasScope() {
  return currentTag
    ? {tag: currentTag, source: ""}
    : {tag: "", source: currentSource};
}

function canvasQuery() {
  const scope = canvasScope();
  return `tag=${encodeURIComponent(scope.tag)}&source=${encodeURIComponent(scope.source)}`;
}

function defaultCanvasPoint(index, total) {
  const columns = Math.max(1, Math.ceil(Math.sqrt(total * 1.35)));
  const rows = Math.max(1, Math.ceil(total / columns));
  return {
    x: (index % columns - (columns - 1) / 2) * 132,
    y: (Math.floor(index / columns) - (rows - 1) / 2) * 112,
  };
}

function applyCanvasTransform() {
  $("#canvasWorld").style.transform = `translate(${canvasPan.x}px, ${canvasPan.y}px) scale(${canvasZoom})`;
}

function canvasCard(item, point) {
  const card = document.createElement("button");
  card.className = "canvas-image";
  card.type = "button";
  card.dataset.id = String(item.id);
  card.title = item.name;
  card.style.left = `${point.x}px`;
  card.style.top = `${point.y}px`;
  const image = document.createElement("img");
  image.loading = "lazy";
  image.decoding = "async";
  image.draggable = false;
  loadImage(image, thumbnailURL(item));
  card.append(image);
  card.onpointerdown = event => {
    if (event.button !== 0) return;
    event.stopPropagation();
    card.setPointerCapture(event.pointerId);
    canvasPointer = {kind: "image", id: item.id, startX: event.clientX, startY: event.clientY, x: point.x, y: point.y, moved: false, card};
  };
  card.onpointermove = moveCanvasPointer;
  card.onpointerup = endCanvasPointer;
  card.onpointercancel = endCanvasPointer;
  bindImageContextMenu(card, item);
  return card;
}

function renderCanvas() {
  const world = $("#canvasWorld");
  world.replaceChildren(...canvasImages.map((item, index) => {
    let point = canvasPositions.get(item.id);
    if (!point) {
      point = defaultCanvasPoint(index, canvasImages.length);
      canvasPositions.set(item.id, point);
    }
    return canvasCard(item, point);
  }));
}

function moveCanvasPointer(event) {
  if (!canvasPointer) return;
  const dx = event.clientX - canvasPointer.startX;
  const dy = event.clientY - canvasPointer.startY;
  if (Math.abs(dx) + Math.abs(dy) > 3) canvasPointer.moved = true;
  if (canvasPointer.kind === "image") {
    const point = {x: canvasPointer.x + dx / canvasZoom, y: canvasPointer.y + dy / canvasZoom};
    canvasPositions.set(canvasPointer.id, point);
    canvasPointer.card.style.left = `${point.x}px`;
    canvasPointer.card.style.top = `${point.y}px`;
  } else {
    canvasPan = {x: canvasPointer.x + dx, y: canvasPointer.y + dy};
    applyCanvasTransform();
  }
}

function endCanvasPointer(event) {
  if (!canvasPointer) return;
  const finished = canvasPointer;
  if (finished.kind === "image" && finished.moved) scheduleCanvasSave();
  if (event.currentTarget.hasPointerCapture?.(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  canvasPointer = null;
  if (finished.kind === "image" && !finished.moved) {
    const item = canvasImages.find(candidate => candidate.id === finished.id);
    if (item) openImageViewer(item);
  }
}

function canvasSavePayload() {
  const scope = canvasScope();
  const positions = canvasImages.map(item => ({id: item.id, ...canvasPositions.get(item.id)}));
  return {...scope, positions};
}

function scheduleCanvasSave() {
  clearTimeout(canvasSaveTimer);
  $("#canvasStatus").textContent = t("canvas.saving");
  const payload = canvasSavePayload();
  canvasSaveTimer = setTimeout(() => saveCanvas(payload), 350);
}

async function saveCanvas(payload = canvasSavePayload()) {
  try {
    await request("/api/app/canvas", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload),
    });
    $("#canvasStatus").textContent = t("canvas.saved");
  } catch (error) {
    $("#canvasStatus").textContent = error.message;
  }
}

async function openFolderCanvas() {
  if (!currentTag && !currentSource) return;
  const dialog = $("#canvasDialog");
  canvasImages = [];
  canvasPositions = new Map();
  $("#canvasWorld").replaceChildren();
  $("#canvasStatus").textContent = t("canvas.loading");
  dialog.showModal();
  try {
    const data = await request(`/api/app/canvas?${canvasQuery()}`);
    canvasImages = data.images;
    for (const [id, point] of Object.entries(data.positions || {})) canvasPositions.set(id, point);
    canvasZoom = 1;
    const viewport = $("#canvasViewport");
    canvasPan = {x: viewport.clientWidth / 2, y: viewport.clientHeight / 2};
    renderCanvas();
    applyCanvasTransform();
    $("#canvasStatus").textContent = t(canvasImages.length === 1 ? "canvas.count_one" : "canvas.count", {count: canvasImages.length});
  } catch (error) {
    $("#canvasStatus").textContent = error.message;
  }
}

function renderSearchIndexSettings() {
  const panel = $("#searchIndexStatus");
  if (!panel || !appState) return;
  const server = appState.searchIndex || {indexed: 0, total: 0, pending: 0, automatic: true};
  let status = semanticIndexStatus;
  if (!semanticIndexPromise && status.state === "idle") {
    const state = !server.total ? "empty" : server.pending ? (server.automatic ? "queued" : "paused") : "ready";
    status = {state, indexed: server.indexed || 0, total: server.total || 0, error: ""};
  }
  const indexed = status.indexed || 0;
  const total = status.total || 0;
  const pending = Math.max(0, total - indexed);
  let title;
  let detail;
  switch (status.state) {
  case "empty":
    title = t("index.empty");
    detail = t("index.empty_help");
    break;
  case "ready":
    title = t("index.ready");
    detail = t("index.ready_help", {total});
    break;
  case "indexing":
    title = t("index.working");
    detail = t("index.working_help", {indexed, total});
    break;
  case "paused":
    title = t("index.paused");
    detail = t("index.paused_help", {pending});
    break;
  case "partial":
    title = t("index.ready");
    detail = t("index.available_help", {indexed, total});
    break;
  case "error":
    title = t("index.failed");
    detail = t("index.failed_help", {error: status.error || "Unknown error"});
    break;
  default:
    title = t("index.queued");
    detail = t("index.working_help", {indexed, total});
  }
  panel.classList.toggle("error", status.state === "error");
  $("#searchIndexStatusTitle").textContent = title;
  $("#searchIndexStatusText").textContent = detail;
  const progress = $("#searchIndexProgress");
  progress.max = total || 1;
  progress.value = indexed;
  $("#automaticIndexingSetting").checked = server.automatic !== false;
  $("#checkIndexNow").disabled = appState.indexJob?.state === "running" || fullReindexRunning;
  $("#rebuildSearchIndex").disabled = fullReindexRunning;
}

function renderState() {
  if (!appState) return;
  const stats = appState.index;
  $("#indexSummary").textContent = stats ? t("library.count", {count: stats.count}) : t("library.empty");
  $("#boardCount").textContent = appState.boards ? `(${appState.boards})` : "";
  $("#languageSetting").value = window.PictogrepI18n.locale();
  $("#thumbnailSizeSetting").value = appState.browser?.thumbnailSize || "medium";
  $("#showFilenamesSetting").checked = Boolean(appState.browser?.showFilenames);
  $("#homeOrderSetting").value = appState.browser?.homeOrder || "random";
  $("#libraryLocation").textContent = appState.paths?.library || "";
  document.body.dataset.thumbnailSize = appState.browser?.thumbnailSize || "medium";
  document.body.classList.toggle("show-filenames", Boolean(appState.browser?.showFilenames));
  const wikimediaEnabled = Boolean(appState.plugins?.wikimedia?.enabled);
  $("#commonsTab").hidden = !wikimediaEnabled;
  $("#wikimediaPluginToggle").checked = wikimediaEnabled;
  const calendarEnabled = Boolean(appState.plugins?.calendar?.enabled);
  $("#calendarTab").hidden = !calendarEnabled;
  $("#calendarPluginToggle").checked = calendarEnabled;
  const sidebarEnabled = Boolean(appState.plugins?.sidebar?.enabled);
  $("#sidebarButton").hidden = !sidebarEnabled;
  $("#sidebarPluginToggle").checked = sidebarEnabled;
  if (sidebarEnabled && !$("#pluginSidebar").hidden) renderSidebar();
  if (!sidebarEnabled) closeSidebar();
  $("#vimPluginToggle").checked = Boolean(appState.plugins?.vim?.enabled);
  $("#commandPalettePluginToggle").checked = Boolean(appState.plugins?.commandPalette?.enabled);
  const pinterestEnabled = Boolean(appState.plugins?.pinterest?.enabled);
  $("#pinterestPluginToggle").checked = pinterestEnabled;
  $("#showPinterest").hidden = !pinterestEnabled;
  if (!pinterestEnabled) $("#pinterestSection").hidden = true;
  const pinterestAvailable = appState.plugins?.pinterest?.available !== false;
  $("#pinterestReadiness").hidden = pinterestAvailable;
  $("#pinterestImportButton").disabled = !pinterestAvailable || Boolean(pinterestImportController);

  const options = $("#tagOptions");
  options.replaceChildren(...appState.tags.map(tag => {
    const option = document.createElement("option");
    option.value = tag.name;
    return option;
  }));
  const choices = $("#tagChoices");
  choices.replaceChildren(...appState.tags.map(tag => {
    const choice = document.createElement("button");
    choice.type = "button";
    choice.textContent = `${tag.name} (${tag.count})`;
    choice.onclick = () => {
      $("#tagName").value = tag.name;
      $("#tagForm").requestSubmit();
    };
    return choice;
  }));
  choices.hidden = !appState.tags.length;
  const job = appState.indexJob;
  const active = job.state === "running";
  $("#indexStatus").hidden = !active && job.state !== "error";
  $("#indexMessage").textContent = job.message || "";
  $("#indexProgress").max = job.total || 1;
  $("#indexProgress").value = job.current || 0;
  renderSearchIndexSettings();
}

async function refreshState() {
  try {
    const previous = lastJobState;
    appState = await request("/api/app/state");
    lastJobState = appState.indexJob.state;
    renderState();
    if (lastJobState === "running") {
      clearTimeout(pollTimer);
      pollTimer = setTimeout(refreshState, 800);
    } else if (previous === "running") {
      if (lastJobState === "complete") {
        lastLibraryRefreshAt = Date.now();
        if (indexJobAnnounce) showMessage(appState.indexJob.message);
        await loadImages();
        await loadFolders();
        failedSemanticPaths.clear();
        scheduleSemanticIndex(250, forceIndexAfterRefresh);
      } else {
        if (indexJobAnnounce) showMessage(appState.indexJob.message, true, true);
        else logBackground("library-refresh", appState.indexJob.message);
      }
      indexJobAnnounce = false;
      forceIndexAfterRefresh = false;
    }
  } catch (error) {
    if (indexJobAnnounce) showMessage(error.message, true, true);
    else logBackground("state-refresh", error.message);
  }
}

async function startIndex(payload, options = {}) {
  try {
    semanticResults.clear();
    const data = await request("/api/app/index", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload),
    });
    indexJobAnnounce ||= Boolean(options.announce);
    forceIndexAfterRefresh ||= Boolean(options.forceSemantic);
    lastJobState = "running";
    await refreshState();
    return data;
  } catch (error) {
    if (options.announce) showMessage(error.message, true, true);
    throw error;
  }
}

function refreshLibraryIndex(folder = "", options = {}) {
  if (appState?.indexJob?.state === "running") return Promise.resolve();
  return startIndex({mode: "incremental", folder}, options);
}

function isSupportedImage(file) {
  return /\.(jpe?g|png|webp|gif)$/i.test(file.name) || ["image/jpeg", "image/png", "image/gif", "image/webp"].includes(file.type);
}

function preparedUpload(file) {
  // Imported bytes and filenames are always preserved. Optimized browsing
  // previews live only in Pictogrep's disposable thumbnail cache.
  if (/\.(jpe?g|png|webp|gif)$/i.test(file.name)) return {file, name: file.name};
  const extension = {"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}[file.type] || "";
  return {file, name: `${file.name || "dropped-image"}${extension}`};
}

function activeImportDestination(target = null) {
  const element = target instanceof Element ? target : null;
  const sidebarFolder = element?.closest(".sidebar-collection");
  if (sidebarFolder?.dataset.tag) return {folder: sidebarFolder.dataset.tag, source: "", label: sidebarFolder.dataset.tag};
  const folderCard = element?.closest(".folder-card[data-folder-kind]");
  if (folderCard) {
    const label = folderCard.dataset.folderName || folderCard.dataset.folderValue;
    if (folderCard.dataset.folderKind === "tag") return {folder: folderCard.dataset.folderValue, source: "", label};
    if (folderCard.dataset.folderKind === "source") return {folder: "", source: folderCard.dataset.folderValue, label};
  }
  if (!$("#imagesPanel").hidden && currentTag) return {folder: currentTag, source: "", label: currentFolderName || currentTag};
  if (!$("#imagesPanel").hidden && currentSource) return {folder: "", source: currentSource, label: currentFolderName || currentSource};
  return {folder: "", source: "", label: t("import.library")};
}

function uploadURL(name, destination) {
  const parameters = new URLSearchParams({name});
  if (destination.folder) parameters.set("folder", destination.folder);
  if (destination.source) parameters.set("source", destination.source);
  return `/api/app/upload?${parameters}`;
}

function uploadFileWithProgress(file, name, destination, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", uploadURL(name, destination));
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    xhr.upload.onprogress = event => {
      if (event.lengthComputable) onProgress(event.loaded / event.total);
    };
    xhr.onerror = () => reject(new Error(t("import.interrupted")));
    xhr.onload = () => {
      let data;
      try { data = JSON.parse(xhr.responseText); }
      catch (_) { return reject(new Error(`Pictogrep returned an invalid response (${xhr.status})`)); }
      if (xhr.status < 200 || xhr.status >= 300 || data.ok === false) {
        const error = new Error(data.error || `Request failed (${xhr.status})`);
        error.status = xhr.status;
        error.data = data;
        reject(error);
      } else resolve(data);
    };
    xhr.send(file);
  });
}

function beginImportProgress(total, destination) {
  clearTimeout(importProgressTimer);
  const panel = $("#importProgress");
  panel.hidden = false;
  panel.className = "import-progress";
  $("#closeImportProgress").hidden = true;
  $("#importProgressTitle").textContent = t(total === 1 ? "import.adding_one" : "import.adding_many", {count: total});
  $("#importProgressName").textContent = destination.label;
  const bar = $("#importProgressBar");
  bar.max = 100;
  bar.value = 0;
  $("#importProgressStatus").textContent = t("import.preparing");
}

function updateImportProgress(index, total, name, fraction, status, indeterminate = false) {
  $("#importProgressName").textContent = name;
  $("#importProgressStatus").textContent = status;
  const bar = $("#importProgressBar");
  if (indeterminate) bar.removeAttribute("value");
  else bar.value = Math.max(0, Math.min(100, ((index + fraction) / total) * 100));
}

function finishImportProgress(saved, duplicates, failed, total, destination, lastError = "") {
  const panel = $("#importProgress");
  const bar = $("#importProgressBar");
  bar.value = 100;
  $("#closeImportProgress").hidden = false;
  const details = [];
  if (saved) details.push(t("import.added", {count: saved}));
  if (duplicates) details.push(t(duplicates === 1 ? "import.duplicate_one" : "import.duplicates", {count: duplicates}));
  if (failed) details.push(t("import.failed", {count: failed}));
  $("#importProgressName").textContent = destination.label;
  $("#importProgressStatus").textContent = `${details.join(" · ") || t("import.nothing_added")}${failed && lastError ? ` — ${lastError}` : ""}`;
  if (failed) {
    panel.classList.add("error");
    $("#importProgressTitle").textContent = t(saved ? "import.partial" : "import.could_not_add");
  } else {
    panel.classList.add("success");
    $("#importProgressTitle").textContent = t(duplicates === total ? "import.already_present" : total === 1 ? "import.added_one_title" : "import.added_many_title");
  }
  importProgressTimer = setTimeout(() => { panel.hidden = true; }, 10000);
}

async function refreshAfterImport(showLibrary) {
  semanticResults.clear();
  await refreshState();
  if (showLibrary) await loadImages();
  await loadFolders();
  scheduleSemanticIndex(250);
}

async function uploadFiles(files, options = {}) {
  const images = Array.from(files).filter(isSupportedImage);
  if (!images.length) return showMessage(t("import.choose_supported"), true);
  const destination = options.destination || activeImportDestination();
  const showLibrary = options.showLibrary ?? !$("#imagesPanel").hidden;
  if (options.openMenu !== false) openMenu();
  beginImportProgress(images.length, destination);
  let saved = 0;
  let duplicates = 0;
  let failed = 0;
  let lastError = "";
  let changed = false;
  for (let index = 0; index < images.length; index++) {
    const original = images[index];
    const upload = preparedUpload(original);
    updateImportProgress(index, images.length, original.name || upload.name, 0, t("import.uploading", {current: index + 1, total: images.length}));
    try {
      const result = await uploadFileWithProgress(upload.file, upload.name, destination, fraction => {
        updateImportProgress(index, images.length, original.name || upload.name, fraction, t("import.uploading", {current: index + 1, total: images.length}));
      });
      if (result.duplicate) duplicates++;
      else saved++;
      changed ||= !result.duplicate || Boolean(result.linked);
    } catch (error) {
      if (error.status === 409) duplicates++;
      else { failed++; lastError = error.message; }
    }
  }
  if (changed) await refreshAfterImport(showLibrary);
  finishImportProgress(saved, duplicates, failed, images.length, destination, lastError);
}

function droppedURLName(rawURL) {
  try {
    const parsed = new URL(rawURL);
    const name = decodeURIComponent(parsed.pathname.split("/").filter(Boolean).pop() || t("import.dropped_image"));
    return name.length > 80 ? `${name.slice(0, 77)}…` : name;
  } catch (_) { return t("import.dropped_image"); }
}

function dataURLFile(rawURL) {
  return fetch(rawURL).then(async response => {
    const blob = await response.blob();
    const extension = {"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}[blob.type];
    if (!extension) throw new Error(t("import.unsupported_drop_format"));
    return new File([blob], `dropped-image${extension}`, {type: blob.type});
  });
}

async function importDroppedURLs(urls, destination) {
  const showLibrary = !$("#imagesPanel").hidden;
  beginImportProgress(urls.length, destination);
  let saved = 0;
  let duplicates = 0;
  let failed = 0;
  let lastError = "";
  let changed = false;
  for (let index = 0; index < urls.length; index++) {
    const rawURL = urls[index];
    const name = droppedURLName(rawURL);
    try {
      if (rawURL.startsWith("data:image/")) {
        const file = await dataURLFile(rawURL);
        updateImportProgress(index, urls.length, name, 0, t("import.adding_progress", {current: index + 1, total: urls.length}));
        const upload = preparedUpload(file);
        const result = await uploadFileWithProgress(upload.file, upload.name, destination, fraction => {
          updateImportProgress(index, urls.length, name, fraction, t("import.adding_progress", {current: index + 1, total: urls.length}));
        });
        if (result.duplicate) duplicates++;
        else saved++;
        changed ||= !result.duplicate || Boolean(result.linked);
      } else {
        updateImportProgress(index, urls.length, name, 0, t("import.downloading_checking"), true);
        const result = await request("/api/app/import-url", {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({url: rawURL, folder: destination.folder, source: destination.source}),
        });
        if (result.duplicate) duplicates++;
        else saved++;
        changed ||= !result.duplicate || Boolean(result.linked);
      }
      updateImportProgress(index, urls.length, name, 1, t("import.saved"));
    } catch (error) {
      if (error.status === 409) duplicates++;
      else { failed++; lastError = error.message; }
      updateImportProgress(index, urls.length, name, 1, error.message);
    }
  }
  if (changed) await refreshAfterImport(showLibrary);
  finishImportProgress(saved, duplicates, failed, urls.length, destination, lastError);
}

function dataTransferTypes(dataTransfer) {
  return Array.from(dataTransfer?.types || [], value => String(value).toLowerCase());
}

function isExternalImageDrop(dataTransfer) {
  const types = dataTransferTypes(dataTransfer);
  if (types.includes("application/x-pictogrep-image-id")) return false;
  if (Array.from(dataTransfer?.files || []).length || Array.from(dataTransfer?.items || []).some(item => item.kind === "file")) return true;
  return types.some(type => type === "files" || type === "text/html" || type === "downloadurl" ||
    type.startsWith("text/plain") || type.includes("uri-list") || type.includes("x-moz-url") ||
    type.includes("file-promise-url") || type === "x-special/gnome-copied-files");
}

function extractDroppedURLs(dataTransfer) {
  const found = new Set();
  const add = value => {
    value = String(value || "").trim();
    if (/^https?:\/\//i.test(value) || /^data:image\/(?:jpeg|png|gif|webp)[;,]/i.test(value)) found.add(value);
  };
  const read = type => {
    try { return dataTransfer.getData(type); }
    catch (_) { return ""; }
  };

  const html = read("text/html");
  if (html) {
    const documentFragment = new DOMParser().parseFromString(html, "text/html");
    documentFragment.querySelectorAll("img[src]").forEach(image => add(image.getAttribute("src")));
    // Chromium and Firefox also include the surrounding page URL in the same
    // drag payload. Once an actual <img> source is present, importing those
    // navigation URLs would produce a misleading partial failure.
    if (found.size) return Array.from(found).slice(0, 20);
  }
  for (const type of ["text/uri-list", "text/x-moz-url-data", "application/x-moz-file-promise-url"]) {
    read(type).split(/\r?\n/).filter(line => line && !line.startsWith("#")).forEach(add);
  }
  for (const type of Array.from(dataTransfer.types || [])) {
    const lower = String(type).toLowerCase();
    if (lower.startsWith("text/plain") || lower.includes("uri-list") || lower === "x-special/gnome-copied-files") {
      read(type).split(/\r?\n/).filter(line => line && line !== "copy" && !line.startsWith("#")).forEach(add);
    }
  }
  const mozURL = read("text/x-moz-url");
  if (mozURL) add(mozURL.split(/\r?\n/)[0]);
  const downloadURL = read("DownloadURL");
  const downloadMatch = downloadURL.match(/^[^:]+:[^:]*:(https?:\/\/.*)$/i);
  if (downloadMatch) add(downloadMatch[1]);
  add(read("text/plain").split(/\r?\n/)[0]);
  return Array.from(found).slice(0, 20);
}

function showDropOverlay(destination) {
  $("#dropDestination").textContent = t("import.add_to", {name: destination.label});
  $("#dropOverlay").hidden = false;
  $("#dropOverlay").setAttribute("aria-hidden", "false");
}

function hideDropOverlay() {
  externalDragDepth = 0;
  $("#dropOverlay").hidden = true;
  $("#dropOverlay").setAttribute("aria-hidden", "true");
  document.querySelectorAll(".is-drop-target").forEach(element => element.classList.remove("is-drop-target"));
}

function queueImport(task) {
  importQueue = importQueue.then(task, task);
}

function transferImageFiles(dataTransfer) {
  const files = Array.from(dataTransfer?.files || []).filter(file => file.size > 0 && isSupportedImage(file));
  if (files.length) return files;
  const itemFiles = Array.from(dataTransfer?.items || [])
    .filter(item => item.kind === "file")
    .map(item => item.getAsFile())
    .filter(file => file && file.size > 0 && isSupportedImage(file));
  return itemFiles;
}

function queueTransferImport(dataTransfer, destination) {
  const files = transferImageFiles(dataTransfer);
  if (files.length) {
    queueImport(() => uploadFiles(files, {destination, openMenu: false, showLibrary: !$("#imagesPanel").hidden}));
    return true;
  }
  const urls = extractDroppedURLs(dataTransfer);
  if (urls.length) {
    queueImport(() => importDroppedURLs(urls, destination));
    return true;
  }
  return false;
}

function openCreateFolder(parent = "") {
  $("#newFolderName").value = "";
  $("#newFolderPrompt").value = "";
  $("#newFolderFiles").value = "";
  $("#newFolderParent").value = parent;
  $("#folderDialog").showModal();
  $("#newFolderName").focus();
}

async function createFolder(event) {
  event.preventDefault();
  const leafName = $("#newFolderName").value.trim();
  const parent = $("#newFolderParent").value;
  const name = parent ? `${parent}/${leafName}` : leafName;
  const prompt = $("#newFolderPrompt").value.trim();
  const files = Array.from($("#newFolderFiles").files || []).filter(isSupportedImage);
  if (!leafName) return;
  try {
    await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "create", tag: name}),
    });
    let saved = 0;
    let skipped = 0;
    for (let index = 0; index < files.length; index++) {
      const original = files[index];
      const upload = preparedUpload(original);
      showMessage(t("folder_dialog.adding", {current: index + 1, total: files.length, name: original.name}), false, true);
      try {
        await request(`/api/app/upload?name=${encodeURIComponent(upload.name)}&folder=${encodeURIComponent(name)}`, {
          method: "POST",
          headers: {"Content-Type": upload.file.type || original.type || "application/octet-stream"},
          body: upload.file,
        });
        saved++;
      } catch (_) {
        skipped++;
      }
    }
    let filled = 0;
    let indexed = 0;
    let total = 0;
    if (prompt) {
      showMessage(t("folder_dialog.finding", {query: prompt}), false, true);
      const vector = await semanticVector(prompt);
      const result = await request("/api/app/tags", {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({action: "fill", model: appState.embeddingModel.key, tag: name, prompt, limit: 50, vector}),
      });
      filled = result.added;
      indexed = result.indexed;
      total = result.total;
      const state = await request("/api/app/ai");
      continueSemanticIndex(state.missing, state.indexed, state.total);
    }
    if (saved || filled) semanticResults.clear();
    $("#folderDialog").close();
    await refreshState();
    await loadImages();
    await loadFolders();
    if (saved) scheduleSemanticIndex(250);
    if (prompt && indexed === 0) showMessage(t("folder_dialog.created_waiting", {name}), true, true);
    else if (prompt && indexed < total) showMessage(t("folder_dialog.created_indexing", {name, count: filled + saved, indexed}));
    else if (prompt) showMessage(t("folder_dialog.created_search", {name, count: filled + saved, query: prompt}));
    else if (skipped) showMessage(t("folder_dialog.created_skipped", {name, count: saved, skipped}));
    else if (saved) showMessage(t(saved === 1 ? "folder_dialog.created_one" : "folder_dialog.created_many", {name, count: saved}));
    else showMessage(t("folder_dialog.created", {name}));
  } catch (error) {
    showMessage(error.message, true, true);
  }
}

function openTagDialog(imageId) {
  $("#tagImageId").value = imageId;
  $("#tagName").value = "";
  $("#tagDialog").showModal();
  $("#tagName").focus();
}

function openDeleteImageDialog(item) {
  closeCardMenus();
  pendingDeleteItem = item;
  $("#deleteImageName").textContent = item.name;
  $("#deleteImagePath").textContent = item.path || t("delete.original_file");
  const confirmButton = $("#deleteImageForm .confirm-delete");
  confirmButton.disabled = false;
  confirmButton.textContent = t("delete.confirm");
  $("#deleteImageDialog").showModal();
}

async function deleteConfirmedImage(event) {
  event.preventDefault();
  const item = pendingDeleteItem;
  if (!item) return;
  const confirmButton = $("#deleteImageForm .confirm-delete");
  confirmButton.disabled = true;
  confirmButton.textContent = t("delete.deleting");
  try {
    await request(`/api/app/images/${encodeURIComponent(item.id)}`, {
      method: "DELETE",
      headers: {"Content-Type": "application/json", "X-Pictogrep-Action": "delete-image"},
      body: JSON.stringify({path: item.path}),
    });
    $("#deleteImageDialog").close();
    if (currentViewerItem?.id === item.id) closeImageViewer();
    pendingDeleteItem = null;
    semanticResults.clear();
    await refreshState();
    await loadImages();
    await loadFolders();
    showMessage(t("delete.deleted", {name: item.name}));
  } catch (error) {
    showMessage(error.message, true, true);
    confirmButton.disabled = false;
    confirmButton.textContent = t("delete.confirm");
  }
}

async function saveTag(event) {
  event.preventDefault();
  const tag = $("#tagName").value.trim();
  if (!tag) return;
  try {
    const imageId = $("#tagImageId").value;
    const result = await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "add", tag, imageId}),
    });
    if (currentViewerItem?.id === imageId) {
      currentViewerItem.tags ||= [];
      if (!currentViewerItem.tags.includes(result.tag)) currentViewerItem.tags.push(result.tag);
      renderViewerTags(currentViewerItem.tags);
    }
    semanticResults.clear();
    $("#tagDialog").close();
    showMessage(t("tag_dialog.added", {name: tag}));
    await refreshState();
    await loadImages();
    await loadFolders();
  } catch (error) {
    showMessage(error.message, true);
  }
}

async function loadBoards() {
  $("#addSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#boardsSection").hidden = false;
  openMenu();
  try {
    const data = await request("/api/app/boards");
    const list = $("#boardList");
    list.replaceChildren(...data.boards.map(board => {
      const card = document.createElement("a");
      card.className = "board-card";
      card.href = board.url;
      card.target = "_blank";
      const image = document.createElement("img");
      image.src = board.url;
      image.alt = board.name;
      image.loading = "lazy";
      const name = document.createElement("span");
      name.textContent = board.name;
      card.append(image, name);
      return card;
    }));
    $("#boardsEmpty").hidden = data.boards.length !== 0;
  } catch (error) {
    showMessage(error.message, true);
  }
}

function showAbout() {
	$("#addSection").hidden = true;
	$("#pinterestSection").hidden = true;
	$("#settingsSection").hidden = true;
	$("#pluginsSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = false;
  $("#currentVersion").textContent = appState?.version ? `v${appState.version}` : t("about.unknown");
  $("#updateMethod").textContent = appState?.updateMethod || "GitHub Releases";
  openMenu();
}

function showSettings() {
  $("#addSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#settingsSection").hidden = false;
  renderState();
  openMenu(t("settings.title"));
}

function showPlugins() {
  $("#addSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = false;
  renderState();
  openMenu(t("menu.plugins"));
}

async function toggleWikimediaPlugin() {
  const enabled = $("#wikimediaPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "wikimedia", enabled}),
    });
    appState.plugins.wikimedia.enabled = enabled;
    renderState();
    if (!enabled && !$("#commonsPanel").hidden) showLocalImages();
  } catch (error) {
    $("#wikimediaPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

async function toggleCalendarPlugin() {
  const enabled = $("#calendarPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "calendar", enabled}),
    });
    appState.plugins.calendar.enabled = enabled;
    renderState();
    if (!enabled && !$("#calendarPanel").hidden) showLocalImages();
  } catch (error) {
    $("#calendarPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

async function toggleSidebarPlugin() {
  const enabled = $("#sidebarPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "sidebar", enabled}),
    });
    appState.plugins.sidebar.enabled = enabled;
    renderState();
  } catch (error) {
    $("#sidebarPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

async function toggleVimPlugin() {
  const enabled = $("#vimPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "vim", enabled}),
    });
    appState.plugins.vim = {enabled};
    renderState();
    showMessage(t(enabled ? "plugins.vim_enabled" : "plugins.vim_disabled"));
  } catch (error) {
    $("#vimPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

async function toggleCommandPalettePlugin() {
  const enabled = $("#commandPalettePluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "commandPalette", enabled}),
    });
    appState.plugins.commandPalette = {enabled};
    renderState();
    if (!enabled && $("#commandPaletteDialog").open) $("#commandPaletteDialog").close();
  } catch (error) {
    $("#commandPalettePluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

async function togglePinterestPlugin() {
  const enabled = $("#pinterestPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "pinterest", enabled}),
    });
    appState.plugins.pinterest = {...appState.plugins.pinterest, enabled};
    renderState();
    showMessage(t(enabled ? "pinterest.enabled" : "pinterest.disabled"));
  } catch (error) {
    $("#pinterestPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

function showPinterestImport() {
  $("#addSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#pinterestSection").hidden = false;
  openMenu(t("pinterest.drawer_title"));
  if (!pinterestImportController) $("#pinterestBoardURL").focus();
}

async function importPinterestBoard(event) {
  event.preventDefault();
  if (pinterestImportController) return;
  const button = $("#pinterestImportButton");
  const resultBox = $("#pinterestImportResult");
  const rawURL = $("#pinterestBoardURL").value.trim();
  const mode = document.querySelector('input[name="pinterestMode"]:checked')?.value || "board";
  const boardInput = $("#pinterestBoardURL");
  let parsedURL;
  try { parsedURL = new URL(rawURL); }
  catch (_) { parsedURL = null; }
  if (!parsedURL || !/(^|\.)pinterest\.[a-z.]+$/i.test(parsedURL.hostname) || parsedURL.pathname.split("/").filter(Boolean).length < 2) {
    boardInput.setAttribute("aria-invalid", "true");
    $("#pinterestResultIcon").textContent = "!";
    $("#pinterestResultTitle").textContent = t("pinterest.invalid_title");
    $("#pinterestResultDetails").textContent = t("pinterest.invalid_help");
    $("#pinterestOpenFolder").hidden = true;
    $("#pinterestImportAnother").hidden = true;
    resultBox.classList.add("error");
    resultBox.hidden = false;
    boardInput.focus();
    return;
  }
  boardInput.removeAttribute("aria-invalid");
  pinterestImportController = new AbortController();
  button.disabled = true;
  button.textContent = t("pinterest.downloading");
  $("#pinterestCancelImport").hidden = false;
  $("#pinterestWorking").hidden = false;
  $("#pinterestImportForm").querySelectorAll("input").forEach(input => { input.disabled = true; });
  resultBox.hidden = true;
  resultBox.classList.remove("error");
  try {
    const result = await request("/api/app/plugins/pinterest/import", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      signal: pinterestImportController.signal,
      body: JSON.stringify({
        url: rawURL,
        mode,
        skipExisting: $("#pinterestSkipExisting").checked,
      }),
    });
    if (result.imported || result.linked) await refreshAfterImport(true);
    const details = [t("pinterest.result_added", {count: result.imported})];
    if (result.skipped) details.push(t("pinterest.result_skipped", {count: result.skipped}));
    if (result.linked) details.push(t("pinterest.result_linked", {count: result.linked}));
    if (result.failed) details.push(t("pinterest.result_failed", {count: result.failed}));
    lastPinterestFolder = result.folder || "";
    $("#pinterestResultIcon").textContent = result.failed ? "!" : "✓";
    $("#pinterestResultTitle").textContent = t(result.failed ? "pinterest.result_partial" : "pinterest.result_success", {board: result.board});
    $("#pinterestResultDetails").textContent = details.join(" · ");
    $("#pinterestOpenFolder").hidden = !lastPinterestFolder;
    $("#pinterestImportAnother").hidden = false;
    resultBox.classList.toggle("error", Boolean(result.failed));
    resultBox.hidden = false;
    showMessage(t("pinterest.imported_notice", {board: result.board}));
  } catch (error) {
    const cancelled = error.name === "AbortError";
    $("#pinterestResultIcon").textContent = cancelled ? "×" : "!";
    $("#pinterestResultTitle").textContent = t(cancelled ? "pinterest.cancelled" : "pinterest.failed_title");
    $("#pinterestResultDetails").textContent = cancelled ? t("pinterest.cancelled_help") : error.message;
    $("#pinterestOpenFolder").hidden = true;
    $("#pinterestImportAnother").hidden = false;
    resultBox.classList.add("error");
    resultBox.hidden = false;
  } finally {
    pinterestImportController = null;
    $("#pinterestWorking").hidden = true;
    $("#pinterestCancelImport").hidden = true;
    $("#pinterestImportForm").querySelectorAll("input").forEach(input => { input.disabled = false; });
    button.disabled = appState.plugins?.pinterest?.available === false;
    button.textContent = t("pinterest.download_all");
  }
}

function resetPinterestImport() {
  lastPinterestFolder = "";
  $("#pinterestBoardURL").value = "";
  $("#pinterestBoardURL").removeAttribute("aria-invalid");
  $("#pinterestImportResult").hidden = true;
  $("#pinterestBoardURL").focus();
}

function openImportedPinterestFolder() {
  if (!lastPinterestFolder) return;
  closeMenu();
  openFolder({kind: "tag", value: lastPinterestFolder, name: lastPinterestFolder});
}

function searchFromCommandPalette(query) {
  query = cleanSearchTerm(query);
  $("#commandPaletteDialog").close();
  if (!query) {
    $("#searchQuery").focus();
    return;
  }
  currentTag = "";
  currentSource = "";
  currentFolderName = "";
  setSearchInput(query, true);
  currentQuery = query;
  rememberSearch(query);
  renderSearchScope();
  loadImages();
}

function commandPaletteChoices(query) {
  const commands = [
    {icon: "⌕", title: t("command.focus_search"), detail: t("command.focus_search_help"), run: () => searchFromCommandPalette("")},
    {icon: "▦", title: t("command.open_images"), detail: t("command.open_images_help"), run: () => { $("#commandPaletteDialog").close(); showLocalImages(); }},
    {icon: "□", title: t("command.open_folders"), detail: t("command.open_folders_help"), run: () => { $("#commandPaletteDialog").close(); switchTab("folders"); loadFolders(); }},
    {icon: "⚙", title: t("command.open_settings"), detail: t("command.open_settings_help"), run: () => { $("#commandPaletteDialog").close(); showSettings(); }},
    {icon: "✎", title: t("command.open_storyboard"), detail: t("command.open_storyboard_help"), run: () => { location.href = "/practice"; }},
  ];
  if (!query) return commands;
  const normalized = query.toLowerCase();
  return [
    {icon: "⌕", title: t("command.search_for", {query}), detail: t("command.default_action"), run: () => searchFromCommandPalette(query)},
    ...commands.filter(command => `${command.title} ${command.detail}`.toLowerCase().includes(normalized)),
  ];
}

function renderCommandPalette() {
  const query = cleanSearchTerm($("#commandPaletteQuery").value);
  commandPaletteItems = commandPaletteChoices(query);
  commandPaletteIndex = Math.max(0, Math.min(commandPaletteIndex, commandPaletteItems.length - 1));
  $("#commandPaletteList").replaceChildren(...commandPaletteItems.map((item, index) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "command-palette-item";
    button.setAttribute("role", "option");
    button.setAttribute("aria-selected", String(index === commandPaletteIndex));
    const icon = document.createElement("span");
    icon.textContent = item.icon;
    const copy = document.createElement("span");
    const title = document.createElement("strong");
    title.textContent = item.title;
    const detail = document.createElement("small");
    detail.textContent = item.detail;
    copy.append(title, detail);
    button.append(icon, copy);
    button.onmouseenter = () => {
      commandPaletteIndex = index;
      $("#commandPaletteList").querySelectorAll(".command-palette-item").forEach((candidate, candidateIndex) => {
        candidate.setAttribute("aria-selected", String(candidateIndex === commandPaletteIndex));
      });
    };
    button.onclick = item.run;
    return button;
  }));
}

function openCommandPalette() {
  if (!appState?.plugins?.commandPalette?.enabled) return;
  const dialog = $("#commandPaletteDialog");
  $("#commandPaletteQuery").value = "";
  commandPaletteIndex = 0;
  renderCommandPalette();
  if (!dialog.open) dialog.showModal();
  $("#commandPaletteQuery").focus();
}

async function saveLanguageSetting() {
  try {
    await request("/api/app/settings/language", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({language: $("#languageSetting").value}),
    });
    appState.language = $("#languageSetting").value;
    await window.PictogrepI18n.init(appState.language);
    renderState();
  } catch (error) {
    showMessage(error.message, true);
  }
}

async function saveBrowserSettings() {
  const browser = {
    thumbnailSize: $("#thumbnailSizeSetting").value,
    showFilenames: $("#showFilenamesSetting").checked,
    homeOrder: $("#homeOrderSetting").value,
  };
  try {
    const data = await request("/api/app/settings/browser", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(browser),
    });
    appState.browser = data.browser;
    sidebarMode = appState.browser.homeOrder || "random";
    renderState();
    loadImages();
  } catch (error) {
    showMessage(error.message, true);
    renderState();
  }
}

async function saveAutomaticIndexing() {
  const automatic = $("#automaticIndexingSetting").checked;
  try {
    const data = await request("/api/app/settings/indexing", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({automatic}),
    });
    appState.searchIndex.automatic = data.indexing.automatic;
    if (automatic) {
      semanticIndexPaused = false;
      scheduleSemanticIndex(100);
    } else {
      semanticIndexQueued = false;
      updateSemanticIndexStatus("paused", appState.searchIndex.indexed || 0, appState.searchIndex.total || 0);
    }
    renderState();
  } catch (error) {
    $("#automaticIndexingSetting").checked = !automatic;
    showMessage(error.message, true);
  }
}

async function checkIndexNow() {
  failedSemanticPaths.clear();
  updateSemanticIndexStatus("queued", appState.searchIndex?.indexed || 0, appState.searchIndex?.total || 0);
  try {
    await refreshLibraryIndex("", {announce: true, forceSemantic: true});
  } catch (error) {
    updateSemanticIndexStatus("error", appState.searchIndex?.indexed || 0, appState.searchIndex?.total || 0, error.message);
  }
}

async function rebuildSearchIndex(event) {
  event.preventDefault();
  $("#reindexDialog").close();
  fullReindexRunning = true;
  semanticIndexPaused = true;
  semanticIndexQueued = false;
  clearTimeout(semanticIndexTimer);
  renderSearchIndexSettings();
  try {
    if (semanticIndexPromise) await semanticIndexPromise;
    const data = await request("/api/app/ai/reindex", {
      method: "POST",
      headers: {"X-Pictogrep-Action": "rebuild-search-index"},
    });
    failedSemanticPaths.clear();
    semanticResults.clear();
    appState.searchIndex.indexed = 0;
    appState.searchIndex.pending = data.total;
    appState.searchIndex.total = data.total;
    updateSemanticIndexStatus(data.total ? "queued" : "empty", 0, data.total);
    semanticIndexPaused = false;
    scheduleSemanticIndex(100, true);
  } catch (error) {
    semanticIndexPaused = false;
    updateSemanticIndexStatus("error", appState.searchIndex?.indexed || 0, appState.searchIndex?.total || 0, error.message);
  } finally {
    fullReindexRunning = false;
    renderSearchIndexSettings();
  }
}

function clearRecentSearchHistory() {
  localStorage.removeItem(recentSearchesKey);
  renderRecentSearches();
  const button = $("#clearRecentSearches");
  button.textContent = t("settings.cleared");
  setTimeout(() => { button.textContent = t("settings.clear"); }, 1400);
}

async function checkForUpdates() {
  const button = $("#checkForUpdates");
  const status = $("#updateStatus");
  button.disabled = true;
  button.textContent = t("update.checking");
  status.className = "";
  status.textContent = t("update.checking_releases");
  $("#installUpdate").hidden = true;
  $("#downloadUpdate").hidden = true;
  try {
    const update = await request("/api/app/update");
    if (!update.available) {
      status.className = "success";
      status.textContent = t("update.current");
      button.textContent = t("update.check_again");
      return;
    }
    status.textContent = `${t("update.available", {version: update.latestVersion})} ${update.hint || ""}`.trim();
    button.hidden = true;
    if (update.action === "replace") {
      const install = $("#installUpdate");
      install.hidden = false;
      install.className = "primary";
      install.textContent = t("update.to_version", {version: update.latestVersion});
    } else {
      const download = $("#downloadUpdate");
      download.hidden = false;
      download.href = update.url;
      if (update.action === "download") download.textContent = t("update.download_installer");
      else if (update.action === "managed") download.textContent = t("update.release_notes");
      else download.textContent = t("update.view_latest");
    }
  } catch (error) {
    status.className = "error";
    status.textContent = error.message;
    button.textContent = t("state.try_again");
  } finally {
    button.disabled = false;
  }
}

async function installAvailableUpdate() {
  const button = $("#installUpdate");
  const status = $("#updateStatus");
  button.disabled = true;
  button.textContent = t("update.updating");
  status.className = "";
  status.textContent = t("update.installing");
  try {
    const result = await request("/api/app/update", {
      method: "POST",
      headers: {"X-Pictogrep-Action": "install-update"},
    });
    if (!result.updated) {
      status.className = "success";
      status.textContent = t("update.already_current");
    } else {
      status.className = "success";
      status.textContent = t("update.installed", {version: result.version});
    }
    button.hidden = true;
  } catch (error) {
    status.className = "error";
    status.textContent = error.message;
    button.disabled = false;
    button.textContent = t("update.try_again");
  }
}

$("#menuButton").onclick = showMenuHome;
$("#wordmark").onmouseenter = event => { event.currentTarget.textContent = "navylily.tv"; };
$("#wordmark").onmouseleave = event => { event.currentTarget.textContent = "pictogrep"; };
$("#sidebarButton").onclick = openSidebar;
$("#closeSidebar").onclick = closeSidebar;
$("#sidebarScrim").onclick = closeSidebar;
$("#closeShortcuts").onclick = () => $("#shortcutsDialog").close();
$("#closeMenu").onclick = closeMenu;
$("#drawerScrim").onclick = closeMenu;
$("#imagesTab").onclick = showLocalImages;
$("#commonsTab").onclick = loadCommons;
$("#shuffleCommons").onclick = () => {
  currentQuery = "";
  clearSearchInput();
  loadCommons();
};
$("#calendarTab").onclick = loadCalendar;
$("#foldersTab").onclick = () => { switchTab("folders"); loadFolders(); };
$("#newFolderButton").onclick = () => openCreateFolder();
$("#showBoards").onclick = loadBoards;
$("#showPinterest").onclick = showPinterestImport;
$("#showPlugins").onclick = showPlugins;
$("#showAbout").onclick = showAbout;
$("#showSettings").onclick = showSettings;
$("#languageSetting").onchange = saveLanguageSetting;
$("#thumbnailSizeSetting").onchange = saveBrowserSettings;
$("#showFilenamesSetting").onchange = saveBrowserSettings;
$("#homeOrderSetting").onchange = saveBrowserSettings;
$("#automaticIndexingSetting").onchange = saveAutomaticIndexing;
$("#checkIndexNow").onclick = checkIndexNow;
$("#rebuildSearchIndex").onclick = () => $("#reindexDialog").showModal();
$("#reindexForm").onsubmit = rebuildSearchIndex;
$("#cancelReindex").onclick = () => $("#reindexDialog").close();
$("#clearRecentSearches").onclick = clearRecentSearchHistory;
$("#wikimediaPluginToggle").onchange = toggleWikimediaPlugin;
$("#calendarPluginToggle").onchange = toggleCalendarPlugin;
$("#sidebarPluginToggle").onchange = toggleSidebarPlugin;
$("#vimPluginToggle").onchange = toggleVimPlugin;
$("#commandPalettePluginToggle").onchange = toggleCommandPalettePlugin;
$("#pinterestPluginToggle").onchange = togglePinterestPlugin;
$("#checkForUpdates").onclick = checkForUpdates;
$("#installUpdate").onclick = installAvailableUpdate;
$("#showAdd").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#addSection").hidden = !$("#addSection").hidden;
};
$("#emptyAddImages").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#addSection").hidden = false;
  openMenu();
};
$("#imageFiles").onchange = event => uploadFiles(event.target.files);
$("#imageFolder").onchange = event => uploadFiles(event.target.files);
$("#pinterestImportForm").onsubmit = importPinterestBoard;
$("#pinterestCancelImport").onclick = () => pinterestImportController?.abort();
$("#pinterestImportAnother").onclick = resetPinterestImport;
$("#pinterestOpenFolder").onclick = openImportedPinterestFolder;
$("#closeImportProgress").onclick = () => {
  clearTimeout(importProgressTimer);
  $("#importProgress").hidden = true;
};
$("#closeMessage").onclick = closeMessage;
$("#commandPaletteQuery").oninput = () => {
  commandPaletteIndex = 0;
  renderCommandPalette();
};
$("#commandPaletteQuery").onkeydown = event => {
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    const direction = event.key === "ArrowDown" ? 1 : -1;
    commandPaletteIndex = (commandPaletteIndex + direction + commandPaletteItems.length) % commandPaletteItems.length;
    renderCommandPalette();
    return;
  }
  if (event.key === "Enter") {
    event.preventDefault();
    commandPaletteItems[commandPaletteIndex]?.run();
  }
};
$("#commandPaletteDialog").onclick = event => {
  if (event.target === event.currentTarget) event.currentTarget.close();
};

document.addEventListener("dragenter", event => {
  if (!isExternalImageDrop(event.dataTransfer)) return;
  event.preventDefault();
  externalDragDepth++;
  showDropOverlay(activeImportDestination(event.target));
});
document.addEventListener("dragover", event => {
  if (!isExternalImageDrop(event.dataTransfer)) return;
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
  showDropOverlay(activeImportDestination(event.target));
});
document.addEventListener("dragleave", event => {
  if (!isExternalImageDrop(event.dataTransfer)) return;
  externalDragDepth = Math.max(0, externalDragDepth - 1);
  if (externalDragDepth === 0 || event.relatedTarget === null) hideDropOverlay();
});
document.addEventListener("drop", event => {
  if (!isExternalImageDrop(event.dataTransfer)) return;
  event.preventDefault();
  const destination = activeImportDestination(event.target);
  const handled = queueTransferImport(event.dataTransfer, destination);
  hideDropOverlay();
  if (!handled) showMessage(t("import.unsupported_drop"), true);
});
document.addEventListener("dragend", hideDropOverlay);
window.addEventListener("blur", () => {
  if (!$("#dropOverlay").hidden) hideDropOverlay();
});
document.addEventListener("paste", event => {
  const files = transferImageFiles(event.clipboardData);
  const typing = ["INPUT", "TEXTAREA", "SELECT"].includes(event.target?.tagName) || event.target?.isContentEditable;
  const urls = files.length || typing ? [] : extractDroppedURLs(event.clipboardData);
  if (!files.length && !urls.length) return;
  event.preventDefault();
  const destination = activeImportDestination(event.target);
  if (files.length) queueImport(() => uploadFiles(files, {destination, openMenu: false, showLibrary: !$("#imagesPanel").hidden}));
  else queueImport(() => importDroppedURLs(urls, destination));
});
$("#searchForm").onsubmit = event => {
  event.preventDefault();
  performSearch(true);
};
$("#searchQuery").addEventListener("input", event => {
  resizeSearchDraft();
  clearTimeout(queryPrimeTimer);
  const query = searchQueryValue();
  if (!query) {
    if (currentQuery) performSearch();
    renderRecentSearches();
    return;
  }
  $("#recentSearches").hidden = true;
  if (query.length < 2) return;
  queryPrimeTimer = setTimeout(() => {
    if (searchQueryValue() !== query || currentQuery === query) return;
    currentQuery = query;
    if (!$("#commonsPanel").hidden) loadCommons();
    else if (!$("#calendarPanel").hidden) loadCalendar();
    else loadImages();
  }, 280);
});
$("#searchQuery").addEventListener("keydown", event => {
  if (event.isComposing) return;
  if (event.key === "Backspace" && !event.currentTarget.value) {
    if (event.repeat) return;
    if (hasSearchScope()) {
      event.preventDefault();
      clearSearchScope();
    }
  }
});
$("#searchQuery").addEventListener("focus", () => {
  if (!searchQueryValue()) renderRecentSearches();
});
$("#searchQuery").addEventListener("blur", event => {
  if (event.relatedTarget?.closest?.("#recentSearches")) return;
  $("#recentSearches").hidden = true;
});
$("#searchInputShell").addEventListener("pointerdown", event => {
  if (event.target === event.currentTarget) {
    event.preventDefault();
    $("#searchQuery").focus();
  }
});
$("#searchScope").onclick = clearSearchScope;
document.querySelectorAll("[data-smart]").forEach(button => {
  button.onclick = () => showSidebarSmart(button.dataset.smart);
});
$("#sidebarSearch").oninput = event => {
  const query = event.target.value.trim().toLowerCase();
  document.querySelectorAll(".sidebar-collection").forEach(row => {
    row.hidden = Boolean(query && !row.dataset.name.includes(query));
  });
};
$("#tagForm").onsubmit = saveTag;
$("#cancelTag").onclick = () => $("#tagDialog").close();
$("#deleteImageForm").onsubmit = deleteConfirmedImage;
$("#cancelDeleteImage").onclick = () => $("#deleteImageDialog").close();
$("#deleteImageDialog").addEventListener("close", () => { pendingDeleteItem = null; });
$("#createFolderForm").onsubmit = createFolder;
$("#cancelFolder").onclick = () => $("#folderDialog").close();
$("#closeViewer").onclick = closeImageViewer;
$("#viewerPrevious").onclick = () => moveViewer(-1);
$("#viewerNext").onclick = () => moveViewer(1);
$("#imageViewer").onclick = event => {
  if (event.target === $("#imageViewer")) closeImageViewer();
};
$("#imageViewer").addEventListener("cancel", event => { event.preventDefault(); closeImageViewer(); });
$("#imageViewer").addEventListener("close", () => {
  currentViewerItem = null;
  relatedLoadId++;
});
$("#closeCanvas").onclick = () => $("#canvasDialog").close();
$("#canvasViewport").onpointerdown = event => {
  if (event.button !== 0) return;
  if (event.target !== $("#canvasViewport")) return;
  event.currentTarget.setPointerCapture(event.pointerId);
  canvasPointer = {kind: "pan", startX: event.clientX, startY: event.clientY, x: canvasPan.x, y: canvasPan.y};
};
$("#canvasViewport").onpointermove = moveCanvasPointer;
$("#canvasViewport").onpointerup = endCanvasPointer;
$("#canvasViewport").onpointercancel = endCanvasPointer;
$("#canvasViewport").addEventListener("wheel", event => {
  event.preventDefault();
  const viewport = $("#canvasViewport");
  const bounds = viewport.getBoundingClientRect();
  const mouse = {x: event.clientX - bounds.left, y: event.clientY - bounds.top};
  const world = {x: (mouse.x - canvasPan.x) / canvasZoom, y: (mouse.y - canvasPan.y) / canvasZoom};
  const next = Math.max(0.25, Math.min(3, canvasZoom * (event.deltaY < 0 ? 1.1 : 0.9)));
  canvasPan = {x: mouse.x - world.x * next, y: mouse.y - world.y * next};
  canvasZoom = next;
  applyCanvasTransform();
}, {passive: false});
document.addEventListener("keydown", event => {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k" && appState?.plugins?.commandPalette?.enabled) {
    event.preventDefault();
    if ($("#commandPaletteDialog").open) $("#commandPaletteDialog").close();
    else openCommandPalette();
    return;
  }
  const typing = ["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement?.tagName);
  if ($("#imageViewer").open && event.key === "ArrowLeft") { event.preventDefault(); moveViewer(-1); return; }
  if ($("#imageViewer").open && event.key === "ArrowRight") { event.preventDefault(); moveViewer(1); return; }
  if (event.key === "Escape") {
    if ($("#imageViewer").open) { event.preventDefault(); closeImageViewer(); return; }
    closeMenu(); closeSidebar(); closeCardMenus();
    if ($("#shortcutsDialog").open) $("#shortcutsDialog").close();
    return;
  }
  if (event.key === "?" && !typing) { event.preventDefault(); showShortcuts(); return; }
  if (event.key === "/" && !typing) {
    event.preventDefault();
    $("#searchQuery").focus();
    return;
  }
  if (typing || $("#imageViewer").open || $("#shortcutsDialog").open) return;
  const vim = Boolean(appState?.plugins?.vim?.enabled);
  if (vim) {
    if (event.key === "g") {
      if (vimPendingG) { window.scrollTo({top: 0, behavior: "smooth"}); vimPendingG = false; }
      else vimPendingG = true;
      setTimeout(() => { vimPendingG = false; }, 700);
      return;
    }
    if (event.key === "G") { event.preventDefault(); window.scrollTo({top: document.body.scrollHeight, behavior: "smooth"}); return; }
    if (event.key === "j") { event.preventDefault(); focusImageCard(1); return; }
    if (event.key === "k") { event.preventDefault(); focusImageCard(-1); return; }
    if (event.key === "o") { event.preventDefault(); openFocusedImage(); return; }
    if (event.key === "q") { event.preventDefault(); closeMenu(); closeSidebar(); closeCardMenus(); return; }
  } else {
    if (event.key === "ArrowDown") { event.preventDefault(); focusImageCard(1); return; }
    if (event.key === "ArrowUp") { event.preventDefault(); focusImageCard(-1); return; }
    if (event.key === "Enter") { openFocusedImage(); return; }
  }
});
document.addEventListener("click", closeCardMenus);

function refreshLibraryWhenDue() {
  if (!appState || document.visibilityState === "hidden" || Date.now() - lastLibraryRefreshAt < 5 * 60 * 1000) return;
  refreshLibraryIndex().catch(error => {
    logBackground("library-refresh", error.message);
  });
}

async function start() {
  appState = await request("/api/app/state");
  await window.PictogrepI18n.init(appState.language);
  resizeSearchDraft();
  sidebarMode = appState.browser?.homeOrder || "random";
  const search = appState.searchIndex || {indexed: 0, total: 0, pending: 0, automatic: true};
  semanticIndexStatus = {
    state: !search.total ? "empty" : search.pending ? (search.automatic === false ? "paused" : "queued") : "ready",
    indexed: search.indexed || 0, total: search.total || 0, error: "",
  };
  setLoading();
  lastJobState = appState.indexJob.state;
  renderState();
  await loadImages();
  await loadFolders();
  await syncViewerFromHistory();
  if (appState?.index?.count) scheduleSemanticIndex(700);
  setTimeout(refreshLibraryWhenDue, 1400);
  setInterval(refreshLibraryWhenDue, 5 * 60 * 1000);
}

window.addEventListener("popstate", syncViewerFromHistory);
window.addEventListener("focus", refreshLibraryWhenDue);
document.addEventListener("visibilitychange", refreshLibraryWhenDue);
start();
