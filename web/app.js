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
let draggedFolder = null;
let folderPendingDelete = null;
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
let imagePaging = null;
let imageScrollObserver = null;
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
let pinterestRunning = false;
let pinterestWatching = false;
// Pinterest and web imports are the same downloader. Either one running means
// neither panel may start another.
function downloaderBusy() { return pinterestRunning || webImportRunning; }
let lastPinterestFolder = "";
let queryPrimeTimer = null;
let currentViewerItem = null;
let browserImages = [];
let relatedLoadId = 0;
let relatedPaging = null;
let relatedScrollObserver = null;
const RELATED_PAGE_SIZE = 18;
// The images endpoint refuses to return more than this in one page, so a
// restored view cannot ask for more than it either.
const MAX_IMAGE_PAGE = 500;
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

/**
 * Set for the one render that follows a picture arriving over sync, so those
 * images resolve into place instead of appearing fully formed. Cleared by the
 * first render that reads it: only the arrival earns this, not every later
 * scroll through the same grid.
 */
let revealNextImages = false;

function loadImage(image, url, {fallback = "", label = "", onload = null} = {}) {
  let activeURL = url;
  let finished = false;
  let slow = revealNextImages;
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

/** The last whole percent shown while a model downloads. See getAIWorker. */
let lastModelPercent = -1;

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
        // Once per whole percent. The worker reports on every chunk it reads
        // off the network, which over a 156 MB model is thousands of calls,
        // and each one used to rewrite the toast and post a line to the server.
        const percent = Math.round(detail.progress);
        if (percent !== lastModelPercent) {
          lastModelPercent = percent;
          showMessage(t("ai.preparing_percent", {progress: percent}), false, true);
        }
      } else if (detail.status === "initiate") {
        lastModelPercent = -1;
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
  try {
    await vectorPromise;
  } catch (error) {
    // The query could not be turned into a vector: offline, or the model
    // download died with the app. Letting this throw cleared the grid and said
    // "Could not load pictures", which is the worst of both answers, because
    // the library is right there and its filenames are searchable without any
    // model at all. This is the same fallback a brand-new library gets.
    logBackground("search-query-vector", error.message);
    const fallback = await request(`/api/app/search?q=${encodeURIComponent(query)}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}&limit=120`);
    fallback.preparing = true;
    return fallback;
  }
  return requestSemanticResults(query);
}

function showMessage(text, error = false, persist = false, seconds = 3.5) {
  // Errors only. Every ordinary status line used to be posted back to the
  // server as well, which during a model download meant one HTTP round trip
  // and one log line per network chunk, on a phone, while that phone was busy
  // downloading 156 MB.
  if (error) logBackground("ui-status", text, "", "error");
  const box = $("#message");
  clearTimeout(messageTimer);
  $("#messageText").textContent = text;
  box.classList.toggle("error", error);
  box.hidden = false;
  if (!persist) messageTimer = setTimeout(() => { box.hidden = true; }, seconds * 1000);
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
  $("#syncSection").hidden = true;
  $("#webSection").hidden = true;
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
  card.dataset.id = item.id;
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
  const wasOpen = viewer.open;
  showViewerImage(item, false);
  if (!wasOpen) viewer.showModal();
  if (updateHistory && new URL(location.href).searchParams.get("image") !== item.id) {
    // The whole viewer takes a single history entry: hopping from one image to
    // a similar one replaces it, so Escape and × leave the viewer for good
    // instead of walking back through everything that was clicked.
    if (wasOpen) history.replaceState({pictogrepViewer: history.state?.pictogrepViewer === true}, "", viewerURL(item.id));
    else history.pushState({pictogrepViewer: true}, "", viewerURL(item.id));
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
  grid.replaceChildren(...data.images.map(relatedCard));
  const status = $("#relatedStatus");
  if (!data.ready) status.textContent = t("viewer.preparing_similar");
  else if (!data.images.length && data.indexed < data.total) status.textContent = t("viewer.similar_later");
  else if (!data.images.length) status.textContent = t("viewer.no_similar");
  else if (data.indexed < data.total) status.textContent = t("viewer.similar_progress", {indexed: data.indexed, total: data.total});
  else status.textContent = "";
  status.hidden = !status.textContent;
  relatedPaging = data.ready && data.images.length
    ? {id: currentViewerItem?.id, limit: RELATED_PAGE_SIZE, offset: data.images.length, loading: false, done: data.images.length < RELATED_PAGE_SIZE}
    : null;
  $("#relatedMore").hidden = true;
  fillRelatedViewport();
}

// The viewer keeps handing out the next slice of the similarity ranking while
// you scroll past the ones it already showed.
async function loadMoreRelated() {
  const paging = relatedPaging;
  if (!paging || paging.loading || paging.done) return;
  if (!$("#imageViewer").open || currentViewerItem?.id !== paging.id) return;
  const loadId = relatedLoadId;
  paging.loading = true;
  paging.failed = false;
  $("#relatedMore").hidden = false;
  try {
    const data = await request(`/api/app/related/${encodeURIComponent(paging.id)}?limit=${paging.limit}&offset=${paging.offset}`);
    if (loadId !== relatedLoadId || paging !== relatedPaging || currentViewerItem?.id !== paging.id) return;
    const images = data.images || [];
    paging.offset += images.length;
    $("#relatedGrid").append(...images.map(relatedCard));
    if (images.length < paging.limit) paging.done = true;
  } catch (_) {
    // Same as the library grid: a slice that failed is not the end of the
    // ranking, so the next scroll gets to ask for it again.
    paging.failed = true;
  } finally {
    paging.loading = false;
    $("#relatedMore").hidden = true;
  }
  if (paging === relatedPaging && !paging.done && !paging.failed) fillRelatedViewport();
}

function fillRelatedViewport() {
  const sentinel = $("#relatedSentinel");
  const viewer = $("#imageViewer");
  if (!sentinel || !relatedPaging || relatedPaging.done || relatedPaging.loading || relatedPaging.failed) return;
  if (sentinel.getBoundingClientRect().top < viewer.getBoundingClientRect().bottom + 400) loadMoreRelated();
}

function watchRelatedScroll() {
  const sentinel = $("#relatedSentinel");
  if (!sentinel || relatedScrollObserver) return;
  relatedScrollObserver = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) loadMoreRelated();
  }, {root: $("#imageViewer"), rootMargin: "400px"});
  relatedScrollObserver.observe(sentinel);
}

function relatedCard(item) {
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
}

async function refreshRelatedResults() {
  const item = currentViewerItem;
  if (!item || !$("#imageViewer").open) return;
  const loadId = relatedLoadId;
  try {
    const data = await request(`/api/app/related/${encodeURIComponent(item.id)}?limit=${RELATED_PAGE_SIZE}`);
    if (loadId === relatedLoadId && currentViewerItem?.id === item.id) renderRelatedImages(data);
  } catch (_) {
    // Background refreshes stay quiet; the initial load reports useful errors.
  }
}

