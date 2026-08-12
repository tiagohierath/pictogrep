const $ = selector => document.querySelector(selector);

let appState = null;
let currentTag = "";
let currentSource = "";
let currentFolderName = "";
let currentQuery = "";
let messageTimer = null;
let pollTimer = null;
let lastJobState = "idle";
let aiWorker = null;
let aiRequestId = 0;
let imageLoadId = 0;
let semanticIndexPromise = null;
let semanticWarmupPromise = null;
let textWarmupPromise = null;
let quietTextWarmup = false;
let foregroundTextRequests = 0;
let queryPrimeTimer = null;
let currentViewerItem = null;
let relatedLoadId = 0;
let canvasPositions = new Map();
let canvasImages = [];
let canvasPan = {x: 0, y: 0};
let canvasZoom = 1;
let canvasSaveTimer = null;
let canvasPointer = null;
const failedSemanticPaths = new Set();
const aiRequests = new Map();
const semanticVectors = new Map();
const semanticResults = new Map();

async function request(url, options = {}) {
  const response = await fetch(url, options);
  const body = await response.text();
  let data;
  try {
    data = JSON.parse(body);
  } catch (_) {
    throw new Error(`Pictogrep returned an invalid response (${response.status})`);
  }
  if (!response.ok || data.ok === false) throw new Error(data.error || `Request failed (${response.status})`);
  return data;
}

