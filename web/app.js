const $ = selector => document.querySelector(selector);

let appState = null;
let currentTag = "";
let currentSource = "";
let currentFolderName = "";
let currentQuery = "";
let messageTimer = null;
let pollTimer = null;
let lastJobState = "idle";

async function request(url, options = {}) {
  const response = await fetch(url, options);
  const data = await response.json();
  if (!response.ok || data.ok === false) throw new Error(data.error || `Request failed (${response.status})`);
  return data;
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
  card.oncontextmenu = event => {
    event.preventDefault();
    event.stopPropagation();
    closeCardMenus();
    menu.classList.add("cursor-menu");
    menu.hidden = false;
    menuButton.setAttribute("aria-expanded", "true");
    const margin = 8;
    const left = Math.max(margin, Math.min(event.clientX, window.innerWidth - menu.offsetWidth - margin));
    const top = Math.max(margin, Math.min(event.clientY, window.innerHeight - menu.offsetHeight - margin));
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
  };
  info.append(menuButton, menu);
  return card;
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
  const image = $("#viewerImage");
  image.src = item.url;
  image.alt = item.name;
  viewer.showModal();
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
  const parts = [];
  if (currentQuery) parts.push(`Search: “${currentQuery}”`);
  if (currentTag || currentSource) parts.push(`Folder: ${currentFolderName || currentTag}`);
  if (!parts.length) {
    notice.hidden = true;
    return;
  }
  notice.textContent = parts.join(" · ") + " — Clear the search box to return to all images.";
  notice.hidden = false;
}

async function loadImages() {
  switchTab("images");
  setLoading();
  updateNotice();
  try {
    if (currentQuery) {
      const url = `/api/app/search?q=${encodeURIComponent(currentQuery)}&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}&limit=120`;
      const data = await request(url);
      renderImages(data.images, data.images.length);
      if (!data.images.length) showMessage("No matching pictures. Try fewer words.");
    } else {
      const data = await request(`/api/app/images?mode=recent&count=300&tag=${encodeURIComponent(currentTag)}&source=${encodeURIComponent(currentSource)}`);
      renderImages(data.images, data.total);
    }
  } catch (error) {
    renderImages([]);
    showMessage(error.message, true, true);
  }
}

function folderCard(folder) {
  const card = document.createElement("button");
  card.className = "folder-card";
  card.type = "button";
  const preview = document.createElement("span");
  preview.className = `folder-preview images-${Math.min(4, folder.images.length)}`;
  folder.images.forEach(item => {
    const image = document.createElement("img");
    image.src = item.url;
    image.alt = "";
    image.loading = "lazy";
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
  card.append(preview, name, count);
  card.onclick = () => {
    currentTag = folder.kind === "tag" ? folder.value : "";
    currentSource = folder.kind === "source" ? folder.value : "";
    currentFolderName = folder.name;
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
  const preview = document.createElement("span");
  preview.className = "folder-preview";
  const create = document.createElement("span");
  create.className = "folder-create-button";
  create.textContent = "Create folder";
  preview.append(create);
  card.append(preview);
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

function renderState() {
  if (!appState) return;
  const stats = appState.index;
  $("#indexSummary").textContent = stats ? `${stats.count} indexed images` : "No index yet";
  $("#modelName").textContent = appState.model;
  $("#boardsPath").textContent = appState.paths.boards;
  $("#boardCount").textContent = appState.boards ? `(${appState.boards})` : "";

  const options = $("#tagOptions");
  options.replaceChildren(...appState.tags.map(tag => {
    const option = document.createElement("option");
    option.value = tag.name;
    return option;
  }));
  const job = appState.indexJob;
  const active = job.state === "running";
  $("#indexStatus").hidden = !active && job.state !== "error";
  $("#indexMessage").textContent = job.message || "";
  $("#indexProgress").max = job.total || 1;
  $("#indexProgress").value = job.current || 0;
  $("#rebuildIndex").disabled = active || !(appState.sources || []).length;
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

async function uploadFiles(files) {
  const images = Array.from(files).filter(file => file.type.startsWith("image/") || /\.(jpe?g|png|webp)$/i.test(file.name));
  if (!images.length) return showMessage("Choose at least one JPG, PNG, or WebP image.", true);
  openMenu();
  showMessage(`Copying 0 of ${images.length} pictures…`, false, true);
  try {
    for (let index = 0; index < images.length; index++) {
      const file = images[index];
      showMessage(`Copying ${index + 1} of ${images.length}: ${file.name}`, false, true);
      await request(`/api/app/upload?name=${encodeURIComponent(file.name)}`, {
        method: "POST",
        headers: {"Content-Type": file.type || "application/octet-stream"},
        body: file,
      });
    }
    await startIndex({includeLibrary: true});
  } catch (_) {}
}

function openCreateFolder() {
  $("#newFolderName").value = "";
  $("#folderPrompt").value = "";
  $("#newFolderFiles").value = "";
  $("#folderDialog").showModal();
  $("#newFolderName").focus();
}

async function createFolder(event) {
  event.preventDefault();
  const name = $("#newFolderName").value.trim();
  const prompt = $("#folderPrompt").value.trim();
  const files = Array.from($("#newFolderFiles").files || []).filter(file => file.type.startsWith("image/"));
  if (!name) return;
  try {
    await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "create", tag: name}),
    });
    let aiAdded = 0;
    if (prompt) {
      showMessage(`Finding pictures for “${prompt}” with local AI…`, false, true);
      const result = await request("/api/app/tags", {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({action: "fill", tag: name, prompt, limit: 50}),
      });
      aiAdded = result.added;
    }
    for (let index = 0; index < files.length; index++) {
      const file = files[index];
      showMessage(`Adding ${index + 1} of ${files.length}: ${file.name}`, false, true);
      await request(`/api/app/upload?name=${encodeURIComponent(file.name)}&folder=${encodeURIComponent(name)}`, {
        method: "POST",
        headers: {"Content-Type": file.type || "application/octet-stream"},
        body: file,
      });
    }
    $("#folderDialog").close();
    await refreshState();
    await loadFolders();
    if (files.length) await startIndex({includeLibrary: true});
    else showMessage(aiAdded ? `Created ${name} with ${aiAdded} AI matches.` : `Created folder: ${name}`);
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
    await request("/api/app/tags", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({action: "add", tag, imageId: Number($("#tagImageId").value)}),
    });
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

$("#menuButton").onclick = openMenu;
$("#closeMenu").onclick = closeMenu;
$("#drawerScrim").onclick = closeMenu;
$("#imagesTab").onclick = () => switchTab("images");
$("#foldersTab").onclick = () => { switchTab("folders"); loadFolders(); };
$("#showBoards").onclick = loadBoards;
$("#showAdd").onclick = () => {
  $("#boardsSection").hidden = true;
  $("#addSection").hidden = !$("#addSection").hidden;
};
$("#emptyAddImages").onclick = () => { openMenu(); $("#addSection").hidden = false; };
$("#imageFiles").onchange = event => uploadFiles(event.target.files);
$("#imageFolder").onchange = event => uploadFiles(event.target.files);
$("#folderForm").onsubmit = async event => {
  event.preventDefault();
  const folder = $("#folderPath").value.trim();
  if (!folder) return;
  try {
    await startIndex({folder});
    $("#folderPath").value = "";
  } catch (_) {}
};
$("#rebuildIndex").onclick = () => startIndex({});
$("#searchForm").onsubmit = event => {
  event.preventDefault();
  currentQuery = $("#searchQuery").value.trim();
  loadImages();
};
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
}

start();