async function loadRelatedImages(item) {
  const loadId = ++relatedLoadId;
  relatedPaging = null;
  $("#relatedMore").hidden = true;
  watchRelatedScroll();
  $("#relatedGrid").replaceChildren();
  $("#relatedStatus").hidden = false;
  $("#relatedStatus").textContent = t("viewer.finding_similar");
  try {
    let data = await request(`/api/app/related/${encodeURIComponent(item.id)}?limit=${RELATED_PAGE_SIZE}`);
    let state = null;
    if (!data.ready) {
      state = await request("/api/app/ai");
      const missing = state.missing.find(candidate => candidate.path === item.path);
      if (missing) {
        await saveSemanticEmbedding(missing);
        data = await request(`/api/app/related/${encodeURIComponent(item.id)}?limit=${RELATED_PAGE_SIZE}`);
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
  // The welcome tutorial answers "how do I start a whole library", which is
  // the wrong answer inside a folder: an empty folder in an otherwise-empty
  // library used to show it anyway, because emptiness was read off the whole
  // library's count rather than off what is actually on screen. Scoped to a
  // folder or a search, whether the library overall has anything in it is not
  // the question being asked.
  const scoped = Boolean(currentTag || currentSource || currentQuery);
  const libraryEmpty = !scoped && (!appState?.index || appState.index.count === 0);
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
  const query = searchQueryValue();
  // Coming back from another tab with the same scope keeps the pictures that
  // are already on screen instead of shuffling a fresh set.
  if (!currentTag && !currentSource && query === currentQuery && browserImages.length) {
    switchTab("images");
    fillImageViewport();
    return;
  }
  currentTag = "";
  currentSource = "";
  currentFolderName = "";
  currentQuery = query;
  renderSearchScope();
  loadImages();
}

function imagePageURL({mode, pageSize, offset, seed}) {
  return `/api/app/images?mode=${mode}&count=${pageSize}&offset=${offset}&seed=${seed}`
    + `&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}`;
}

function setMoreImagesVisible(visible) {
  const more = $("#imageGridMore");
  if (!more) return;
  if (visible) more.replaceChildren(document.createTextNode(t("state.loading_more")));
  more.hidden = !visible;
}

// A page that failed to arrive is not the end of the library, so the footer
// says what went wrong and offers another go instead of leaving the grid to
// stop growing for no visible reason.
function showImageGridRetry(message) {
  const more = $("#imageGridMore");
  if (!more) return;
  const retry = document.createElement("button");
  retry.type = "button";
  retry.className = "grid-retry";
  retry.textContent = t("state.try_again");
  retry.onclick = () => {
    if (!imagePaging) return;
    imagePaging.failed = false;
    setMoreImagesVisible(true);
    loadMoreImages();
  };
  more.replaceChildren(document.createTextNode(`${t("state.load_more_failed", {error: message})} `), retry);
  more.hidden = false;
}

// Taking the card out in place keeps every other picture exactly where it was.
// Reloading cannot do that: a seeded shuffle over a list that is one shorter
// deals a different order, so the whole library would rearrange itself around
// the gap and the reader would lose their place over a single deletion.
function forgetImage(id) {
  const before = browserImages.length;
  browserImages = browserImages.filter(item => item.id !== id);
  $("#imageGrid").querySelectorAll(".image-card").forEach(card => {
    if (card.dataset.id === id) card.remove();
  });
  // A picture deleted from somewhere other than the grid, a viewer opened
  // straight from a link for instance, never took up a slot in the loaded
  // pages, so the paging window stays where it is.
  if (browserImages.length === before) return;
  if (imagePaging) {
    imagePaging.total = Math.max(0, imagePaging.total - 1);
    imagePaging.offset = Math.max(0, imagePaging.offset - 1);
    imagePaging.done = imagePaging.offset >= imagePaging.total;
    $("#imageCount").textContent = imagePaging.total ? `(${imagePaging.total})` : "";
  } else {
    $("#imageCount").textContent = browserImages.length ? `(${browserImages.length})` : "";
  }
  fillImageViewport();
}

function appendImages(images) {
  browserImages = browserImages.concat(images);
  $("#imageGrid").append(...images.map(pictureCard));
  $("#imageCount").textContent = imagePaging?.total ? `(${imagePaging.total})` : "";
}

// The grid grows a page at a time as the sentinel below it comes into view, so
// a big library never has to be rendered all at once.
async function loadMoreImages() {
  const paging = imagePaging;
  if (!paging || paging.loading || paging.done) return;
  if ($("#imagesPanel").hidden) return;
  const loadId = imageLoadId;
  paging.loading = true;
  paging.failed = false;
  setMoreImagesVisible(true);
  try {
    const data = await request(imagePageURL(paging));
    if (loadId !== imageLoadId || paging !== imagePaging) return;
    paging.total = data.total ?? paging.total;
    paging.offset += paging.pageSize;
    appendImages(data.images || []);
    if (paging.offset >= paging.total || !(data.images || []).length) paging.done = true;
  } catch (error) {
    // One page that would not load used to end the scroll for good, which put
    // the rest of the library out of reach. The position is kept instead, so
    // the retry button or the next scroll can pick it up again.
    paging.failed = true;
    showImageGridRetry(error.message);
  } finally {
    paging.loading = false;
    if (!paging.failed) setMoreImagesVisible(false);
  }
  if (paging === imagePaging && !paging.done && !paging.failed) fillImageViewport();
}

// An IntersectionObserver only fires on a crossing, so after each page we check
// whether the sentinel is still on screen and keep going until it is pushed off.
// A failed page is skipped here so a sentinel that stays on screen cannot spin
// on the same error. The observer still retries once the reader scrolls again.
function fillImageViewport() {
  const sentinel = $("#imageGridSentinel");
  if (!sentinel || !imagePaging || imagePaging.done || imagePaging.loading || imagePaging.failed) return;
  if (sentinel.getBoundingClientRect().top < window.innerHeight + 600) loadMoreImages();
}

function watchImageScroll() {
  const sentinel = $("#imageGridSentinel");
  if (!sentinel || imageScrollObserver) return;
  imageScrollObserver = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) loadMoreImages();
  }, {rootMargin: "600px"});
  imageScrollObserver.observe(sentinel);
}

// `keepOrder` is for refreshing after an edit rather than navigating. It holds
// on to the shuffle already on screen and asks for the pages already scrolled
// through in one go, so the library does not reorder itself and throw the
// reader back to the top over an unrelated change.
async function loadImages({keepOrder = false} = {}) {
  const loadId = ++imageLoadId;
  const previous = imagePaging;
  imagePaging = null;
  setMoreImagesVisible(false);
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
        // renderImages has already written "No results" across the screen, and
        // while the search model is still being fetched that is a lie: there is
        // no answer yet, rather than an empty one. On a phone the model is 156
        // MB, so "no results" can sit there for minutes over a library that is
        // about to answer perfectly well.
        if (!data.images.length) showResultState(t("ai.preparing_first"), t("ai.downloading"));
      } else if (!semanticIndexPromise && !semanticWarmupPromise) closeMessage();
    } else {
      const mode = currentTag || currentSource ? "recent" : sidebarMode;
      const pageSize = mode === "random" ? 50 : 120;
      const resumable = keepOrder && previous && previous.mode === mode
        && previous.tag === currentTag && previous.source === currentSource;
      const seed = resumable ? previous.seed : 0;
      const count = resumable ? Math.min(Math.max(previous.offset, pageSize), MAX_IMAGE_PAGE) : pageSize;
      const data = await request(imagePageURL({mode, pageSize: count, offset: 0, seed}));
      if (loadId !== imageLoadId) return;
      renderImages(data.images, data.total);
      imagePaging = {
        mode,
        pageSize,
        tag: currentTag,
        source: currentSource,
        seed: data.seed || 0,
        offset: count,
        total: data.total || data.images.length,
        loading: false,
      };
      imagePaging.done = imagePaging.offset >= imagePaging.total;
      fillImageViewport();
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
  if (folder.kind === "tag") makeFolderMergeable(card, folder);
  card.onclick = () => openFolder(folder);
  card.oncontextmenu = event => openFolderContextMenu(event, folder);
  return card;
}

// Collections can be dragged onto each other; the badge is the only prompt, so
// the merge happens without a dialog or a name to type.
function makeFolderMergeable(card, folder) {
  const badge = document.createElement("span");
  badge.className = "folder-merge-badge";
  badge.textContent = t("folders.merge_hint");
  card.append(badge);
  card.draggable = true;
  card.ondragstart = event => {
    draggedFolder = folder;
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("application/x-pictogrep-folder", folder.value);
  };
  card.ondragend = () => {
    draggedFolder = null;
    document.querySelectorAll(".is-merge-target").forEach(element => element.classList.remove("is-merge-target"));
  };
  card.ondragover = event => {
    if (!draggedFolder || draggedFolder.value === folder.value) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    card.classList.add("is-merge-target");
  };
  card.ondragleave = () => card.classList.remove("is-merge-target");
  card.ondrop = event => {
    event.preventDefault();
    card.classList.remove("is-merge-target");
    const from = draggedFolder;
    draggedFolder = null;
    if (from) mergeFolders(from, folder);
  };
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
  const canvas = $("#folderContextCanvas");
  canvas.hidden = !appState?.plugins?.canvas?.enabled;
  canvas.onclick = () => {
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
  const remove = $("#folderContextDelete");
  remove.hidden = folder.kind !== "tag";
  remove.onclick = () => {
    closeCardMenus();
    openDeleteFolder(folder);
  };
  menu.hidden = false;
  positionContextMenu(menu, event);
}

function openDeleteFolder(folder) {
  folderPendingDelete = folder;
  $("#deleteFolderText").textContent = t("folders.delete_confirm", {name: folder.name});
  $("#deleteFolderDialog").showModal();
}

// A folder that was deleted or absorbed cannot go on scoping the library. The
// scope line and the pictures already on screen both have to let go of it,
// otherwise the Images tab keeps showing a folder that is gone and scrolling
// asks the server for more of it.
function releaseFolderScope() {
  renderSearchScope();
  if (!$("#imagesPanel").hidden) {
    loadImages();
    return;
  }
  // Nothing is on screen to reload, so drop the scoped pictures and let the
  // next visit to the library fetch a fresh page. Bumping the load id keeps a
  // request that is still in flight from filling the grid back up.
  imageLoadId++;
  imagePaging = null;
  browserImages = [];
  $("#imageGrid").replaceChildren();
  setMoreImagesVisible(false);
}

async function deleteFolder(folder) {
  try {
    await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "delete", tag: folder.value}),
    });
    if (currentTag === folder.value) {
      currentTag = "";
      currentFolderName = "";
      releaseFolderScope();
    }
    await refreshState();
    await loadFolders();
    showMessage(t("folders.deleted", {name: folder.name}));
  } catch (error) { showMessage(error.message, true); }
}