function getAIWorker() {
  if (aiWorker) return aiWorker;
  aiWorker = new Worker("/assets/ai-worker.js", {type: "module"});
  aiWorker.onmessage = event => {
    const message = event.data;
    if (message.type === "progress") {
      if (message.kind === "text" && quietTextWarmup && foregroundTextRequests === 0) return;
      const detail = message.detail || {};
      if (detail.status === "progress" && Number.isFinite(detail.progress)) {
        showMessage(`Preparing search… ${Math.round(detail.progress)}%`, false, true);
      } else if (detail.status === "initiate") {
        showMessage("Preparing search for the first time…", false, true);
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
  const vector = await runAI("embed", {item});
  await request("/api/app/ai/embeddings", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({model: appState.embeddingModel.key, items: [{path: item.path, mtime: item.mtime, vector}]}),
  });
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

function continueSemanticIndex(items, completed, total) {
  items = items.filter(item => !failedSemanticPaths.has(item.path));
  if (semanticIndexPromise || !items.length) return;
  semanticIndexPromise = (async () => {
    let ready = completed;
    for (let index = 0; index < items.length; index++) {
      try {
        await saveSemanticEmbedding(items[index]);
        ready++;
      } catch (error) {
        if (error.kind !== "image") throw error;
        failedSemanticPaths.add(items[index].path);
        continue;
      }
      showMessage(`Making search better: ${ready} of ${total} pictures…`, false, true);
      if (ready % 24 === 0) {
        await refreshSemanticResults();
        await refreshRelatedResults();
      }
    }
    await refreshSemanticResults();
    await refreshRelatedResults();
    const skipped = total - ready;
    showMessage(skipped > 0
      ? `Search is ready for ${ready} pictures; ${skipped} could not be read.`
      : `Search is ready for all ${total} pictures.`);
  })().catch(error => {
    showMessage(`Search setup paused: ${error.message}`, true);
  }).finally(() => {
    semanticIndexPromise = null;
  });
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
      const total = state.total;
      semanticWarmupPromise = (async () => {
        showMessage("Downloading AI search… This happens only once.", false, true);
        for (let index = 0; index < first.length; index++) {
          try {
            await saveSemanticEmbedding(first[index]);
            showMessage(`First result ready. Improving search for ${total} pictures…`, false, true);
            return;
          } catch (error) {
            if (error.kind !== "image") throw error;
            failedSemanticPaths.add(first[index].path);
          }
        }
        throw new Error("The first pictures could not be read. Try adding a JPG or PNG image.");
      })().then(async () => {
        const updated = await request("/api/app/ai");
        continueSemanticIndex(updated.missing, updated.indexed, updated.total);
        await refreshSemanticResults();
      }).catch(error => {
        showMessage(`AI search could not start. Check your internet connection and try again. ${error.message}`, true, true);
      }).finally(() => {
        semanticWarmupPromise = null;
      });
    }
    const fallback = await request(`/api/app/search?q=${encodeURIComponent(query)}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}&limit=120`);
    fallback.preparing = true;
    return fallback;
  }

  continueSemanticIndex(state.missing, state.indexed, state.total);
  showMessage(`Searching for “${query}”…`, false, true);
  await vectorPromise;
  return requestSemanticResults(query);
}

function showMessage(text, error = false, persist = false) {
  const box = $("#message");
  clearTimeout(messageTimer);
  box.textContent = text;
  box.classList.toggle("error", error);
  box.hidden = false;
  if (!persist) messageTimer = setTimeout(() => { box.hidden = true; }, 3500);
}

function openMenu() {
  $("#drawer").classList.add("open");
  $("#drawer").setAttribute("aria-hidden", "false");
  $("#drawerScrim").hidden = false;
  $("#menuButton").setAttribute("aria-expanded", "true");
}

function closeMenu() {
  $("#drawer").classList.remove("open");
  $("#drawer").setAttribute("aria-hidden", "true");
  $("#drawerScrim").hidden = true;
  $("#menuButton").setAttribute("aria-expanded", "false");
}

function switchTab(tab) {
  const images = tab === "images";
  $("#imagesTab").classList.toggle("active", images);
  $("#foldersTab").classList.toggle("active", !images);
  $("#imagesTab").setAttribute("aria-selected", String(images));
  $("#foldersTab").setAttribute("aria-selected", String(!images));
  $("#imagesPanel").hidden = !images;
  $("#foldersPanel").hidden = images;
}

function setLoading() {
  const grid = $("#imageGrid");
  grid.classList.add("is-loading");
  const item = document.createElement("p");
  item.className = "loading-message";
  item.textContent = "Loading pictures…";
  grid.replaceChildren(item);
  $("#imagesEmpty").hidden = true;
}

function pictureCard(item) {
  const card = document.createElement("article");
  card.className = "image-card";

  const image = document.createElement("img");
  image.src = item.url;
  image.alt = item.name;
  image.loading = "lazy";
  image.decoding = "async";
  if (item.width && item.height) {
    image.width = item.width;
    image.height = item.height;
  }
  image.onclick = () => openImageViewer(item);
  card.append(image);

  const info = document.createElement("div");
  info.className = "image-info";
  const meta = document.createElement("div");
  meta.className = "image-meta";
  (item.tags || []).slice(0, 2).forEach(value => {
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = `#${value}`;
    meta.append(tag);
  });
  info.append(meta);
  card.append(info);

  const menuButton = document.createElement("button");
  menuButton.className = "card-menu-button";
  menuButton.type = "button";
  menuButton.textContent = "⋯";
  menuButton.setAttribute("aria-label", `Options for ${item.name}`);
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
  draw.textContent = "Draw";
  const tagButton = document.createElement("button");
  tagButton.type = "button";
  tagButton.textContent = "Add to folder";
  tagButton.onclick = () => {
    closeCardMenus();
    openTagDialog(item.id);
  };
  menu.append(menuName, draw, tagButton);
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
  $("#contextDraw").href = `/practice?image=${encodeURIComponent(item.id)}`;
  $("#contextAddFolder").onclick = () => {
    closeCardMenus();
    openTagDialog(item.id);
  };
  menu.hidden = false;
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

function openImageViewer(item) {
  const viewer = $("#imageViewer");
  showViewerImage(item);
  if (!viewer.open) viewer.showModal();
}

function showViewerImage(item) {
  const viewer = $("#imageViewer");
  const image = $("#viewerImage");
  const picture = image.parentElement;
  currentViewerItem = item;
  picture.classList.remove("is-scrollable-portrait");
  const setPortraitMode = (width, height) => {
    if (currentViewerItem?.id !== item.id) return;
    picture.classList.toggle("is-scrollable-portrait", width > 0 && height / width > 3.25);
  };
  image.onload = () => setPortraitMode(image.naturalWidth, image.naturalHeight);
  image.src = item.url;
  image.alt = item.name;
  bindImageContextMenu(image, item);
  if (item.width && item.height) setPortraitMode(item.width, item.height);
  viewer.scrollTop = 0;
  renderViewerTags(item.tags || []);
  loadRelatedImages(item);
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
    empty.textContent = "No tags yet";
    children.push(empty);
  }
  const add = document.createElement("button");
  add.className = "viewer-add-tag";
  add.type = "button";
  add.textContent = "+ Add tag";
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
    const image = document.createElement("img");
    image.src = item.url;
    image.alt = item.name;
    image.loading = "lazy";
    button.append(image);
    button.onclick = () => showViewerImage(item);
    bindImageContextMenu(button, item);
    return button;
  }));
  const status = $("#relatedStatus");
  if (!data.ready) status.textContent = "Preparing this picture for similarity search…";
  else if (!data.images.length && data.indexed < data.total) status.textContent = "Similar pictures will appear as indexing finishes.";
  else if (!data.images.length) status.textContent = "No similar pictures yet.";
  else if (data.indexed < data.total) status.textContent = `Comparing ${data.indexed} of ${data.total} indexed pictures; results will improve in the background.`;
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
  $("#relatedStatus").textContent = "Finding similar pictures…";
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
    $("#relatedStatus").textContent = `Similar pictures are not ready: ${error.message}`;
  }
}