// Dropping one folder on another merges them, keeping the dragged folder's
// name so nothing has to be typed.
async function mergeFolders(from, into) {
  if (from.value === into.value) return;
  try {
    await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "merge", tag: from.value, into: into.value}),
    });
    // The dragged folder survives and swallows the one it was dropped on, so a
    // view scoped to either side is now stale: one name no longer exists, and
    // the other just gained a second folder's pictures.
    if (currentTag === into.value || currentTag === from.value) {
      currentTag = from.value;
      currentFolderName = from.name;
      releaseFolderScope();
    }
    await refreshState();
    await loadFolders();
    showMessage(t("folders.merged", {from: from.name, into: into.name}));
  } catch (error) { showMessage(error.message, true); }
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
  const sourceCount = (appState.sources || []).length;
  renderSourceFolders();
  $("#sourceFolderSummary").textContent = sourceCount === 1
    ? t("settings.folders_one")
    : sourceCount ? t("settings.folders_count", {count: sourceCount}) : t("settings.folders_help");
  document.body.dataset.thumbnailSize = appState.browser?.thumbnailSize || "medium";
  document.body.classList.toggle("show-filenames", Boolean(appState.browser?.showFilenames));
  // Inside the Android app there is no folder to point at and no gallery-dl to
  // run, so the parts of the interface that offer them are not disabled, they
  // are absent. The server is what knows; the class is how the stylesheet and
  // the onboarding find out.
  document.body.classList.toggle("is-phone", Boolean(appState.mobile));
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
  $("#canvasPluginToggle").checked = Boolean(appState.plugins?.canvas?.enabled);
  $("#commandPalettePluginToggle").checked = Boolean(appState.plugins?.commandPalette?.enabled);
  $("#showSync").hidden = appState.mobile || appState.sync?.available === false;
  $("#showSyncPhone").hidden = !appState.mobile || appState.sync?.available === false;
  const pinterestEnabled = Boolean(appState.plugins?.pinterest?.enabled);
  $("#pinterestPluginToggle").checked = pinterestEnabled;
  $("#pinterestAutoSyncToggle").checked = appState.pinterest?.autoSync !== false;
  $("#pinterestAutoSyncToggle").disabled = !pinterestEnabled;
  $("#showPinterest").hidden = !pinterestEnabled;
  if (!pinterestEnabled) $("#pinterestSection").hidden = true;
  const pinterestAvailable = appState.plugins?.pinterest?.available !== false;
  $("#pinterestReadiness").hidden = pinterestAvailable;
  // One downloader serves both importers, so a run started in either panel has
  // to keep both Start buttons down. Reading only this panel's flag let a
  // second click through, and the 409 it earned wiped the progress it was
  // reporting on.
  $("#pinterestImportButton").disabled = !pinterestAvailable || downloaderBusy();
  const webEnabled = Boolean(appState.plugins?.web?.enabled);
  const webAvailable = appState.plugins?.web?.available !== false;
  $("#webPluginToggle").checked = webEnabled;
  $("#webAutoSyncToggle").checked = appState.web?.autoSync !== false;
  $("#webAutoSyncToggle").disabled = !webEnabled;
  $("#webImportRow").hidden = !webEnabled;
  if (!webEnabled) $("#webSection").hidden = true;
  $("#webReadiness").hidden = webAvailable;
  $("#webImportButton").disabled = !webAvailable || downloaderBusy();

  renderAutoUpdate();

  renderPinterestFolders();

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
  // Only if it was already open. On a desktop this flow starts inside the
  // drawer, so it stays put. On a phone it starts at the floating button, and
  // opening the menu there threw a full-screen panel over the library at the
  // exact moment the new pictures landed in it: they were imported, they were
  // rendered, and the user was looking at a menu. The import progress panel is
  // its own element and shows either way.
  if (options.openMenu !== false && $("#drawer").classList.contains("open")) openMenu();
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

/**
 * The one intake method a drop or a share sheet cannot cover: a link with no
 * picture attached to drag and no app to share it out of, just a URL someone
 * has and wants in the library. Reuses the same single-picture fetch every
 * other path already goes through (import-url, native_gallery.go where
 * gallery-dl is not installed), so this adds no new server behaviour, only a
 * place to type into.
 */
async function pasteImageURL(event) {
  event.preventDefault();
  const input = $("#pasteURLInput");
  const url = input.value.trim();
  if (!url) return;
  const form = $("#pasteURLForm");
  form.querySelectorAll("input, button").forEach(element => { element.disabled = true; });
  try {
    await importDroppedURLs([url], activeImportDestination());
    input.value = "";
  } finally {
    form.querySelectorAll("input, button").forEach(element => { element.disabled = false; });
  }
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
    forgetImage(item.id);
    await refreshState();
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
    await loadImages({keepOrder: true});
    await loadFolders();
  } catch (error) {
    showMessage(error.message, true);
  }
}

async function loadBoards() {
  $("#addSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#syncSection").hidden = true;
  $("#webSection").hidden = true;
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
	$("#syncSection").hidden = true;
	$("#webSection").hidden = true;
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
  $("#syncSection").hidden = true;
  $("#webSection").hidden = true;
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
  $("#syncSection").hidden = true;
  $("#webSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = false;
  renderState();
  renderFollowedBoards();
  renderFollowedWebSources();
  openMenu(t("menu.plugins"));
}

// Adding a folder is one click now, so removing one has to be too. A folder
// picked by mistake, or a huge one picked without meaning to, used to be
// permanent: indexing only ever unions what it knows with what it is given.
function renderSourceFolders() {
  const list = $("#sourceFolderList");
  if (!list) return;
  const sources = appState?.sources || [];
  if (!sources.length) {
    list.hidden = true;
    return;
  }
  list.replaceChildren(...sources.map(folder => {
    const row = document.createElement("div");
    row.className = "followed-board";
    const text = document.createElement("span");
    const name = document.createElement("strong");
    name.textContent = folder.split(/[\\/]/).filter(Boolean).pop() || folder;
    name.title = folder;
    const path = document.createElement("small");
    path.textContent = folder;
    text.append(name, path);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = t("settings.stop_reading");
    remove.onclick = async () => {
      remove.disabled = true;
      try {
        await request("/api/app/folders/forget", {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({folder}),
        });
        showMessage(t("settings.stopped_reading", {name: name.textContent}));
        await refreshState();
        await loadImages();
        await loadFolders();
      } catch (error) {
        remove.disabled = false;
        showMessage(error.message, true);
      }
    };
    row.append(text, remove);
    return row;
  }));
  list.hidden = false;
}

// A board name is the last piece of its address, which is what people called it
// on Pinterest.
function pinterestBoardLabel(rawURL) {
  try {
    const parts = new URL(rawURL).pathname.split("/").filter(Boolean);
    return parts.length ? decodeURIComponent(parts[parts.length - 1]).replace(/-/g, " ") : rawURL;
  } catch (_) { return rawURL; }
}

function lastCheckedLabel(seconds) {
  if (!seconds) return t("pinterest.never_checked");
  const days = Math.floor((Date.now() / 1000 - seconds) / 86400);
  if (days <= 0) return t("pinterest.checked_today");
  return days === 1 ? t("pinterest.checked_yesterday") : t("pinterest.checked_days", {count: days});
}

// Turning the whole feature off was the only way to stop following a board, so
// one board you no longer want meant giving up the ones you do.
async function renderFollowedBoards() {
  const list = $("#pinterestBoardList");
  if (!list) return;
  if (!appState?.plugins?.pinterest?.enabled) {
    list.hidden = true;
    return;
  }
  let boards = [];
  try {
    boards = (await request("/api/app/plugins/pinterest/boards")).boards || [];
  } catch (_) {
    list.hidden = true;
    return;
  }
  if (!boards.length) {
    list.hidden = true;
    return;
  }
  list.replaceChildren(...boards.map(board => {
    const row = document.createElement("div");
    row.className = "followed-board";
    const text = document.createElement("span");
    const name = document.createElement("strong");
    name.textContent = pinterestBoardLabel(board.url);
    name.title = board.url;
    const when = document.createElement("small");
    when.textContent = lastCheckedLabel(board.lastSyncAt);
    text.append(name, when);
    const stop = document.createElement("button");
    stop.type = "button";
    stop.textContent = t("pinterest.unfollow");
    stop.onclick = async () => {
      stop.disabled = true;
      try {
        await request("/api/app/settings/pinterest", {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({forget: board.url}),
        });
        showMessage(t("pinterest.unfollowed", {board: pinterestBoardLabel(board.url)}));
        await renderFollowedBoards();
      } catch (error) {
        stop.disabled = false;
        showMessage(error.message, true);
      }
    };
    row.append(text, stop);
    return row;
  }));
  list.hidden = false;
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

async function toggleCanvasPlugin() {
  const enabled = $("#canvasPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "canvas", enabled}),
    });
    appState.plugins.canvas = {enabled};
    renderState();
    showMessage(t(enabled ? "plugins.canvas_enabled" : "plugins.canvas_disabled"));
  } catch (error) {
    $("#canvasPluginToggle").checked = !enabled;
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
    renderFollowedBoards();
    renderFollowedWebSources();
    showMessage(t(enabled ? "pinterest.enabled" : "pinterest.disabled"));
  } catch (error) {
    $("#pinterestPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

// --- LAN phone sync -----------------------------------------------------
//
// The panel never blocks on the network: opening it starts a pairing request
// and shows a QR the moment the reply arrives, but the drawer itself opens
// immediately, and every button below answers right away and lets the actual
// network work happen after. A phone that is asleep or a laptop with the lid
// closed is not an error state anywhere in this file, because it just isn't
// one: it is the normal condition sync is designed to sit through.

let syncExpiryTimer = null;
let syncPollTimer = null;

// While the panel is open, and only then. Pairing and a first transfer both
// finish in seconds and the user is watching both happen, so the screen has to
// move on its own; the moment the panel closes there is nothing to look at and
// polling a LAN on a phone's battery stops being free.
function startSyncPolling() {
  clearInterval(syncPollTimer);
  syncPollTimer = setInterval(refreshSyncState, 2000);
}

function stopSyncPolling() {
  clearInterval(syncPollTimer);
  syncPollTimer = null;
}

function openSyncPanel(title) {
  $("#addSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#webSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#syncSection").hidden = false;
  openMenu(title);
  startSyncPolling();
}

function showSyncPairing() {
  openSyncPanel(t("sync.title"));
  refreshSyncState();
  beginSyncPairing();
}

// The phone half of pairing. A phone shows no QR of its own, so this skips
// the pairing request entirely and offers the camera instead. The scan itself
// belongs to the Android shell, which calls window.onPairingScanned below.
function showSyncPairingOnPhone() {
  openSyncPanel(t("sync.title_phone"));
  clearTimeout(syncExpiryTimer);
  $("#syncQR").innerHTML = "";
  refreshSyncState();
}

// Called by name from the Android side once the camera closes, with the raw
// scanned text or null when the scan was cancelled. Defined on window because
// that is the only handle the native code has on it.
window.onPairingScanned = async function (text) {
  if (!text) return; // Cancelled or unreadable; the panel is still open to try again.
  try {
    const reply = await request("/api/app/sync/pair-with", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({data: text}),
    });
    showMessage(t("sync.paired", {name: reply.name || ""}));
  } catch (error) {
    showMessage(error.message, true, true);
  }
  refreshSyncState();
};

async function refreshSyncState() {
  if ($("#syncSection").hidden) {
    stopSyncPolling();
    return;
  }
  let state;
  try {
    state = await (await fetch("/api/app/sync")).json();
  } catch {
    return; // The panel already shows what it had; nothing to update yet.
  }
  const peers = state.peers || [];
  renderSyncDevices(peers);
  renderSyncStatus(state.outbox || {}, peers);
  $("#syncPauseToggle").textContent = state.listening ? t("sync.pause") : t("sync.resume");
  // Only once there is somewhere to send: a switch for a thing that cannot
  // happen yet is a question nobody has the context to answer.
  $("#syncAutoSendRow").hidden = !peers.some(peer => peer.listens);
  $("#syncAutoSendToggle").checked = state.autoSend !== false;
}

// The one line that says whether sync is doing its job. Not an error display:
// a computer that is asleep or off the network is the ordinary case, and a
// phone that shouted about it would be shouting most of the time. So the
// server's failure, if there is one, is folded into the same sentence as the
// count: pictures are waiting, which is true and actionable.
function renderSyncStatus(outbox, peers) {
  const line = $("#syncStatus");
  const sendable = peers.some(peer => peer.listens);
  $("#syncSendNow").hidden = !sendable;
  if (!sendable) {
    line.hidden = true;
    return;
  }
  line.hidden = false;
  const waiting = outbox.waiting || 0;
  if (outbox.sending) line.textContent = t("sync.sending");
  else if (waiting === 0) line.textContent = t("sync.all_sent");
  else if (waiting === 1) line.textContent = t("sync.waiting_one");
  else line.textContent = t("sync.waiting_many", {count: waiting});
  line.classList.toggle("sync-status-working", Boolean(outbox.sending) || waiting > 0);
}

function renderSyncDevices(peers) {
  const list = $("#syncDeviceList");
  $("#syncNoDevices").hidden = peers.length > 0;
  list.hidden = peers.length === 0;
  list.innerHTML = "";
  const now = Date.now() / 1000;
  for (const peer of peers) {
    const item = document.createElement("li");
    item.className = "sync-device";
    const seen = document.createElement("span");
    seen.className = "sync-device-status";
    // "nearby" for anything seen in the last five minutes, which is the
    // window a phone on the same wifi realistically checks in within. Older
    // than that reads as a plain date rather than a stale "2 min ago" that
    // quietly became "3 weeks ago" and kept looking recent.
    if (!peer.lastSeen) seen.textContent = t("sync.last_seen_never");
    else if (now - peer.lastSeen < 300) seen.textContent = t("sync.last_seen_now");
    else seen.textContent = new Date(peer.lastSeen * 1000).toLocaleDateString();
    const name = document.createElement("strong");
    name.textContent = peer.name || peer.id;
    const forget = document.createElement("button");
    forget.type = "button";
    forget.textContent = t("sync.forget");
    forget.onclick = () => forgetSyncPeer(peer.id);
    item.append(name, seen, forget);
    list.append(item);
  }
}

async function forgetSyncPeer(id) {
  try {
    await fetch(`/api/app/sync/peers/${encodeURIComponent(id)}`, { method: "DELETE" });
  } catch {
    // The button already reflects the attempt; a retry is just pressing it
    // again, and there is nothing more specific to say about a LAN request
    // that did not land.
  }
  refreshSyncState();
}

async function beginSyncPairing() {
  clearTimeout(syncExpiryTimer);
  $("#syncExpiry").textContent = t("sync.expiry");
  let reply;
  try {
    reply = await (await fetch("/api/app/sync/pairing", { method: "POST" })).json();
  } catch {
    // Sync is not reachable at all (attachSync failed at startup, most
    // likely a read-only data directory). The menu button that opens this
    // panel is already hidden in that case; reaching here regardless fails
    // quietly rather than with a wall of red text over a QR code.
    return;
  }
  $("#syncQR").innerHTML = reply.qrSVG || "";
  // Refreshed a little before the server's own deadline, so there is no
  // moment where a phone might scan a code between it going stale on the
  // server and this screen noticing.
  syncExpiryTimer = setTimeout(beginSyncPairing, Math.max(5, (reply.expiresIn || 180) - 10) * 1000);
}

$("#syncNewCode").onclick = beginSyncPairing;
$("#syncSendNow").onclick = async () => {
  try {
    await fetch("/api/app/sync/now", { method: "POST" });
  } catch {
    // The status line is the answer, and it comes from the server rather than
    // from whether this request happened to land.
  }
  refreshSyncState();
};
$("#syncAutoSendToggle").onchange = async () => {
  const autoSend = $("#syncAutoSendToggle").checked;
  try {
    await request("/api/app/sync/auto-send", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({autoSend}),
    });
  } catch (error) {
    $("#syncAutoSendToggle").checked = !autoSend;
    showMessage(error.message, true);
  }
  refreshSyncState();
};
$("#syncPauseToggle").onclick = async () => {
  const pausing = $("#syncPauseToggle").textContent === t("sync.pause");
  try {
    await fetch(`/api/app/sync/${pausing ? "pause" : "listen"}`, { method: "POST" });
  } catch {
    // Nothing to report; refreshSyncState below shows whatever the truth
    // turns out to be.
  }
  refreshSyncState();
};

// An empty library is the moment a Pinterest board is most useful, so the
// welcome screen offers it and turns the plugin on for whoever asks.
async function startPinterestOnboarding() {
  if (!appState?.plugins?.pinterest?.enabled) {
    $("#pinterestPluginToggle").checked = true;
    await togglePinterestPlugin();
  }
  if (!appState?.plugins?.pinterest?.enabled) return;
  showPinterestImport();
}

function showPinterestImport() {
  $("#addSection").hidden = true;
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#webSection").hidden = true;
  $("#pinterestSection").hidden = false;
  openMenu(t("pinterest.drawer_title"));
  if (!pinterestRunning) $("#pinterestBoardURL").focus();
}

async function importPinterestBoard(event) {
  event.preventDefault();
  if (pinterestRunning) return;
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
  resultBox.hidden = true;
  resultBox.classList.remove("error");
  showPinterestWorking();
  let started;
  try {
    started = await request("/api/app/plugins/pinterest/import", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({
        url: rawURL,
        mode,
        folder: mode === "existing" ? $("#pinterestExistingFolder").value : "",
        skipExisting: $("#pinterestSkipExisting").checked,
      }),
    });
  } catch (error) {
    showPinterestFailure(error.status === 409 ? t("pinterest.already_running") : error.message);
    return;
  }
  // Nothing here needs the panel any more, so hand the board off and put the
  // library back in front of the user.
  // showMenuHome reopens the drawer, so it has to run before the close.
  showMenuHome();
  closeMenu();
  lastPinterestFolder = "";
  $("#pinterestBoardURL").value = "";
  $("#pinterestImportResult").hidden = true;
  showMessage(t("pinterest.handed_off", {board: started.board}), false, false, 10);
  watchPinterestImport();
}