function renderImages(images, total = images.length) {
  const grid = $("#imageGrid");
  grid.classList.remove("is-loading");
  grid.replaceChildren(...images.map(pictureCard));
  $("#imageCount").textContent = total ? `(${total})` : "";
  $("#imagesEmpty").hidden = images.length !== 0;
}

function updateNotice() {
  const notice = $("#notice");
  const insideFolder = Boolean(currentTag || currentSource);
  if (!insideFolder && !currentQuery) {
    notice.hidden = true;
    $("#imagesTab").title = "";
    return;
  }
  const context = [];
  if (insideFolder) context.push(`Folder: ${currentFolderName || currentTag}`);
  if (currentQuery) context.push(`Search: “${currentQuery}”`);
  const returnHint = insideFolder
    ? "Click Images to return to your full library."
    : "Clear the search box to return to all images.";
  const text = document.createElement("span");
  text.textContent = `${context.join(" · ")} — ${returnHint}`;
  notice.replaceChildren(text);
  if (insideFolder) {
    const canvas = document.createElement("button");
    canvas.type = "button";
    canvas.className = "open-canvas";
    canvas.textContent = "Canvas";
    canvas.onclick = openFolderCanvas;
    notice.append(canvas);
  }
  notice.hidden = false;
  $("#imagesTab").title = "Return to all images";
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
  $("#searchQuery").value = "";
  loadImages();
}

async function loadImages() {
  const loadId = ++imageLoadId;
  switchTab("images");
  const preserveResults = Boolean(currentQuery && $("#imageGrid .image-card"));
  if (!preserveResults) setLoading();
  else showMessage(`Searching for “${currentQuery}”…`, false, true);
  updateNotice();
  try {
    if (currentQuery) {
      const data = appState?.aiAvailable
        ? await semanticSearch(currentQuery)
        : await request(`/api/app/search?q=${encodeURIComponent(currentQuery)}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}&limit=120`);
      if (loadId !== imageLoadId) return;
      renderImages(data.images, data.images.length);
      if (data.preparing) {
        $("#imagesEmpty").hidden = true;
      } else if (!data.images.length) showMessage("No matching pictures. Try fewer words.");
      else if (!semanticIndexPromise && !semanticWarmupPromise) $("#message").hidden = true;
    } else {
      const mode = currentTag || currentSource ? "recent" : "random";
      const data = await request(`/api/app/images?mode=${mode}&count=300&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}`);
      if (loadId !== imageLoadId) return;
      renderImages(data.images, data.total);
    }
  } catch (error) {
    if (loadId !== imageLoadId) return;
    if (!preserveResults) renderImages([]);
    showMessage(error.message, true, true);
  }
}