// A board does not always deserve a folder of its own. Several boards about one
// subject belong together, and the only way to do that used to be importing
// straight into the library and sorting it by hand afterwards, so the folders
// already in the library are offered as a destination.
function renderPinterestFolders() {
  const select = $("#pinterestExistingFolder");
  const choice = $("#pinterestExistingChoice");
  const folders = appState.tags || [];
  const chosen = select.value;
  select.replaceChildren(...folders.map(folder => {
    const option = document.createElement("option");
    option.value = folder.name;
    option.textContent = `${folder.name} (${folder.count})`;
    return option;
  }));
  if (folders.some(folder => folder.name === chosen)) select.value = chosen;
  // Offering an empty list would be offering nothing. Somebody with no folders
  // yet gets the board folder, which is what makes their first one.
  choice.hidden = !folders.length;
  if (!folders.length && document.querySelector('input[name="pinterestMode"]:checked')?.value === "existing") {
    document.querySelector('input[name="pinterestMode"][value="board"]').checked = true;
  }
  syncPinterestFolderChoice();
}

function syncPinterestFolderChoice() {
  const mode = document.querySelector('input[name="pinterestMode"]:checked')?.value;
  $("#pinterestExistingFolder").hidden = mode !== "existing" || !(appState.tags || []).length;
}

function showPinterestWorking(status) {
  pinterestRunning = true;
  const title = $("#pinterestWorkingTitle");
  if (status && status.stopping) {
    title.textContent = t("pinterest.stopping");
  } else if (status && status.phase === "importing" && status.total) {
    title.textContent = t("pinterest.progress_importing", {count: status.done, total: status.total});
  } else if (status && status.done) {
    title.textContent = t("pinterest.progress_downloading", {count: status.done});
  } else {
    title.textContent = t("pinterest.downloading_board");
  }
  $("#pinterestWorkingHelp").textContent = t("pinterest.keeps_running");
  $("#pinterestImportButton").disabled = true;
  $("#pinterestImportButton").textContent = t("pinterest.downloading");
  $("#pinterestCancelImport").hidden = false;
  $("#pinterestWorking").hidden = false;
  $("#pinterestImportForm").querySelectorAll("input, select").forEach(input => { input.disabled = true; });
}

function clearPinterestWorking() {
  pinterestRunning = false;
  $("#pinterestWorking").hidden = true;
  $("#pinterestCancelImport").hidden = true;
  $("#pinterestImportForm").querySelectorAll("input, select").forEach(input => { input.disabled = false; });
  $("#pinterestImportButton").disabled = appState.plugins?.pinterest?.available === false;
  $("#pinterestImportButton").textContent = t("pinterest.download_all");
}

function showPinterestFailure(message) {
  clearPinterestWorking();
  $("#pinterestResultIcon").textContent = "!";
  $("#pinterestResultTitle").textContent = t("pinterest.failed_title");
  $("#pinterestResultDetails").textContent = message;
  $("#pinterestOpenFolder").hidden = true;
  $("#pinterestImportAnother").hidden = false;
  $("#pinterestImportResult").classList.add("error");
  $("#pinterestImportResult").hidden = false;
}

// Stopping keeps whatever already downloaded, so the result still reports it.
async function showPinterestStopped(result) {
  clearPinterestWorking();
  if (result && (result.imported || result.linked)) await refreshAfterImport(true);
  const kept = Boolean(result && (result.imported || result.linked || result.skipped));
  const details = [];
  if (kept) {
    details.push(t("pinterest.result_added", {count: result.imported || 0}));
    if (result.skipped) details.push(t("pinterest.result_skipped", {count: result.skipped}));
    if (result.linked) details.push(t("pinterest.result_linked", {count: result.linked}));
  }
  lastPinterestFolder = (result && result.folder) || "";
  $("#pinterestResultIcon").textContent = "\u00d7";
  $("#pinterestResultTitle").textContent = t("pinterest.cancelled");
  $("#pinterestResultDetails").textContent = kept
    ? t("pinterest.cancelled_kept") + " " + details.join(" \u00b7 ")
    : t("pinterest.cancelled_empty");
  $("#pinterestOpenFolder").hidden = !lastPinterestFolder;
  $("#pinterestImportAnother").hidden = false;
  $("#pinterestImportResult").classList.add("error");
  $("#pinterestImportResult").hidden = false;
}

async function showPinterestResult(result) {
  clearPinterestWorking();
  if (result.imported || result.linked) await refreshAfterImport(true);
  const details = [t("pinterest.result_added", {count: result.imported})];
  if (result.skipped) details.push(t("pinterest.result_skipped", {count: result.skipped}));
  if (result.linked) details.push(t("pinterest.result_linked", {count: result.linked}));
  if (result.failed) details.push(t("pinterest.result_failed", {count: result.failed}));
  lastPinterestFolder = result.folder || "";
  $("#pinterestResultIcon").textContent = result.failed ? "!" : "\u2713";
  $("#pinterestResultTitle").textContent = t(result.failed ? "pinterest.result_partial" : "pinterest.result_success", {board: result.board});
  $("#pinterestResultDetails").textContent = details.join(" \u00b7 ");
  $("#pinterestOpenFolder").hidden = !lastPinterestFolder;
  $("#pinterestImportAnother").hidden = false;
  $("#pinterestImportResult").classList.toggle("error", Boolean(result.failed));
  $("#pinterestImportResult").hidden = false;
  showMessage(t("pinterest.imported_notice", {board: result.board}), false, false, 8);
}

// The import runs in Pictogrep, not in this window, so closing the panel or the
// tab leaves it downloading. Whatever window is open next picks the result up.
async function watchPinterestImport() {
  if (pinterestWatching) return;
  pinterestWatching = true;
  let failures = 0;
  try {
    for (;;) {
      let status = null;
      try {
        status = await request("/api/app/plugins/pinterest/import");
        failures = 0;
      } catch (error) {
        if (++failures > 5) { showPinterestFailure(error.message); return; }
      }
      // The web panel shares this downloader, so a job that belongs to it is
      // not this panel's to report.
      if (status && status.kind === "web") {
        clearPinterestWorking();
        return;
      }
      if (status && status.state !== "running") {
        if (status.state === "done") await showPinterestResult(status.result || {});
        else if (status.state === "cancelled") await showPinterestStopped(status.result);
        else if (status.state === "error") showPinterestFailure(status.error || t("pinterest.failed_title"));
        else clearPinterestWorking();
        return;
      }
      if (status) showPinterestWorking(status);
      await new Promise(resolve => setTimeout(resolve, 2000));
    }
  } finally {
    pinterestWatching = false;
  }
}

async function resumePinterestImport() {
  // Either panel may have started what is running, so this bows out only when
  // both importers are off.
  if (appState.plugins?.pinterest?.enabled === false && appState.plugins?.web?.enabled === false) return;
  try {
    const status = await request("/api/app/plugins/pinterest/import");
    if (status.state !== "running") return;
    // A weekly check nobody started should not open the import panel and look
    // like something went off on its own. It refreshes the library quietly when
    // it lands, and the panel stays for imports the reader actually asked for.
    if (status.automatic) {
      showAutoSyncStatus(status);
      watchAutomaticPinterestSync();
      return;
    }
    // Both panels share one downloader, so a running job has to go back to the
    // panel that started it. Without this a followed website reported itself as
    // a Pinterest board, down to the "import another board" wording.
    if (status.kind === "web") {
      showWebWorking(status);
      watchWebImport();
      return;
    }
    showPinterestWorking(status);
    watchPinterestImport();
  } catch (_) {}
}

// A download nobody started still has to be visible and still has to be
// stoppable. Quiet is not the same as hidden: the strip names the board, says
// how far along it is, and carries the stop button, because the import panel it
// used to live in never opens for an automatic check.
function showAutoSyncStatus(status) {
  const strip = $("#autoSyncStatus");
  const done = Number(status.done || 0);
  const prefix = status.kind === "web" ? "web" : "pinterest";
  $("#autoSyncText").textContent = done
    ? t(prefix + ".auto_running_count", {board: status.board || "", count: done})
    : t(prefix + ".auto_running", {board: status.board || ""});
  $("#autoSyncStop").disabled = Boolean(status.stopping);
  if (status.stopping) $("#autoSyncText").textContent = t("pinterest.stopping");
  strip.hidden = false;
}

function hideAutoSyncStatus() {
  $("#autoSyncStatus").hidden = true;
  $("#autoSyncStop").disabled = false;
}

async function watchAutomaticPinterestSync() {
  if (pinterestWatching) return;
  pinterestWatching = true;
  try {
    for (;;) {
      let status = null;
      try {
        status = await request("/api/app/plugins/pinterest/import");
      } catch (_) {
        hideAutoSyncStatus();
        return;
      }
      if (status.state !== "running") {
        hideAutoSyncStatus();
        const added = Number(status.result?.saved || 0);
        if (added > 0) {
          showMessage(t((status.kind === "web" ? "web" : "pinterest") + ".auto_added", {count: added, board: status.board || ""}));
          await refreshAfterImport(!$("#imagesPanel").hidden);
        }
        return;
      }
      showAutoSyncStatus(status);
      await new Promise(resolve => setTimeout(resolve, 4000));
    }
  } finally {
    pinterestWatching = false;
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

// The automatic check runs in Pictogrep, so the window only reports what it
// already found. An update it has installed is waiting for the next launch,
// which is the one thing worth interrupting anybody about.
function renderAutoUpdate() {
  const update = appState?.update || {};
  const toggle = $("#autoUpdateToggle");
  if (toggle) {
    toggle.checked = update.autoUpdate !== false;
    // An installation somebody else owns, like a Nix or system package, is not
    // Pictogrep's to replace. It still says when a new version exists.
    toggle.disabled = update.canSelfUpdate === false;
    $("#autoUpdateHelp").textContent = update.canSelfUpdate === false
      ? t("update.auto_managed", {method: appState?.updateMethod || ""})
      : t("update.auto_help");
  }
  // The line above the button describes how updates arrive here. A manual
  // check owns it while it runs, so this only writes the resting state.
  const status = $("#updateStatus");
  if (status && !status.dataset.manual) {
    status.className = "";
    status.textContent = update.canSelfUpdate === false
      ? t("update.on_demand")
      : t(update.autoUpdate === false ? "update.on_demand" : "update.automatic");
  }
  const ready = $("#updateReady");
  if (!ready) return;
  if (update.installedVersion) {
    ready.className = "update-ready success";
    ready.textContent = t("update.installed_restart", {version: update.installedVersion});
    ready.hidden = false;
    return;
  }
  if (update.available && update.latestVersion) {
    ready.className = "update-ready";
    ready.textContent = t("update.available", {version: update.latestVersion});
    ready.hidden = false;
    return;
  }
  ready.hidden = true;
}

async function checkForUpdates() {
  const button = $("#checkForUpdates");
  const status = $("#updateStatus");
  status.dataset.manual = "1";
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
$("#showSync").onclick = showSyncPairing;
$("#showSyncPhone").onclick = showSyncPairingOnPhone;
$("#syncScanButton").onclick = () => window.AndroidBridge?.scanForPairing?.();
$("#showPlugins").onclick = showPlugins;
$("#showOnboarding").onclick = () => {
  closeMenu();
  window.PictogrepOnboarding?.start();
};
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
$("#cancelDeleteFolder").onclick = () => $("#deleteFolderDialog").close();
$("#deleteFolderForm").onsubmit = event => {
  event.preventDefault();
  const folder = folderPendingDelete;
  folderPendingDelete = null;
  $("#deleteFolderDialog").close();
  if (folder) deleteFolder(folder);
};
$("#clearRecentSearches").onclick = clearRecentSearchHistory;
$("#wikimediaPluginToggle").onchange = toggleWikimediaPlugin;
$("#calendarPluginToggle").onchange = toggleCalendarPlugin;
$("#sidebarPluginToggle").onchange = toggleSidebarPlugin;
$("#vimPluginToggle").onchange = toggleVimPlugin;
$("#commandPalettePluginToggle").onchange = toggleCommandPalettePlugin;
$("#canvasPluginToggle").onchange = toggleCanvasPlugin;
$("#pinterestPluginToggle").onchange = togglePinterestPlugin;
$("#pinterestAutoSyncToggle").onchange = async () => {
  const autoSync = $("#pinterestAutoSyncToggle").checked;
  try {
    await request("/api/app/settings/pinterest", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({autoSync}),
    });
    await refreshState();
    renderFollowedBoards();
  } catch (error) {
    $("#pinterestAutoSyncToggle").checked = !autoSync;
    showMessage(error.message, true);
  }
};
$("#autoUpdateToggle").onchange = async () => {
  const autoUpdate = $("#autoUpdateToggle").checked;
  try {
    await request("/api/app/settings/update", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({autoUpdate}),
    });
    await refreshState();
  } catch (error) {
    $("#autoUpdateToggle").checked = !autoUpdate;
    showMessage(error.message, true);
  }
};
$("#checkForUpdates").onclick = checkForUpdates;
$("#installUpdate").onclick = installAvailableUpdate;
$("#showAdd").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#syncSection").hidden = true;
  $("#webSection").hidden = true;
  $("#addSection").hidden = !$("#addSection").hidden;
};
$("#emptyAddImages").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#syncSection").hidden = true;
  $("#webSection").hidden = true;
  $("#addSection").hidden = false;
  openMenu();
};
$("#emptyPinterest").onclick = startPinterestOnboarding;
$("#emptyPinterestPhone").onclick = startPinterestOnboarding;
$("#imageFiles").onchange = event => uploadFiles(event.target.files);
$("#pasteURLForm").onsubmit = pasteImageURL;
$("#chooseFolder").onclick = openFolderPickerDialog;
$("#addSourceFolder").onclick = openFolderPickerDialog;
$("#autoSyncStop").onclick = async () => {
  $("#autoSyncStop").disabled = true;
  try {
    await request("/api/app/plugins/pinterest/import", {method: "DELETE"});
  } catch (error) {
    $("#autoSyncStop").disabled = false;
    showMessage(error.message, true);
  }
};
$("#folderPickerClose").onclick = () => $("#folderPickerDialog").close();
$("#pinterestImportForm").onsubmit = importPinterestBoard;
$("#pinterestImportForm").querySelectorAll('input[name="pinterestMode"]')
  .forEach(radio => { radio.onchange = syncPinterestFolderChoice; });
$("#pinterestCancelImport").onclick = () => request("/api/app/plugins/pinterest/import", {method: "DELETE"}).catch(() => {});
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
  relatedPaging = null;
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

/** What /api/app/heartbeat last reported, so a repeat only means "still open" and a rise means "something arrived". */
let lastKnownArrivals = null;

/**
 * A picture that arrives over sync should appear on its own, the same way one
 * dropped on the window does, without anybody reloading the tab to go find
 * it. The heartbeat this reuses already exists to keep the server from
 * idling out an open tab, so this is one request doing two jobs rather than
 * a second timer: a page sitting on the library sees new pictures inside a
 * few seconds of a phone delivering them, at the cost of one small request
 * this page was already sending.
 */
function watchForArrivals() {
  setInterval(async () => {
    if (document.visibilityState === "hidden") return;
    let state;
    try { state = await (await fetch("/api/app/heartbeat", {cache: "no-store"})).json(); }
    catch { return; }
    const arrivals = state?.arrivals ?? 0;
    if (lastKnownArrivals !== null && arrivals > lastKnownArrivals) {
      // A picture that arrived on its own gets the unblur every slow load
      // already gets. It is the one moment the effect is telling the truth
      // rather than covering a wait: something really did just turn up, and
      // resolving into place is how the eye is told which one is new.
      revealNextImages = true;
      refreshLibraryIndex()
        .catch(() => {})
        .finally(() => { revealNextImages = false; });
    }
    lastKnownArrivals = arrivals;
  }, 3000);
}