function folderCard(folder) {
  const card = document.createElement("button");
  card.className = "folder-card";
  card.type = "button";
  const depth = Number(folder.depth) || 0;
  card.dataset.depth = String(depth);
  card.style.setProperty("--folder-depth", depth);
  card.title = folder.kind === "source" ? folder.value : folder.name;
  const preview = document.createElement("span");
  preview.className = `folder-preview images-${Math.min(4, folder.images.length)}`;
  folder.images.forEach(item => {
    const image = document.createElement("img");
    image.src = item.url;
    image.alt = "";
    image.loading = "lazy";
    bindImageContextMenu(image, item);
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
  count.textContent = `${folder.count} ${folder.count === 1 ? "picture" : "pictures"}`;
  const details = document.createElement("span");
  details.className = "folder-details";
  details.append(name, count);
  card.append(preview, details);
  card.onclick = () => {
    currentTag = folder.kind === "tag" ? folder.value : "";
    currentSource = folder.kind === "source" ? folder.value : "";
    currentFolderName = folder.kind === "source" ? folder.value : folder.name;
    currentQuery = "";
    $("#searchQuery").value = "";
    loadImages();
  };
  return card;
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
  name.textContent = "Create folder";
  details.append(name);
  preview.append(create);
  card.append(preview, details);
  card.onclick = openCreateFolder;
  return card;
}

async function loadFolders() {
  const list = $("#folderList");
  try {
    const data = await request("/api/app/folders");
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
  image.src = `/thumbnail/${encodeURIComponent(item.id)}`;
  image.alt = item.name;
  image.loading = "lazy";
  image.draggable = false;
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
  $("#canvasStatus").textContent = "Saving…";
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
    $("#canvasStatus").textContent = "Saved";
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
  $("#canvasStatus").textContent = "Loading…";
  dialog.showModal();
  try {
    const data = await request(`/api/app/canvas?${canvasQuery()}`);
    canvasImages = data.images;
    for (const [id, point] of Object.entries(data.positions || {})) canvasPositions.set(Number(id), point);
    canvasZoom = 1;
    const viewport = $("#canvasViewport");
    canvasPan = {x: viewport.clientWidth / 2, y: viewport.clientHeight / 2};
    renderCanvas();
    applyCanvasTransform();
    $("#canvasStatus").textContent = `${canvasImages.length} ${canvasImages.length === 1 ? "picture" : "pictures"}`;
  } catch (error) {
    $("#canvasStatus").textContent = error.message;
  }
}

function renderState() {
  if (!appState) return;
  const stats = appState.index;
  $("#indexSummary").textContent = stats ? `${stats.count} pictures in your library` : "No pictures yet";
  $("#boardCount").textContent = appState.boards ? `(${appState.boards})` : "";

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
}

async function refreshState() {
  try {
    const previous = lastJobState;
    appState = await request("/api/app/state");
    lastJobState = appState.indexJob.state;
    renderState();
    if (lastJobState === "running") {
      showMessage(appState.indexJob.message, false, true);
      clearTimeout(pollTimer);
      pollTimer = setTimeout(refreshState, 800);
    } else if (previous === "running") {
      $("#message").hidden = true;
      if (lastJobState === "complete") {
        showMessage(appState.indexJob.message);
        await loadImages();
        await loadFolders();
      } else {
        showMessage(appState.indexJob.message, true, true);
      }
    }
  } catch (error) {
    showMessage(error.message, true, true);
  }
}

async function startIndex(payload) {
  try {
    semanticResults.clear();
    const data = await request("/api/app/index", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload),
    });
    showMessage("Indexing started…", false, true);
    lastJobState = "running";
    await refreshState();
    return data;
  } catch (error) {
    showMessage(error.message, true, true);
    throw error;
  }
}

function isSupportedImage(file) {
  return /\.(jpe?g|png|webp|gif)$/i.test(file.name);
}

async function uploadFiles(files) {
  const images = Array.from(files).filter(isSupportedImage);
  if (!images.length) return showMessage("Choose at least one JPG, PNG, WebP, or GIF image.", true);
  openMenu();
  showMessage(`Copying 0 of ${images.length} pictures…`, false, true);
  let saved = 0;
  let skipped = 0;
  for (let index = 0; index < images.length; index++) {
    const file = images[index];
    showMessage(`Copying ${index + 1} of ${images.length}: ${file.name}`, false, true);
    try {
      await request(`/api/app/upload?name=${encodeURIComponent(file.name)}`, {
        method: "POST",
        headers: {"Content-Type": file.type || "application/octet-stream"},
        body: file,
      });
      saved++;
    } catch (_) {
      skipped++;
    }
  }
  if (saved) semanticResults.clear();
  await refreshState();
  await loadImages();
  await loadFolders();
  if (!saved) showMessage("No pictures could be added.", true);
  else if (skipped) showMessage(`Added ${saved} pictures; skipped ${skipped} unreadable files.`);
  else showMessage(`Added ${saved} ${saved === 1 ? "picture" : "pictures"}.`);
}

function openCreateFolder() {
  $("#newFolderName").value = "";
  $("#newFolderPrompt").value = "";
  $("#newFolderFiles").value = "";
  $("#folderDialog").showModal();
  $("#newFolderName").focus();
}

async function createFolder(event) {
  event.preventDefault();
  const name = $("#newFolderName").value.trim();
  const prompt = $("#newFolderPrompt").value.trim();
  const files = Array.from($("#newFolderFiles").files || []).filter(isSupportedImage);
  if (!name) return;
  try {
    await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "create", tag: name}),
    });
    let saved = 0;
    let skipped = 0;
    for (let index = 0; index < files.length; index++) {
      const file = files[index];
      showMessage(`Adding ${index + 1} of ${files.length}: ${file.name}`, false, true);
      try {
        await request(`/api/app/upload?name=${encodeURIComponent(file.name)}&folder=${encodeURIComponent(name)}`, {
          method: "POST",
          headers: {"Content-Type": file.type || "application/octet-stream"},
          body: file,
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
      showMessage(`Finding 50 pictures for “${prompt}”…`, false, true);
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
    if (prompt && indexed === 0) showMessage(`Created ${name}, but search indexing has not reached any pictures yet. Try filling it again shortly.`, true, true);
    else if (prompt && indexed < total) showMessage(`Created ${name} with ${filled + saved} pictures from ${indexed} indexed; search is still improving in the background.`);
    else if (prompt) showMessage(`Created ${name} with ${filled + saved} pictures for “${prompt}”.`);
    else if (skipped) showMessage(`Created ${name} with ${saved} pictures; skipped ${skipped} unreadable files.`);
    else if (saved) showMessage(`Created ${name} with ${saved} ${saved === 1 ? "picture" : "pictures"}.`);
    else showMessage(`Created folder: ${name}`);
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

async function saveTag(event) {
  event.preventDefault();
  const tag = $("#tagName").value.trim();
  if (!tag) return;
  try {
    const imageId = Number($("#tagImageId").value);
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
    showMessage(`Added tag: ${tag}`);
    await refreshState();
    await loadImages();
    await loadFolders();
  } catch (error) {
    showMessage(error.message, true);
  }
}

async function loadBoards() {
  $("#addSection").hidden = true;
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
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = false;
  $("#currentVersion").textContent = appState?.version ? `v${appState.version}` : "Unknown";
  $("#updateMethod").textContent = appState?.updateMethod || "GitHub Releases";
  openMenu();
}

async function checkForUpdates() {
  const button = $("#checkForUpdates");
  const status = $("#updateStatus");
  button.disabled = true;
  button.textContent = "Checking…";
  status.className = "";
  status.textContent = "Checking GitHub Releases…";
  $("#installUpdate").hidden = true;
  $("#downloadUpdate").hidden = true;
  try {
    const update = await request("/api/app/update");
    if (!update.available) {
      status.className = "success";
      status.textContent = "Pictogrep is up to date ✓";
      button.textContent = "Check again";
      return;
    }
    status.textContent = `Pictogrep v${update.latestVersion} is available. ${update.hint || ""}`.trim();
    button.hidden = true;
    if (update.action === "replace") {
      const install = $("#installUpdate");
      install.hidden = false;
      install.className = "primary";
      install.textContent = `Update to v${update.latestVersion}`;
    } else {
      const download = $("#downloadUpdate");
      download.hidden = false;
      download.href = update.url;
      if (update.action === "download") download.textContent = "Download installer";
      else if (update.action === "managed") download.textContent = "View release notes";
      else download.textContent = "View newest release";
    }
  } catch (error) {
    status.className = "error";
    status.textContent = error.message;
    button.textContent = "Try again";
  } finally {
    button.disabled = false;
  }
}

async function installAvailableUpdate() {
  const button = $("#installUpdate");
  const status = $("#updateStatus");
  button.disabled = true;
  button.textContent = "Updating…";
  status.className = "";
  status.textContent = "Downloading and installing the update… Keep Pictogrep open.";
  try {
    const result = await request("/api/app/update", {
      method: "POST",
      headers: {"X-Pictogrep-Action": "install-update"},
    });
    if (!result.updated) {
      status.className = "success";
      status.textContent = "Pictogrep is already up to date ✓";
    } else {
      status.className = "success";
      status.textContent = `Pictogrep v${result.version} is installed. Restart the app to use it.`;
    }
    button.hidden = true;
  } catch (error) {
    status.className = "error";
    status.textContent = error.message;
    button.disabled = false;
    button.textContent = "Try update again";
  }
}

$("#menuButton").onclick = openMenu;
$("#closeMenu").onclick = closeMenu;
$("#drawerScrim").onclick = closeMenu;
$("#imagesTab").onclick = showAllImages;
$("#foldersTab").onclick = () => { switchTab("folders"); loadFolders(); };
$("#showBoards").onclick = loadBoards;
$("#showAbout").onclick = showAbout;
$("#checkForUpdates").onclick = checkForUpdates;
$("#installUpdate").onclick = installAvailableUpdate;
$("#showAdd").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#addSection").hidden = !$("#addSection").hidden;
};
$("#emptyAddImages").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#aboutSection").hidden = true;
  $("#addSection").hidden = false;
  openMenu();
};
$("#imageFiles").onchange = event => uploadFiles(event.target.files);
$("#imageFolder").onchange = event => uploadFiles(event.target.files);
$("#searchForm").onsubmit = event => {
  event.preventDefault();
  clearTimeout(queryPrimeTimer);
  currentQuery = $("#searchQuery").value.trim();
  loadImages();
};
$("#searchQuery").addEventListener("input", event => {
  clearTimeout(queryPrimeTimer);
  const query = event.target.value.trim();
  if (!query) {
    if (currentQuery) {
      currentQuery = "";
      currentTag = "";
      currentSource = "";
      currentFolderName = "";
      loadImages();
    }
    return;
  }
  if (query.length < 2) return;
  queryPrimeTimer = setTimeout(() => {
    if ($("#searchQuery").value.trim() !== query || currentQuery === query) return;
    currentQuery = query;
    loadImages();
  }, 280);
});
$("#searchQuery").addEventListener("search", () => {
  if (!$("#searchQuery").value) {
    currentQuery = "";
    currentTag = "";
    currentSource = "";
    currentFolderName = "";
    loadImages();
  }
});
$("#tagForm").onsubmit = saveTag;
$("#cancelTag").onclick = () => $("#tagDialog").close();
$("#createFolderForm").onsubmit = createFolder;
$("#cancelFolder").onclick = () => $("#folderDialog").close();
$("#closeViewer").onclick = () => $("#imageViewer").close();
$("#imageViewer").onclick = event => {
  if (event.target === $("#imageViewer")) $("#imageViewer").close();
};
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
  if (event.key === "Escape") { closeMenu(); closeCardMenus(); }
  if (event.key === "/" && !["INPUT", "TEXTAREA"].includes(document.activeElement.tagName)) {
    event.preventDefault();
    $("#searchQuery").focus();
  }
});
document.addEventListener("click", closeCardMenus);

async function start() {
  setLoading();
  await refreshState();
  await loadImages();
  await loadFolders();
  if (appState?.index?.count) setTimeout(warmTextSearch, 700);
}

start();