/** What /api/app/sync last reported waiting, so a fall to zero can be told apart from "nothing was ever waiting". */
let lastKnownOutboxWaiting = null;

/**
 * The one confirmation a phone offers for a send it never asked the user to
 * watch: waiting went from something to nothing, so whatever was held for
 * the desktop is there now. Polled globally rather than only while the
 * Connect to computer panel is open, because the whole point of the outbox
 * is that nobody has to open that panel for a send to happen.
 */
function watchOutboxForConfirmation() {
  setInterval(async () => {
    if (document.visibilityState === "hidden") return;
    let state;
    try { state = await (await fetch("/api/app/sync", {cache: "no-store"})).json(); }
    catch { return; }
    const waiting = state?.outbox?.waiting ?? 0;
    const hadSomethingWaiting = lastKnownOutboxWaiting !== null && lastKnownOutboxWaiting > 0;
    if (hadSomethingWaiting && waiting === 0 && (state.peers || []).some(peer => peer.listens)) {
      showMessage(t("sync.all_sent"));
    }
    lastKnownOutboxWaiting = waiting;
  }, 4000);
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
  watchImageScroll();
  // Together, not one after the other: the folder list is not built from the
  // pictures and neither waits on the other's answer, so running them in
  // series spent one whole request's latency on nothing. The pictures are
  // still what the eye is waiting for, and they no longer queue behind a
  // list that is off screen until the Folders tab is opened.
  await Promise.all([loadImages(), loadFolders()]);
  await syncViewerFromHistory();
  if (appState?.index?.count) scheduleSemanticIndex(700);
  setTimeout(refreshLibraryWhenDue, 1400);
  setInterval(refreshLibraryWhenDue, 5 * 60 * 1000);
  watchForArrivals();
  if (appState.mobile) watchOutboxForConfirmation();
  resumePinterestImport();
  // First run only, and only while there is nothing to look at. Once it has
  // been through, or closed, Pictogrep does not ask again.
  //
  // Never on a phone. The empty library already explains itself, in the same
  // words and on the screen the app opens on, and the only thing the phone
  // path of this flow adds is a modal in front of it saying so again. The
  // desktop keeps it: there it is what asks for a folder to read, which is
  // the one thing a desktop library cannot start without.
  const wanted = !appState.mobile && !appState.onboarding?.completed && !appState.index?.count;
  if (wanted) window.PictogrepOnboarding?.start();
}

// The bridge the onboarding flow talks to. It is the whole contract between
// web/onboarding.js and this file: onboarding never reaches into library
// internals, so a screen can be rewritten without touching anything here, and
// anything new a screen needs gets added as one more function below.
// Walks real folders through /api/app/browse and hands back the one that was
// chosen. Both the onboarding screen and the Add drawer render this, so there is
// one folder picker in Pictogrep and one meaning of "choose a folder": the
// pictures are read where they already are.
function renderFolderBrowser(container, {onChoose, chooseLabel = null} = {}) {
  const path = document.createElement("div");
  path.className = "onboarding-path";
  const list = document.createElement("div");
  list.className = "onboarding-folders";
  const use = document.createElement("button");
  use.type = "button";
  use.className = "onboarding-go";
  use.disabled = true;
  const note = document.createElement("p");
  note.className = "onboarding-note";
  note.textContent = t("onboarding.folder.stays");
  // Clicking down from the home folder never reaches a second drive without a
  // long climb through the root, so the path can also be typed or pasted. The
  // backend already accepts any absolute path.
  const jump = document.createElement("form");
  jump.className = "onboarding-jump";
  const jumpInput = document.createElement("input");
  jumpInput.type = "text";
  jumpInput.className = "onboarding-jump-input";
  jumpInput.spellcheck = false;
  jumpInput.autocapitalize = "off";
  jumpInput.setAttribute("aria-label", t("onboarding.folder.jump_label"));
  jumpInput.placeholder = t("onboarding.folder.jump_placeholder");
  const jumpGo = document.createElement("button");
  jumpGo.type = "submit";
  jumpGo.className = "onboarding-jump-go";
  jumpGo.textContent = t("onboarding.folder.jump");
  jump.append(jumpInput, jumpGo);
  jump.onsubmit = event => {
    event.preventDefault();
    const target = jumpInput.value.trim();
    target && show(target);
  };
  let current = "";

  const quiet = text => {
    const line = document.createElement("p");
    line.className = "onboarding-quiet";
    line.textContent = text;
    return line;
  };

  async function show(target) {
    list.replaceChildren(quiet(t("onboarding.folder.loading")));
    try {
      const data = await request(`/api/app/browse?path=${encodeURIComponent(target || "")}`);
      current = data.path;
      jumpInput.value = data.path;
      path.replaceChildren();
      if (data.parent) {
        const up = document.createElement("button");
        up.type = "button";
        up.className = "onboarding-up";
        up.textContent = "↑";
        up.setAttribute("aria-label", t("onboarding.folder.up"));
        up.onclick = () => show(data.parent);
        path.append(up);
      }
      const value = document.createElement("span");
      value.className = "onboarding-path-value";
      value.textContent = data.path;
      value.title = data.path;
      path.append(value);
      // A count of zero only means "empty" when the walk actually finished. It
      // gives up on slow drives and deep trees, and a folder full of pictures
      // was refused because the counter ran out of time before it saw one.
      const unknown = data.images === 0 && data.truncated;
      use.disabled = data.images === 0 && !data.truncated;
      use.textContent = unknown
        ? chooseLabel?.(data) || t("onboarding.folder.use_unknown")
        : data.images === 0
          ? t("onboarding.folder.none_here")
          : chooseLabel?.(data) || t("onboarding.folder.use", {count: data.images + (data.truncated ? "+" : "")});
      list.replaceChildren(...(data.folders.length
        ? data.folders.map(folder => {
          const button = document.createElement("button");
          button.type = "button";
          button.className = "onboarding-folder";
          button.textContent = folder.name;
          button.onclick = () => show(folder.path);
          return button;
        })
        : [quiet(t("onboarding.folder.no_subfolders"))]));
    } catch (error) {
      list.replaceChildren(quiet(error.message));
      use.disabled = true;
    }
  }

  use.onclick = async () => {
    use.disabled = true;
    use.textContent = t("onboarding.folder.starting");
    try {
      await onChoose(current);
    } catch (error) {
      list.replaceChildren(quiet(error.message));
      use.disabled = false;
    }
  };

  container.append(path, list, jump, use, note);
  show("");
}

function openFolderPickerDialog() {
  closeMenu();
  const body = $("#folderPickerBody");
  body.replaceChildren();
  renderFolderBrowser(body, {
    onChoose: async folder => {
      await startIndex({folders: [folder]}, {announce: true});
      $("#folderPickerDialog").close();
    },
  });
  $("#folderPickerDialog").showModal();
}

window.PictogrepApp = {
  renderFolderBrowser,
  browseFolders: path => request(`/api/app/browse?path=${encodeURIComponent(path || "")}`),

  // Index a folder in place. Nothing is copied or moved: the scan records where
  // the pictures already are, which is what the onboarding promises.
  indexFolder: path => startIndex({folders: [path]}, {announce: true}),

  startPinterest: () => startPinterestOnboarding(),

  /** Running inside the Android app, where pictures arrive through the share sheet. */
  isMobile: () => Boolean(appState?.mobile),

  setLanguage: async locale => {
    await request("/api/app/settings/language", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({language: locale}),
    });
    appState.language = locale;
    await window.PictogrepI18n.init(locale);
    renderState();
  },

  enterLibrary: () => {
    switchTab("images");
    if (!browserImages.length) loadImages();
  },

  completeOnboarding: () => request("/api/app/onboarding", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({completed: true}),
  }),
};


// Import from web
//
// One downloader serves both importers, so a running board import and a running
// website import are the same job. The panel that started one watches it; the
// other one just stays disabled until it is free.

let webImportRunning = false;
let webImportWatching = false;
let lastWebFolder = "";

function showWebImport() {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#settingsSection").hidden = true;
  $("#pluginsSection").hidden = true;
  $("#pinterestSection").hidden = true;
  $("#syncSection").hidden = true;
  $("#webSection").hidden = false;
  openMenu(t("web.title"));
  renderFollowedWebSources();
  if (!webImportRunning) $("#webSourceURL").focus();
}

async function fetchFollowedWebSources() {
  if (!appState?.plugins?.web?.enabled) return [];
  try {
    return (await request("/api/app/plugins/web/sources")).sources || [];
  } catch (_) {
    return [];
  }
}

// The same list is useful in two places: while deciding whether to follow a new
// site, and later in settings when deciding to stop.
async function renderFollowedWebSources() {
  const sources = await fetchFollowedWebSources();
  const summary = $("#webSourceSummary");
  if (summary) summary.hidden = Boolean(sources.length) || !appState?.plugins?.web?.enabled;
  ["#webSourceList", "#webSectionSourceList"].forEach(selector => {
    const list = $(selector);
    if (!list) return;
    if (!sources.length) {
      list.replaceChildren();
      list.hidden = true;
      return;
    }
    list.replaceChildren(...sources.map(source => followedWebSourceRow(source)));
    list.hidden = false;
  });
}

function followedWebSourceRow(source) {
  const row = document.createElement("div");
  row.className = "followed-board";
  const text = document.createElement("span");
  const name = document.createElement("strong");
  name.textContent = source.name || source.url;
  name.title = source.url;
  const when = document.createElement("small");
  const folder = source.folder ? t("web.into_folder", {folder: source.folder}) : "";
  when.textContent = folder ? folder + " \u00b7 " + lastCheckedLabel(source.lastSyncAt) : lastCheckedLabel(source.lastSyncAt);
  text.append(name, when);
  const stop = document.createElement("button");
  stop.type = "button";
  stop.textContent = t("pinterest.unfollow");
  stop.onclick = async () => {
    stop.disabled = true;
    try {
      await request("/api/app/plugins/web/sources", {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({forget: source.url}),
      });
      showMessage(t("web.unfollowed", {site: source.name || source.url}));
      await renderFollowedWebSources();
    } catch (error) {
      stop.disabled = false;
      showMessage(error.message, true);
    }
  };
  row.append(text, stop);
  return row;
}

async function startWebImport(event) {
  event.preventDefault();
  if (webImportRunning) return;
  const backfill = $("#webBackfill").checked;
  const follow = $("#webFollow").checked;
  if (!backfill && !follow) {
    showWebFailure(t("web.pick_one"));
    return;
  }
  const rawURL = $("#webSourceURL").value.trim();
  let parsedURL = null;
  try { parsedURL = new URL(rawURL); } catch (_) {}
  if (!parsedURL || !/^https?:$/.test(parsedURL.protocol)) {
    showWebFailure(t("web.invalid_help"));
    $("#webSourceURL").focus();
    return;
  }
  $("#webImportResult").hidden = true;
  showWebWorking();
  let started = null;
  try {
    started = await request("/api/app/plugins/web/import", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({
        url: rawURL,
        folder: $("#webSourceFolder").value.trim(),
        backfill,
        follow,
      }),
    });
  } catch (error) {
    showWebFailure(error.status === 409 ? t("web.already_running") : error.message);
    return;
  }
  lastWebFolder = "";
  showMessage(t("web.handed_off", {site: started.board}), false, false, 10);
  watchWebImport();
}

function showWebWorking(status) {
  webImportRunning = true;
  const title = $("#webWorkingTitle");
  if (status && status.stopping) title.textContent = t("pinterest.stopping");
  else if (status && status.phase === "importing") title.textContent = t("pinterest.progress_importing", {count: status.done, total: status.total});
  else if (status && status.done) title.textContent = t("pinterest.progress_downloading", {count: status.done});
  else title.textContent = t("web.downloading");
  $("#webImportButton").disabled = true;
  $("#webImportButton").textContent = t("web.downloading");
  $("#webCancelImport").hidden = false;
  $("#webWorking").hidden = false;
  $("#webImportForm").querySelectorAll("input").forEach(input => { input.disabled = true; });
}

function clearWebWorking() {
  webImportRunning = false;
  $("#webWorking").hidden = true;
  $("#webCancelImport").hidden = true;
  $("#webImportButton").disabled = false;
  $("#webImportButton").textContent = t("web.start");
  $("#webImportForm").querySelectorAll("input").forEach(input => { input.disabled = false; });
}

function showWebFailure(message) {
  clearWebWorking();
  $("#webResultIcon").textContent = "!";
  $("#webResultTitle").textContent = t("web.failed_title");
  $("#webResultDetails").textContent = message;
  $("#webOpenFolder").hidden = true;
  $("#webImportResult").classList.add("error");
  $("#webImportResult").hidden = false;
}

async function showWebResult(result, cancelled) {
  clearWebWorking();
  if (result.imported || result.linked) await refreshAfterImport(true);
  const details = [t("pinterest.result_added", {count: result.imported || 0})];
  if (result.skipped) details.push(t("pinterest.result_skipped", {count: result.skipped}));
  if (result.failed) details.push(t("pinterest.result_failed", {count: result.failed}));
  lastWebFolder = result.folder || "";
  $("#webResultIcon").textContent = cancelled ? "\u00d7" : (result.failed ? "!" : "\u2713");
  $("#webResultTitle").textContent = cancelled
    ? t("pinterest.cancelled")
    : t(result.failed ? "pinterest.result_partial" : "web.result_success", {board: result.board, site: result.board});
  $("#webResultDetails").textContent = details.join(" \u00b7 ");
  $("#webOpenFolder").hidden = !lastWebFolder;
  $("#webImportResult").classList.toggle("error", Boolean(cancelled || result.failed));
  $("#webImportResult").hidden = false;
  await renderFollowedWebSources();
}

// The download runs in Pictogrep, not in this window, so closing the panel or
// the tab leaves it going. Whatever window is open next picks the result up.
async function watchWebImport() {
  if (webImportWatching) return;
  webImportWatching = true;
  let failures = 0;
  try {
    for (;;) {
      let status = null;
      try {
        status = await request("/api/app/plugins/pinterest/import");
        failures = 0;
      } catch (error) {
        if (++failures > 5) { showWebFailure(error.message); return; }
      }
      // An automatic board check can start in the gap between this download
      // finishing and the next poll. Reporting that job here would put a
      // board's result in the web panel, so a job that is not ours ends the
      // watch instead.
      if (status && status.state === "running" && status.kind !== "web") {
        clearWebWorking();
        return;
      }
      if (status && status.state !== "running") {
        if (status.kind && status.kind !== "web") { clearWebWorking(); return; }
        if (status.state === "done") await showWebResult(status.result || {}, false);
        else if (status.state === "cancelled") await showWebResult(status.result || {}, true);
        else if (status.state === "error") showWebFailure(status.error || t("web.failed_title"));
        else clearWebWorking();
        return;
      }
      if (status) showWebWorking(status);
      await new Promise(resolve => setTimeout(resolve, 2000));
    }
  } finally {
    webImportWatching = false;
  }
}

async function toggleWebPlugin() {
  const enabled = $("#webPluginToggle").checked;
  try {
    await request("/api/app/plugins", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: "web", enabled}),
    });
    appState.plugins.web = {...appState.plugins.web, enabled};
    renderState();
    renderFollowedWebSources();
    showMessage(t(enabled ? "web.enabled" : "web.disabled"));
  } catch (error) {
    $("#webPluginToggle").checked = !enabled;
    showMessage(error.message, true);
  }
}

$("#webPluginToggle").onchange = toggleWebPlugin;
$("#webAutoSyncToggle").onchange = async () => {
  const autoSync = $("#webAutoSyncToggle").checked;
  try {
    await request("/api/app/settings/web", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({autoSync}),
    });
    await refreshState();
    renderFollowedWebSources();
  } catch (error) {
    $("#webAutoSyncToggle").checked = !autoSync;
    showMessage(error.message, true);
  }
};
$("#showWebImport").onclick = showWebImport;
$("#webImportForm").onsubmit = startWebImport;
$("#webCancelImport").onclick = async () => {
  $("#webCancelImport").disabled = true;
  try {
    await request("/api/app/plugins/pinterest/import", {method: "DELETE"});
  } catch (error) {
    showMessage(error.message, true);
  } finally {
    $("#webCancelImport").disabled = false;
  }
};
$("#webOpenFolder").onclick = () => {
  if (!lastWebFolder) return;
  closeMenu();
  openFolder({kind: "tag", value: lastWebFolder, name: lastWebFolder});
};
$("#webImportAnother").onclick = () => {
  $("#webImportResult").hidden = true;
  $("#webSourceURL").value = "";
  $("#webSourceFolder").value = "";
  $("#webSourceURL").focus();
};

window.addEventListener("popstate", syncViewerFromHistory);
window.addEventListener("focus", refreshLibraryWhenDue);
document.addEventListener("visibilitychange", refreshLibraryWhenDue);
start();
