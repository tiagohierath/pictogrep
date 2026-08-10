import argparse
import base64
import json
import mimetypes
from pathlib import Path
import random
import re
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, quote, unquote, urlparse
import webbrowser

from pictogrep_core import (
    BASE,
    METADATA_PATH,
    collection_images,
    collection_names,
    image_files,
    search as clip_search,
    find_milklily_project,
    tags_for_image,
)


DEFAULT_PORT = 8765


def clean_name(value):
    value = Path(value).stem.lower()
    value = re.sub(r"[^a-z0-9]+", "-", value).strip("-")
    return value[:60] or "image"


def clean_reference_name(value, mime=""):
    suffix = Path(value).suffix.lower()
    allowed = {".jpg", ".jpeg", ".png", ".webp", ".gif"}
    if suffix not in allowed:
        suffix = mimetypes.guess_extension(mime) or ".png"
    return clean_name(value) + suffix


def read_index_paths():
    if not METADATA_PATH.exists():
        return []
    with METADATA_PATH.open() as fh:
        return [Path(p) for p in json.load(fh)]


def collect_images(folder=None):
    if folder:
        paths = image_files(folder)
    else:
        paths = [p for p in read_index_paths() if p.exists()]
        if not paths:
            paths = image_files(BASE / "images")
    paths = [p.resolve() for p in paths if p.exists()]
    paths.sort(key=lambda p: p.stat().st_mtime, reverse=True)
    return paths


def image_payload(paths, mode, count, record=None):
    record = record or image_record
    selected = list(enumerate(paths))
    if mode == "all":
        random.shuffle(selected)
    elif mode == "recent":
        selected = selected[:count]
    return [
        record(i, p)
        for i, p in selected
    ]


def image_record(image_id, path):
    return {
        "id": image_id,
        "name": path.name,
        "mtime": int(path.stat().st_mtime),
        "url": f"/image/{image_id}",
    }


def page_html():
    return r"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Pictogrep Storyboard</title>
  <style>
    :root { color-scheme: light; --bg:#f5f5f2; --paper:#fff; --fg:#171717; --muted:#666; --line:#c9c9c3; --soft:#eeeeea; }
    * { box-sizing: border-box; }
    html, body { height: 100%; overflow: hidden; }
    body { min-height: 100vh; margin: 0; display: flex; flex-direction: column; font: 15px/1.35 serif; background: var(--bg); color: var(--fg); }
    header { display: flex; gap: .55rem; align-items: center; padding: .55rem .75rem; border-bottom: 1px solid var(--line); background: var(--paper); white-space: nowrap; overflow-x: auto; overflow-y: hidden; }
    .title { flex: 0 0 auto; min-width: 0; }
    .title strong { display: inline; letter-spacing: .02em; }
    .title span { display: none; }
    .controls { display: flex; gap: .35rem; align-items: center; flex-wrap: nowrap; min-width: 0; }
    .controls.primary { flex: 1 1 auto; }
    .controls.secondary { flex: 0 0 auto; }
    button, select, input { font: inherit; border: 1px solid var(--line); background: var(--paper); color: var(--fg); padding: .42rem .55rem; }
    button { cursor: pointer; }
    button:hover { background: var(--soft); }
    button.active { background: #171717; border-color: #171717; color: #fff; }
    input[type=number] { width: 5.5rem; }
    .search-input { width: min(12rem, 18vw); min-width: 8rem; }
    input[type=range] { width: 7rem; vertical-align: middle; }
    main { flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(220px, var(--left-pane, 50%)) 18px minmax(260px, 1fr); gap: 0; padding: 1rem; touch-action: none; }
    .pane { min-width: 0; min-height: 0; display: flex; flex-direction: column; gap: .6rem; }
    .pane.left-pane { padding-right: .7rem; }
    .pane.right-pane { padding-left: .7rem; }
    .divider { position: relative; cursor: col-resize; touch-action: none; background: transparent; }
    .divider::before { content: ""; position: absolute; top: 0; bottom: 0; left: 7px; width: 4px; border-left: 1px solid #aaa696; border-right: 1px solid #fff; background: #d7d3c3; }
    .divider::after { content: ""; position: absolute; top: 50%; left: 4px; width: 10px; height: 42px; transform: translateY(-50%); border: 1px solid #aaa696; background: repeating-linear-gradient(90deg, #c6c1b0 0 1px, #ece8da 1px 3px); }
    .divider:hover::before, .divider.dragging::before { background: #bdb7a6; }
    .divider:hover::after, .divider.dragging::after { background: repeating-linear-gradient(90deg, #9f9987 0 1px, #d5d0c0 1px 3px); }
    .divider button { position: absolute; top: 50%; left: 50%; z-index: 3; width: 1.35rem; height: 1.35rem; transform: translate(-50%, -50%); padding: 0; line-height: 1; border-color: #aaa696; background: #ece8da; cursor: pointer; }
    .divider button:hover { background: #fff; }
    .label { display: flex; justify-content: space-between; gap: 1rem; align-items: baseline; color: var(--muted); font-size: .9rem; }
    .label strong { color: var(--fg); font-size: 1rem; font-weight: 650; }
    .label-title { display: inline-flex; gap: .45rem; align-items: center; min-width: 0; }
    .label-actions { display: inline-flex; gap: .25rem; align-items: center; min-width: 0; }
    .label-actions button { padding: .12rem .38rem; min-width: 1.8rem; }
    .label button { padding: .12rem .42rem; min-width: 0; }
    #refName { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .frame { flex: 1; min-height: 0; display: grid; place-items: center; background: var(--paper); border: 1px solid var(--line); overflow: hidden; padding: .5rem; }
    .board-frame { position: relative; background: #474744; touch-action: none; user-select: none; -webkit-user-select: none; }
    .board-refs { position: absolute; inset: 0; z-index: 10; pointer-events: none; overflow: hidden; }
    .board-ref { position: absolute; width: 150px; min-width: 70px; max-width: 420px; border: 2px solid rgba(255,255,255,.82); background: #222; box-shadow: 0 3px 12px rgba(0,0,0,.38); pointer-events: auto; touch-action: none; cursor: move; }
    .board-ref img { display: block; width: 100%; height: auto; max-height: 280px; object-fit: contain; pointer-events: none; }
    .board-ref button { position: absolute; top: -10px; right: -10px; width: 22px; height: 22px; padding: 0; border-radius: 50%; line-height: 18px; background: #fff; }
    .board-ref .ref-resize { position: absolute; right: -7px; bottom: -7px; width: 16px; height: 16px; border: 2px solid #fff; background: #333; cursor: nwse-resize; }
    #reference { width: var(--ref-size, 100%); height: var(--ref-size, 100%); max-width: none; max-height: none; object-fit: contain; -webkit-user-drag: none; user-select: none; }
    #reference.mirrored, .trace-ref.mirrored { transform: scaleX(-1); }
    .canvas-stack { position: relative; background: #e0dedb; border: 1px solid #a7a7a0; user-select: none; -webkit-user-select: none; }
    .canvas-stack canvas, .trace-ref { position: absolute; inset: 0; width: 100%; height: 100%; }
    #paper { z-index: 0; pointer-events: none; }
    .trace-ref { z-index: 1; object-fit: contain; opacity: .46; filter: grayscale(1) contrast(.92) brightness(1.1); pointer-events: none; }
    #guide { z-index: 6; pointer-events: none; }
    #fill { z-index: 3; pointer-events: none; }
    #lasso { z-index: 4; pointer-events: none; }
    #board { z-index: 5; touch-action: none; cursor: crosshair; user-select: none; -webkit-user-select: none; -webkit-user-drag: none; }
    .bar { display: grid; grid-template-columns: 1fr auto; gap: .7rem; align-items: center; }
    progress { width: 100%; height: 1rem; border: 1px solid var(--line); background: var(--paper); }
    progress::-webkit-progress-bar { background: var(--paper); }
    progress::-webkit-progress-value { background: var(--fg); }
    progress::-moz-progress-bar { background: var(--fg); }
    .tools { display: flex; gap: .5rem; align-items: center; flex-wrap: wrap; }
    .status { color: var(--muted); min-height: 1.3rem; }
    .header-status { display: none; }
    .save-state { color: var(--fg); }
    .info { position: relative; flex: 0 0 auto; }
    .info summary { display: grid; place-items: center; width: 2rem; height: 2rem; border: 1px solid var(--line); background: var(--paper); cursor: help; list-style: none; }
    .info summary:hover { background: var(--soft); }
    .info summary::-webkit-details-marker { display: none; }
    .info-panel { position: fixed; top: 3.1rem; right: .75rem; z-index: 30; width: min(34rem, calc(100vw - 1.5rem)); padding: .75rem .85rem; border: 1px solid var(--line); background: var(--paper); box-shadow: 0 4px 14px rgba(0,0,0,.14); color: var(--muted); white-space: normal; }
    .info-row { display: grid; grid-template-columns: 5rem 1fr; gap: .75rem; margin: .25rem 0; }
    .info-row strong { color: var(--fg); font-weight: 650; }
    .empty { padding: 2rem; text-align: center; color: var(--muted); }
    [hidden] { display: none !important; }
    @media (max-width: 850px) {
      .search-input { width: 9rem; }
      main { grid-template-columns: 1fr; min-height: auto; }
      .pane.left-pane, .pane.right-pane { padding: 0; }
      .divider { display: none; }
      .frame { min-height: 42vh; }
    }
  </style>
</head>
<body>
  <header>
    <div class="title">
      <strong>PICTOGREP STORYBOARD</strong>
      <span>rough redraws, one reference at a time</span>
    </div>
    <div class="controls primary">
      <input id="clipSearch" class="search-input" type="search" placeholder="Search">
      <select id="mode">
        <option value="recent" data-count="30">30 most recent</option>
        <option value="recent" data-count="100">100 most recent</option>
        <option value="all" selected>All images</option>
        <option value="recent" data-custom="1">Custom recent</option>
      </select>
      <select id="collection" title="Reference tag">
        <option value="">All tags</option>
      </select>
      <input id="count" type="number" min="1" value="30" hidden>
      <select id="aspect">
        <option value="16:9">16:9</option>
        <option value="4:3" selected>4:3</option>
        <option value="2:1">Pan H 2:1</option>
        <option value="3:4">Pan V 3:4</option>
      </select>
      <button id="start">Load</button>
      <span class="status header-status" id="status"></span>
    </div>
    <div class="controls secondary">
      <button id="prev">Prev</button>
      <button id="next">Save + Next</button>
      <button id="skip">Skip</button>
      <details class="info" id="infoMenu">
        <summary title="Info">i</summary>
        <div class="info-panel">
          <div class="info-row"><strong>Keys</strong><span>1/P pen · 2/E eraser · 3/S colour lasso · 4/T trace · Q/W pen size · Ctrl+Enter save + next · Ctrl+Z undo</span></div>
          <div class="info-row"><strong>Status</strong><span id="infoStatus">Loading</span></div>
          <div class="info-row"><strong>Saving</strong><span id="infoSavePath"></span></div>
        </div>
      </details>
    </div>
  </header>

  <main>
    <section class="pane left-pane">
      <div class="label">
        <strong>Reference</strong>
        <span class="label-actions">
          <span id="refName"></span>
          <button id="refSmaller" title="Make reference image smaller">-</button>
          <button id="refBigger" title="Make reference image bigger">+</button>
          <button id="refMirror" title="Mirror reference horizontally">Mirror</button>
        </span>
      </div>
      <div class="frame">
        <img id="reference" alt="">
        <div id="empty" class="empty" hidden>No images found. Run pictogrep index /path/to/images or pass a folder to storyboard.</div>
      </div>
      <div class="bar"><progress id="progress" value="0" max="1"></progress><span id="counter">0/0</span></div>
    </section>
    <div id="divider" class="divider" title="Drag to resize"><button id="viewReset" title="Reset view">=</button></div>
    <section class="pane right-pane">
      <div class="label">
        <span class="label-title"><strong>Board</strong><button id="trace" title="Show reference under the board">Trace</button></span>
        <span class="label-actions"><span id="saveState" class="save-state">Not saved yet</span><button id="boardSmaller" title="Make drawing board smaller">-</button><button id="boardBigger" title="Make drawing board bigger">+</button></span>
      </div>
      <div id="boardFrame" class="frame board-frame">
        <div id="boardRefs" class="board-refs"></div>
        <div id="canvasStack" class="canvas-stack">
          <canvas id="paper"></canvas>
          <img id="traceRef" class="trace-ref" alt="" hidden>
          <canvas id="guide"></canvas>
          <canvas id="fill"></canvas>
          <canvas id="lasso"></canvas>
          <canvas id="board"></canvas>
        </div>
      </div>
      <div class="tools">
        <button id="pen" class="active">Pen</button>
        <button id="eraser">Eraser</button>
        <button id="shade" title="Draw a closed shape for a colored-pencil grain">Colour lasso</button>
        <select id="shadeColour" title="Shade colour">
          <option value="#698ca3" selected>Muted blue</option>
          <option value="#000000">Grey</option>
          <option value="#aa7182">Muted pink</option>
          <option value="#b39654">Muted yellow</option>
          <option value="#719983">Muted green</option>
          <option value="#947ca3">Muted lilac</option>
        </select>
        <label>Size <input id="brush" type="range" min="1" max="22" value="4"> <span id="brushValue">4</span></label>
        <button id="undo">Undo</button>
        <button id="clear">Clear</button>
        <button id="save">Save now</button>
        <button id="addRefs" title="Add up to five movable reference images">Add refs</button>
        <input id="refFiles" type="file" accept="image/*" multiple hidden>
      </div>
      <div class="status">The pen supports tablet pressure. Drawings autosave while the board is idle.</div>
    </section>
  </main>

  <script>
    const img = document.getElementById('reference');
    const empty = document.getElementById('empty');
    const workArea = document.querySelector('main');
    const canvasStack = document.getElementById('canvasStack');
    const traceRef = document.getElementById('traceRef');
    const boardFrame = document.getElementById('boardFrame');
    const boardRefs = document.getElementById('boardRefs');
    const addRefs = document.getElementById('addRefs');
    const refFiles = document.getElementById('refFiles');
    const paper = document.getElementById('paper');
    const paperCtx = paper.getContext('2d');
    const guide = document.getElementById('guide');
    const guideCtx = guide.getContext('2d');
    const fill = document.getElementById('fill');
    const fillCtx = fill.getContext('2d');
    const lasso = document.getElementById('lasso');
    const lassoCtx = lasso.getContext('2d');
    const canvas = document.getElementById('board');
    const ctx = canvas.getContext('2d');
    const clipSearch = document.getElementById('clipSearch');
    const mode = document.getElementById('mode');
    const collection = document.getElementById('collection');
    const count = document.getElementById('count');
    const aspect = document.getElementById('aspect');
    const statusEl = document.getElementById('status');
    const infoStatus = document.getElementById('infoStatus');
    const infoSavePath = document.getElementById('infoSavePath');
    const saveState = document.getElementById('saveState');
    const refName = document.getElementById('refName');
    const progress = document.getElementById('progress');
    const counter = document.getElementById('counter');
    const brush = document.getElementById('brush');
    const brushValue = document.getElementById('brushValue');
    const penBtn = document.getElementById('pen');
    const eraserBtn = document.getElementById('eraser');
    const shadeBtn = document.getElementById('shade');
    const shadeColour = document.getElementById('shadeColour');
    const traceBtn = document.getElementById('trace');
    const divider = document.getElementById('divider');
    const viewReset = document.getElementById('viewReset');
    const refSmaller = document.getElementById('refSmaller');
    const refBigger = document.getElementById('refBigger');
    const refMirror = document.getElementById('refMirror');
    const boardSmaller = document.getElementById('boardSmaller');
    const boardBigger = document.getElementById('boardBigger');
    let images = [];
    let index = 0;
    let drawing = false;
    let dirty = false;
    let undoStack = [];
    let saveTimer = null;
    let saveIdleHandle = null;
    let saving = false;
    let saveAgain = false;
    let drawingPresent = false;
    let changeRevision = 0;
    let boardReferences = [];
    let tool = 'pen';
    let trace = localStorage.getItem('pictogrepStoryTrace') === '1';
    let refScale = Number(localStorage.getItem('pictogrepStoryRefScale') || 1);
    if (!Number.isFinite(refScale)) refScale = 1;
    let refMirrored = localStorage.getItem('pictogrepStoryRefMirror') === '1';
    let boardScale = Number(localStorage.getItem('pictogrepStoryBoardScale') || 1);
    if (!Number.isFinite(boardScale)) boardScale = 1;
    let lastPoint = null;
    let lassoPoints = [];
    let resizing = false;
    let strokeChanged = false;
    let strokeUndoCaptured = false;

    function status(text) {
      statusEl.textContent = text;
      infoStatus.textContent = text || '';
    }

    function cancelScheduledSave() {
      clearTimeout(saveTimer);
      saveTimer = null;
      if (saveIdleHandle !== null && 'cancelIdleCallback' in window) {
        cancelIdleCallback(saveIdleHandle);
      }
      saveIdleHandle = null;
    }

    function referenceLayoutKey() {
      return 'pictogrepStoryBoardRefs';
    }

    function saveReferenceLayout() {
      localStorage.setItem(referenceLayoutKey(), JSON.stringify(boardReferences.map(ref => ({
        name: ref.name, x: ref.x, y: ref.y, width: ref.width,
      }))));
    }

    function renderBoardReferences() {
      boardRefs.replaceChildren();
      for (const ref of boardReferences) {
        const card = document.createElement('div');
        card.className = 'board-ref';
        card.style.left = ref.x + '%';
        card.style.top = ref.y + '%';
        card.style.width = ref.width + 'px';
        card.dataset.name = ref.name;
        const image = document.createElement('img');
        image.src = ref.url;
        image.alt = ref.name;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.textContent = '×';
        remove.title = 'Remove reference from board';
        const handle = document.createElement('span');
        handle.className = 'ref-resize';
        card.append(image, remove, handle);
        boardRefs.append(card);

        remove.onclick = async e => {
          e.stopPropagation();
          await fetch('/api/references?name=' + encodeURIComponent(ref.name), {method: 'DELETE'});
          boardReferences = boardReferences.filter(item => item.name !== ref.name);
          saveReferenceLayout();
          renderBoardReferences();
        };
        const begin = (e, resize) => {
          if (e.button !== undefined && e.button !== 0) return;
          e.preventDefault();
          e.stopPropagation();
          const startX = e.clientX;
          const startY = e.clientY;
          const startWidth = card.offsetWidth;
          const startLeft = card.offsetLeft;
          const startTop = card.offsetTop;
          card.setPointerCapture(e.pointerId);
          const move = event => {
            if (resize) {
              ref.width = Math.max(70, Math.min(420, startWidth + event.clientX - startX));
              card.style.width = ref.width + 'px';
            } else {
              const maxLeft = Math.max(0, boardFrame.clientWidth - card.offsetWidth);
              const maxTop = Math.max(0, boardFrame.clientHeight - card.offsetHeight);
              const left = Math.max(0, Math.min(maxLeft, startLeft + event.clientX - startX));
              const top = Math.max(0, Math.min(maxTop, startTop + event.clientY - startY));
              ref.x = boardFrame.clientWidth ? left / boardFrame.clientWidth * 100 : 0;
              ref.y = boardFrame.clientHeight ? top / boardFrame.clientHeight * 100 : 0;
              card.style.left = ref.x + '%';
              card.style.top = ref.y + '%';
            }
          };
          const end = () => {
            card.removeEventListener('pointermove', move);
            card.removeEventListener('pointerup', end);
            card.removeEventListener('pointercancel', end);
            saveReferenceLayout();
          };
          card.addEventListener('pointermove', move);
          card.addEventListener('pointerup', end);
          card.addEventListener('pointercancel', end);
        };
        card.addEventListener('pointerdown', e => {
          if (!e.target.closest('button, .ref-resize')) begin(e, false);
        });
        handle.addEventListener('pointerdown', e => begin(e, true));
      }
      addRefs.disabled = boardReferences.length >= 5;
      addRefs.title = boardReferences.length >= 5 ? 'Five-reference limit reached' : 'Add up to five movable reference images';
    }

    async function loadBoardReferences() {
      const res = await fetch('/api/references');
      if (!res.ok) return;
      const data = await res.json();
      let saved = [];
      try { saved = JSON.parse(localStorage.getItem(referenceLayoutKey()) || '[]'); } catch (_) {}
      boardReferences = data.references.slice(0, 5).map((item, i) => {
        const layout = saved.find(savedItem => savedItem.name === item.name) || {};
        return {
          ...item,
          x: Number.isFinite(layout.x) ? layout.x : 2 + i * 4,
          y: Number.isFinite(layout.y) ? layout.y : 2 + i * 4,
          width: Number.isFinite(layout.width) ? layout.width : 150,
        };
      });
      renderBoardReferences();
    }

    function setPaneSplit(clientX) {
      const main = document.querySelector('main');
      const rect = main.getBoundingClientRect();
      const pct = Math.max(24, Math.min(76, ((clientX - rect.left) / rect.width) * 100));
      setPaneSplitPercent(pct);
    }

    function setPaneSplitPercent(pct) {
      const main = document.querySelector('main');
      pct = Math.max(24, Math.min(76, pct));
      main.style.setProperty('--left-pane', pct.toFixed(1) + '%');
      localStorage.setItem('pictogrepStorySplit', pct.toFixed(1));
      requestAnimationFrame(fitCanvasStack);
    }

    function restorePaneSplit() {
      const pct = Number(localStorage.getItem('pictogrepStorySplit') || 50);
      if (Number.isFinite(pct)) {
        setPaneSplitPercent(pct);
      }
    }

    function setReferenceScale(nextScale, showStatus = true) {
      refScale = Math.max(0.45, Math.min(2.4, nextScale));
      img.style.setProperty('--ref-size', Math.round(refScale * 100) + '%');
      localStorage.setItem('pictogrepStoryRefScale', refScale.toFixed(2));
      if (showStatus) status('Reference image ' + Math.round(refScale * 100) + '%');
    }

    function resetView() {
      setPaneSplitPercent(50);
      setReferenceScale(1, false);
      setBoardScale(1, false);
      status('View reset');
    }

    function setBoardScale(nextScale, showStatus = true) {
      boardScale = Math.max(0.45, Math.min(2.4, nextScale));
      localStorage.setItem('pictogrepStoryBoardScale', boardScale.toFixed(2));
      fitCanvasStack();
      if (showStatus) status('Drawing board ' + Math.round(boardScale * 100) + '%');
    }

    function applyReferenceMirror() {
      img.classList.toggle('mirrored', refMirrored);
      traceRef.classList.toggle('mirrored', refMirrored);
      refMirror.classList.toggle('active', refMirrored);
      refMirror.textContent = refMirrored ? 'Mirrored' : 'Mirror';
      refMirror.title = refMirrored ? 'Unmirror reference' : 'Mirror reference horizontally';
    }

    function toggleReferenceMirror() {
      refMirrored = !refMirrored;
      localStorage.setItem('pictogrepStoryRefMirror', refMirrored ? '1' : '0');
      applyReferenceMirror();
      status(refMirrored ? 'Reference mirrored' : 'Reference normal');
    }

    function canvasSize() {
      const sizes = {
        '16:9': [1280, 720],
        '4:3': [1280, 960],
        '2:1': [1440, 720],
        '3:4': [960, 1280],
      };
      return sizes[aspect.value] || sizes['4:3'];
    }

    function resetCanvas() {
      const [w, h] = canvasSize();
      paper.width = w;
      paper.height = h;
      guide.width = w;
      guide.height = h;
      fill.width = w;
      fill.height = h;
      lasso.width = w;
      lasso.height = h;
      canvas.width = w;
      canvas.height = h;
      fitCanvasStack();
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      fillCtx.clearRect(0, 0, fill.width, fill.height);
      lassoCtx.clearRect(0, 0, lasso.width, lasso.height);
      drawPaper();
      drawGuides();
      undoStack = [];
      dirty = false;
      drawingPresent = false;
      changeRevision++;
      saveState.textContent = 'Not saved yet';
    }

    function fitCanvasStack() {
      if (!canvas.width || !canvas.height) return;
      const style = getComputedStyle(boardFrame);
      const padX = parseFloat(style.paddingLeft) + parseFloat(style.paddingRight);
      const padY = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
      const maxW = Math.max(1, boardFrame.clientWidth - padX);
      const maxH = Math.max(1, boardFrame.clientHeight - padY);
      const scale = Math.min(maxW / canvas.width, maxH / canvas.height) * boardScale;
      canvasStack.style.width = Math.floor(canvas.width * scale) + 'px';
      canvasStack.style.height = Math.floor(canvas.height * scale) + 'px';
    }

    function drawPaper() {
      const image = paperCtx.createImageData(paper.width, paper.height);
      const pixels = image.data;
      for (let i = 0; i < pixels.length; i += 4) {
        // Near-imperceptible frame-to-frame paper variation: lightness moves
        // about one value and warmth by at most one additional value.
        const light = Math.round((Math.random() - 0.5) * 3);
        const warmth = Math.round((Math.random() - 0.5) * 2);
        pixels[i] = 224 + light + warmth;
        pixels[i + 1] = 222 + light;
        pixels[i + 2] = 219 + light - warmth;
        pixels[i + 3] = 255;
      }
      paperCtx.putImageData(image, 0, 0);
    }

    function drawGuides() {
      guideCtx.clearRect(0, 0, guide.width, guide.height);
      guideCtx.save();
      const alternateInk = Math.random() < 0.05;
      const mutedInks = ['#8b5f5a', '#5d7386', '#4b4a47', '#637965'];
      const guideInk = alternateInk
        ? mutedInks[Math.floor(Math.random() * mutedInks.length)]
        : '#17345f';
      const ink = normal => alternateInk ? guideInk : normal;
      guideCtx.strokeStyle = guideInk;
      guideCtx.lineWidth = 1.25;
      guideCtx.globalAlpha = 0.55;
      const w = guide.width;
      const h = guide.height;
      const shift = () => (Math.random() - 0.5) * 2;
      const mx = Math.round(w * 0.075) + 0.5;
      const my = Math.round(h * 0.075) + 0.5;
      const left = mx + shift();
      const top = my + shift();
      const right = w - mx + shift();
      const bottom = h - my + shift();
      // Existing special-design thresholds occupy 0.00–0.10. Sampling over
      // 0.00–2.00 makes that whole family exactly 5%; rectangles take 95%.
      const guideRoll = Math.random() * 2;
      guideCtx.beginPath();
      if (guideRoll < 0.01) {
        const outer = Math.min(right - left, bottom - top) * 0.5;
        const centerX = (left + right) / 2;
        const centerY = (top + bottom) / 2;
        if (guideRoll < 0.004) {
          // A layered ceremonial cross with stepped arms, inset geometry,
          // a centre medallion, and fine radial construction marks.
          const s = outer * 0.88;
          const outline = [[-.13,-1],[.13,-1],[.13,-.34],[.58,-.34],[.58,-.08],[.23,-.08],[.23,1],[-.23,1],[-.23,-.08],[-.58,-.08],[-.58,-.34],[-.13,-.34]];
          outline.forEach(([x, y], point) => {
            if (point === 0) guideCtx.moveTo(centerX + x * s, centerY + y * s);
            else guideCtx.lineTo(centerX + x * s, centerY + y * s);
          });
          guideCtx.closePath();
          guideCtx.rect(centerX - s * 0.075, centerY - s * 0.78, s * 0.15, s * 1.55);
          guideCtx.rect(centerX - s * 0.43, centerY - s * 0.25, s * 0.86, s * 0.15);
          guideCtx.arc(centerX, centerY - s * 0.16, s * 0.19, 0, Math.PI * 2);
          guideCtx.moveTo(centerX - s * 0.3, centerY - s * 0.46);
          guideCtx.lineTo(centerX + s * 0.3, centerY + s * 0.14);
          guideCtx.moveTo(centerX + s * 0.3, centerY - s * 0.46);
          guideCtx.lineTo(centerX - s * 0.3, centerY + s * 0.14);
        } else {
          const inner = outer * 0.42;
          for (let point = 0; point < 10; point++) {
            const angle = -Math.PI / 2 + point * Math.PI / 5;
            const radius = point % 2 === 0 ? outer : inner;
            const x = centerX + Math.cos(angle) * radius + shift();
            const y = centerY + Math.sin(angle) * radius + shift();
            if (point === 0) guideCtx.moveTo(x, y);
            else guideCtx.lineTo(x, y);
          }
          guideCtx.closePath();
        }
      } else if (guideRoll < 0.025) {
        guideCtx.ellipse(
          (left + right) / 2,
          (top + bottom) / 2,
          (right - left) / 2,
          (bottom - top) / 2,
          shift() * 0.001,
          0,
          Math.PI * 2
        );
      } else if (guideRoll < 0.05) {
        // Quiet ruler variations: horizontal, vertical, or just off-axis,
        // with different subdivisions and occasional ticks on both edges.
        const rulerH = Math.max(18, h * 0.026);
        const rulerVariant = Math.random();
        const angle = rulerVariant < 0.5 ? 0 : rulerVariant < 0.72 ? Math.PI / 2 : (Math.random() - 0.5) * 0.34;
        const rulerLength = angle === 0 ? right - left : Math.min(w, h) * 0.78;
        const ticks = [16, 20, 24, 32][Math.floor(Math.random() * 4)];
        const doubleSided = Math.random() < 0.32;
        const numbered = Math.random() < 0.28;
        guideCtx.save();
        guideCtx.translate(w / 2 + shift(), h / 2 + shift());
        guideCtx.rotate(angle);
        guideCtx.rect(-rulerLength / 2, -rulerH / 2, rulerLength, rulerH);
        for (let tick = 0; tick <= ticks; tick++) {
          const x = -rulerLength / 2 + rulerLength * tick / ticks;
          const tickH = tick % 4 === 0 ? rulerH * 0.62 : tick % 2 === 0 ? rulerH * 0.42 : rulerH * 0.27;
          guideCtx.moveTo(x, -rulerH / 2);
          guideCtx.lineTo(x, -rulerH / 2 + tickH);
          if (doubleSided) {
            guideCtx.moveTo(x, rulerH / 2);
            guideCtx.lineTo(x, rulerH / 2 - tickH * 0.72);
          }
        }
        if (numbered) {
          guideCtx.save();
          guideCtx.globalAlpha = 0.36;
          guideCtx.fillStyle = guideInk;
          guideCtx.font = Math.max(7, Math.round(rulerH * 0.34)) + 'px serif';
          guideCtx.textAlign = 'center';
          guideCtx.textBaseline = 'bottom';
          for (let tick = 0; tick <= ticks; tick += 2) {
            const x = -rulerLength / 2 + rulerLength * tick / ticks;
            guideCtx.fillText(String(tick), x, rulerH / 2 - 2);
          }
          guideCtx.restore();
        }
        guideCtx.restore();
      } else if (guideRoll < 0.075) {
        // Nine registration crosses in a loose three-by-three grid.
        const crossArm = Math.max(8, Math.min(w, h) * 0.012);
        for (let row = 1; row <= 3; row++) {
          for (let column = 1; column <= 3; column++) {
            const x = w * column / 4 + shift();
            const y = h * row / 4 + shift();
            guideCtx.moveTo(x - crossArm, y);
            guideCtx.lineTo(x + crossArm, y);
            guideCtx.moveTo(x, y - crossArm);
            guideCtx.lineTo(x, y + crossArm);
          }
        }
      } else if (guideRoll < 0.0775) {
        // A nearly ghosted paperclip, placed differently on each occurrence.
        guideCtx.save();
        guideCtx.globalAlpha = 0.22;
        guideCtx.translate(w * (0.25 + Math.random() * 0.5), h * (0.25 + Math.random() * 0.5));
        guideCtx.rotate((Math.random() - 0.5) * 1.3);
        const clipW = Math.min(w, h) * 0.09;
        const clipH = clipW * 2.2;
        guideCtx.roundRect(-clipW / 2, -clipH / 2, clipW, clipH, clipW / 2);
        guideCtx.roundRect(-clipW * 0.25, -clipH * 0.34, clipW * 0.5, clipH * 0.72, clipW * 0.25);
        guideCtx.stroke();
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.08) {
        // A muted, generic shop receipt—suggestive rather than readable data.
        const receiptW = Math.min(w * 0.34, 390);
        const receiptH = Math.min(h * 0.5, 430);
        const receiptX = (w - receiptW) / 2 + shift();
        const receiptY = (h - receiptH) / 2 + shift();
        guideCtx.save();
        guideCtx.globalAlpha = 0.28;
        guideCtx.strokeRect(receiptX, receiptY, receiptW, receiptH);
        guideCtx.fillStyle = guideInk;
        guideCtx.font = Math.max(11, Math.round(Math.min(w, h) * 0.013)) + 'px serif';
        guideCtx.textAlign = 'left';
        guideCtx.textBaseline = 'alphabetic';
        guideCtx.fillText('BUSINESS RECEIPT', receiptX + 16, receiptY + 28);
        const items = ['paper', 'pencil', 'coffee', 'film', 'tape', 'notebook', 'postage'];
        for (let line = 0; line < 5; line++) {
          const y = receiptY + 65 + line * 30;
          const item = items[Math.floor(Math.random() * items.length)];
          const price = (1 + Math.random() * 28).toFixed(2);
          guideCtx.fillText(item, receiptX + 16, y);
          guideCtx.textAlign = 'right';
          guideCtx.fillText(price, receiptX + receiptW - 16, y);
          guideCtx.textAlign = 'left';
        }
        guideCtx.setLineDash([3, 5]);
        guideCtx.beginPath();
        guideCtx.moveTo(receiptX + 16, receiptY + 225);
        guideCtx.lineTo(receiptX + receiptW - 16, receiptY + 225);
        guideCtx.stroke();
        guideCtx.setLineDash([]);
        guideCtx.textAlign = 'right';
        guideCtx.fillText('TOTAL  ' + (18 + Math.random() * 70).toFixed(2), receiptX + receiptW - 16, receiptY + 260);
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0825) {
        // A subdued calendar leaf for the month in which the board was made.
        const calendarW = Math.min(w * 0.42, 470);
        const calendarH = Math.min(h * 0.48, 410);
        const calendarX = (w - calendarW) / 2 + shift();
        const calendarY = (h - calendarH) / 2 + shift();
        const headerH = calendarH * 0.18;
        const cellW = calendarW / 7;
        const cellH = (calendarH - headerH) / 5;
        const now = new Date();
        guideCtx.save();
        guideCtx.globalAlpha = 0.27;
        guideCtx.strokeRect(calendarX, calendarY, calendarW, calendarH);
        guideCtx.beginPath();
        guideCtx.moveTo(calendarX, calendarY + headerH);
        guideCtx.lineTo(calendarX + calendarW, calendarY + headerH);
        for (let column = 1; column < 7; column++) {
          const x = calendarX + column * cellW;
          guideCtx.moveTo(x, calendarY + headerH);
          guideCtx.lineTo(x, calendarY + calendarH);
        }
        for (let row = 1; row < 5; row++) {
          const y = calendarY + headerH + row * cellH;
          guideCtx.moveTo(calendarX, y);
          guideCtx.lineTo(calendarX + calendarW, y);
        }
        guideCtx.stroke();
        guideCtx.fillStyle = guideInk;
        guideCtx.font = Math.max(15, Math.round(Math.min(w, h) * 0.021)) + 'px serif';
        guideCtx.textAlign = 'center';
        guideCtx.textBaseline = 'middle';
        guideCtx.fillText(
          now.toLocaleDateString('en-GB', { month: 'long', year: 'numeric' }).toLowerCase(),
          calendarX + calendarW / 2,
          calendarY + headerH / 2
        );
        guideCtx.font = Math.max(9, Math.round(Math.min(w, h) * 0.011)) + 'px serif';
        guideCtx.textAlign = 'left';
        guideCtx.textBaseline = 'top';
        for (let day = 1; day <= 31; day++) {
          const slot = day - 1;
          const column = slot % 7;
          const row = Math.floor(slot / 7);
          guideCtx.fillText(
            String(day),
            calendarX + column * cellW + 7,
            calendarY + headerH + row * cellH + 6
          );
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.085) {
        // A classical wire globe: curved meridians and latitudes make the
        // circle read as a quiet three-dimensional ball.
        const globeX = w / 2 + shift();
        const globeY = h / 2 + shift();
        const globeR = Math.min(w, h) * 0.29;
        guideCtx.save();
        guideCtx.globalAlpha = 0.42;
        guideCtx.beginPath();
        guideCtx.arc(globeX, globeY, globeR, 0, Math.PI * 2);
        guideCtx.ellipse(globeX, globeY, globeR * 0.48, globeR, 0, 0, Math.PI * 2);
        guideCtx.ellipse(globeX, globeY, globeR * 0.78, globeR, 0, 0, Math.PI * 2);
        guideCtx.ellipse(globeX, globeY, globeR, globeR * 0.34, 0, 0, Math.PI * 2);
        guideCtx.ellipse(globeX, globeY - globeR * 0.48, globeR * 0.86, globeR * 0.19, 0, 0, Math.PI * 2);
        guideCtx.ellipse(globeX, globeY + globeR * 0.48, globeR * 0.86, globeR * 0.19, 0, 0, Math.PI * 2);
        guideCtx.stroke();
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0875) {
        // Six cross-stitch motifs: flower, heart, butterfly, snowflake,
        // diamond medallion, and a tiny house.
        const motifs = [
          ['....RRR....','..RRRRRRR..','.RRRRRRRRR.','RRRRRYRRRRR','.RRRRRRRRR.','..RRRRRRR..','....RRR....','.....G.....','....GGG....','.....G.....','...G.G.....','..G..G.....','.....G.....'],
          ['.RRR...RRR.','RRRRR.RRRRR','RRRRRRRRRRR','RRRRRRRRRRR','.RRRRRRRRR.','..RRRRRRR..','...RRRRR...','....RRR....','.....R.....'],
          ['BB...G...BB','BBB..G..BBB','.BBB.G.BBB.','..BBBGBBB..','...BBGBB...','....GGG....','...BBGBB...','..BBBGBBB..','.BBB.G.BBB.','BBB..G..BBB','BB...G...BB'],
          ['.....B.....','..B..B..B..','...B.B.B...','.B..BBB..B.','..BBBBBBB..','BBBBBBBBBBB','..BBBBBBB..','.B..BBB..B.','...B.B.B...','..B..B..B..','.....B.....'],
          ['.....Y.....','....YYY....','...Y...Y...','..Y.YYY.Y..','.Y.Y...Y.Y.','Y.Y.YYY.Y.Y','.Y.Y...Y.Y.','..Y.YYY.Y..','...Y...Y...','....YYY....','.....Y.....'],
          ['.....B.....','....BBB....','...BBBBB...','..BBBBBBB..','.BBBBBBBBB.','BBBBBBBBBBB','..RRRRRRR..','..R..R..R..','..R..R..R..','..RRRRRRR..','..GGGGGGG..'],
        ];
        const motif = motifs[Math.floor(Math.random() * motifs.length)];
        const stitchSize = Math.max(5, Math.min(w, h) * 0.012);
        const spacing = stitchSize * 1.45;
        const motifX = w / 2 - (motif[0].length - 1) * spacing / 2 + shift();
        const motifY = h / 2 - (motif.length - 1) * spacing / 2 + shift();
        const colours = { R: ink('#9b6575'), G: ink('#66846f'), B: ink('#667f9b'), Y: ink('#9d8752') };
        const stitch = (x, y, colour) => {
          guideCtx.strokeStyle = colour;
          guideCtx.beginPath();
          guideCtx.moveTo(x - stitchSize / 2 + shift(), y - stitchSize / 2 + shift());
          guideCtx.lineTo(x + stitchSize / 2 + shift(), y + stitchSize / 2 + shift());
          guideCtx.moveTo(x + stitchSize / 2 + shift(), y - stitchSize / 2 + shift());
          guideCtx.lineTo(x - stitchSize / 2 + shift(), y + stitchSize / 2 + shift());
          guideCtx.stroke();
        };
        guideCtx.save();
        guideCtx.globalAlpha = 0.32;
        for (let row = 0; row < motif.length; row++) {
          for (let column = 0; column < motif[row].length; column++) {
            const mark = motif[row][column];
            if (colours[mark]) stitch(motifX + column * spacing, motifY + row * spacing, colours[mark]);
          }
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0895) {
        // Loose cross stitch fragments, as though left from another pattern.
        guideCtx.save();
        guideCtx.globalAlpha = 0.25;
        guideCtx.strokeStyle = ink('#725f70');
        const stitchSize = Math.max(5, Math.min(w, h) * 0.01);
        for (let stitch = 0; stitch < 42; stitch++) {
          const x = w * (0.12 + Math.random() * 0.76);
          const y = h * (0.12 + Math.random() * 0.76);
          guideCtx.beginPath();
          guideCtx.moveTo(x - stitchSize / 2, y - stitchSize / 2);
          guideCtx.lineTo(x + stitchSize / 2, y + stitchSize / 2);
          guideCtx.moveTo(x + stitchSize / 2, y - stitchSize / 2);
          guideCtx.lineTo(x - stitchSize / 2, y + stitchSize / 2);
          guideCtx.stroke();
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.09) {
        // A small ASCII-art sheet with a different composition each time.
        const asciiSheets = [
          ['       /\\_/\\', '      ( o.o )', '       > ^ <', '', '  signal: soft'],
          ['   .-----------.', '  /  *       *  \\', ' |      /\\      |', '  \\  *    *   /', '   `-----------\''],
          ['      .-===-.', '    .\'  .-.  \'.', '   /   (   )   \\', '  |  .-`-\'-..  |', '   \\__________/'],
        ];
        const sheet = asciiSheets[Math.floor(Math.random() * asciiSheets.length)];
        guideCtx.save();
        guideCtx.globalAlpha = 0.24;
        guideCtx.fillStyle = ink('#3d4a58');
        guideCtx.font = Math.max(12, Math.round(Math.min(w, h) * 0.019)) + 'px monospace';
        guideCtx.textAlign = 'left';
        guideCtx.textBaseline = 'top';
        const lineHeight = Math.min(w, h) * 0.034;
        const startX = w * 0.36 + shift();
        const startY = h * 0.38 + shift();
        sheet.forEach((line, row) => guideCtx.fillText(line, startX, startY + row * lineHeight));
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0925) {
        // Muted Cyrillic fragments in locally available traditional faces.
        const words = ['архив', 'кино', 'рисунок', 'свет', 'кадр', 'память', 'набросок'];
        const faces = ['serif', 'Georgia', 'Times New Roman'];
        guideCtx.save();
        guideCtx.globalAlpha = 0.24;
        guideCtx.fillStyle = ink('#3f4d5e');
        guideCtx.textAlign = 'center';
        guideCtx.textBaseline = 'middle';
        for (let line = 0; line < 4; line++) {
          const size = Math.round(Math.min(w, h) * (0.026 + Math.random() * 0.025));
          guideCtx.font = size + 'px ' + faces[Math.floor(Math.random() * faces.length)];
          guideCtx.fillText(
            words[Math.floor(Math.random() * words.length)],
            w * (0.25 + Math.random() * 0.5),
            h * (0.2 + line * 0.2) + shift()
          );
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.094) {
        // Tiny microdesign marks: registration signs, codes and geometry.
        const symbols = ['○', '△', '×', '+', '⌁', '◇', '—', '··'];
        guideCtx.save();
        guideCtx.globalAlpha = 0.24;
        guideCtx.fillStyle = guideInk;
        guideCtx.textAlign = 'center';
        guideCtx.textBaseline = 'middle';
        for (let mark = 0; mark < 32; mark++) {
          const size = Math.round(Math.min(w, h) * (0.009 + Math.random() * 0.009));
          guideCtx.font = size + 'px serif';
          const symbol = symbols[Math.floor(Math.random() * symbols.length)];
          guideCtx.fillText(symbol, w * (0.08 + Math.random() * 0.84), h * (0.08 + Math.random() * 0.84));
        }
        guideCtx.font = Math.max(8, Math.round(Math.min(w, h) * 0.009)) + 'px serif';
        guideCtx.fillText('REF ' + String(Math.floor(Math.random() * 9999)).padStart(4, '0'), w * 0.18, h * 0.87);
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.095) {
        // A deliberately nonfunctional QR-like block for registration flavor.
        const modules = 21;
        const moduleSize = Math.max(2, Math.round(Math.min(w, h) * 0.006));
        const qrSize = modules * moduleSize;
        const qrX = w * (Math.random() < 0.5 ? 0.14 : 0.86) - qrSize / 2;
        const qrY = h * (0.18 + Math.random() * 0.64) - qrSize / 2;
        const finderCell = (column, row, originX, originY) => {
          const x = column - originX;
          const y = row - originY;
          if (x < 0 || y < 0 || x > 6 || y > 6) return null;
          return x === 0 || y === 0 || x === 6 || y === 6 || (x >= 2 && x <= 4 && y >= 2 && y <= 4);
        };
        guideCtx.save();
        guideCtx.globalAlpha = 0.25;
        guideCtx.fillStyle = guideInk;
        for (let row = 0; row < modules; row++) {
          for (let column = 0; column < modules; column++) {
            const finder = finderCell(column, row, 0, 0) ??
              finderCell(column, row, modules - 7, 0) ??
              finderCell(column, row, 0, modules - 7);
            if (finder === true || (finder === null && Math.random() < 0.43)) {
              guideCtx.fillRect(qrX + column * moduleSize, qrY + row * moduleSize, moduleSize, moduleSize);
            }
          }
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0965) {
        // A handful of offset boxes with the feel of old layout marks.
        guideCtx.save();
        guideCtx.globalAlpha = 0.24;
        for (let box = 0; box < 13; box++) {
          const boxW = w * (0.035 + Math.random() * 0.16);
          const boxH = h * (0.035 + Math.random() * 0.14);
          const x = w * (0.08 + Math.random() * 0.84);
          const y = h * (0.08 + Math.random() * 0.84);
          guideCtx.save();
          guideCtx.translate(x, y);
          guideCtx.rotate((Math.random() - 0.5) * 0.12);
          guideCtx.strokeRect(-boxW / 2, -boxH / 2, boxW, boxH);
          guideCtx.restore();
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0975) {
        // Scrambled Japanese characters, treated as quiet typographic marks.
        const characters = Array.from('あかさたなはまやらわ日月火水木金土空光影絵紙線');
        guideCtx.save();
        guideCtx.globalAlpha = 0.23;
        guideCtx.fillStyle = ink('#34485b');
        guideCtx.textAlign = 'center';
        guideCtx.textBaseline = 'middle';
        for (let glyph = 0; glyph < 18; glyph++) {
          const size = Math.round(Math.min(w, h) * (0.017 + Math.random() * 0.025));
          guideCtx.font = size + 'px serif';
          guideCtx.save();
          guideCtx.translate(w * (0.1 + Math.random() * 0.8), h * (0.1 + Math.random() * 0.8));
          guideCtx.rotate((Math.random() - 0.5) * 0.18);
          guideCtx.fillText(characters[Math.floor(Math.random() * characters.length)], 0, 0);
          guideCtx.restore();
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.098) {
        // A single oversized, quiet studio wordmark in the default serif.
        guideCtx.save();
        guideCtx.globalAlpha = 0.23;
        guideCtx.fillStyle = ink('#34485b');
        guideCtx.font = Math.max(48, Math.round(Math.min(w, h) * 0.105)) + 'px serif';
        guideCtx.textAlign = 'center';
        guideCtx.textBaseline = 'middle';
        guideCtx.fillText('navylilyworks', w / 2 + shift(), h / 2 + shift(), w * 0.82);
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0985) {
        // Three elegant mathematical families: Lissajous, polar rose, and
        // hypotrochoid. Each is smooth enough to feel plotted by hand.
        const curve = Math.floor(Math.random() * 3);
        const centerX = w / 2 + shift();
        const centerY = h / 2 + shift();
        const radius = Math.min(w, h) * 0.34;
        guideCtx.save();
        guideCtx.globalAlpha = 0.34;
        guideCtx.strokeStyle = guideInk;
        guideCtx.lineWidth = 1.15;
        guideCtx.beginPath();
        if (curve === 0) {
          const a = Math.random() < 0.5 ? 3 : 5;
          const b = a === 3 ? 4 : 6;
          const phase = Math.PI / (2 + Math.floor(Math.random() * 3));
          for (let step = 0; step <= 1000; step++) {
            const t = Math.PI * 2 * step / 1000;
            const x = centerX + radius * Math.sin(a * t + phase);
            const y = centerY + radius * 0.74 * Math.sin(b * t);
            if (step === 0) guideCtx.moveTo(x, y);
            else guideCtx.lineTo(x, y);
          }
        } else if (curve === 1) {
          const petals = [5, 7, 9][Math.floor(Math.random() * 3)];
          for (let step = 0; step <= 1200; step++) {
            const t = Math.PI * 2 * step / 1200;
            const r = radius * Math.cos(petals * t);
            const x = centerX + r * Math.cos(t);
            const y = centerY + r * Math.sin(t);
            if (step === 0) guideCtx.moveTo(x, y);
            else guideCtx.lineTo(x, y);
          }
        } else {
          const outer = 5;
          const inner = 3;
          const offset = Math.random() < 0.5 ? 4.2 : 5.4;
          const extent = outer - inner + offset;
          for (let step = 0; step <= 1500; step++) {
            const t = Math.PI * 6 * step / 1500;
            const x = centerX + radius * (
              (outer - inner) * Math.cos(t) + offset * Math.cos((outer - inner) * t / inner)
            ) / extent;
            const y = centerY + radius * (
              (outer - inner) * Math.sin(t) - offset * Math.sin((outer - inner) * t / inner)
            ) / extent;
            if (step === 0) guideCtx.moveTo(x, y);
            else guideCtx.lineTo(x, y);
          }
        }
        guideCtx.stroke();
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.099) {
        // Ten true 2D fractal families, selected independently on each hit.
        const fractal = Math.floor(Math.random() * 10);
        const fx = w / 2 + shift();
        const fy = h / 2 + shift();
        const fr = Math.min(w, h) * 0.34;
        guideCtx.save();
        guideCtx.globalAlpha = 0.27;
        guideCtx.strokeStyle = ink('#294a62');
        guideCtx.fillStyle = ink('#294a62');
        guideCtx.lineWidth = 0.9;

        if (fractal === 0) {
          // Sierpiński triangle by chaos game.
          const vertices = [[fx, fy-fr], [fx-fr*.9, fy+fr*.72], [fx+fr*.9, fy+fr*.72]];
          let x = fx, y = fy;
          for (let point = 0; point < 7000; point++) {
            const vertex = vertices[Math.floor(Math.random() * 3)];
            x = (x + vertex[0]) / 2; y = (y + vertex[1]) / 2;
            guideCtx.fillRect(x, y, 1, 1);
          }
        } else if (fractal === 1) {
          // Barnsley fern IFS.
          let x = 0, y = 0;
          for (let point = 0; point < 9000; point++) {
            const roll = Math.random();
            let nextX, nextY;
            if (roll < .01) { nextX = 0; nextY = .16*y; }
            else if (roll < .86) { nextX = .85*x + .04*y; nextY = -.04*x + .85*y + 1.6; }
            else if (roll < .93) { nextX = .2*x - .26*y; nextY = .23*x + .22*y + 1.6; }
            else { nextX = -.15*x + .28*y; nextY = .26*x + .24*y + .44; }
            x = nextX; y = nextY;
            guideCtx.fillRect(fx + x*fr*.09, fy + fr*.82 - y*fr*.17, 1, 1);
          }
        } else if (fractal === 2 || fractal === 3) {
          // Binary tree and radial coral tree.
          const branch = (x, y, length, angle, depth, spread) => {
            if (depth <= 0) return;
            const x2 = x + Math.cos(angle) * length;
            const y2 = y + Math.sin(angle) * length;
            guideCtx.moveTo(x, y); guideCtx.lineTo(x2, y2);
            branch(x2, y2, length*.72, angle-spread, depth-1, spread*.96);
            branch(x2, y2, length*.72, angle+spread, depth-1, spread*.96);
          };
          guideCtx.beginPath();
          if (fractal === 2) branch(fx, fy+fr*.82, fr*.31, -Math.PI/2, 9, .42);
          else for (let ray=0; ray<7; ray++) branch(fx, fy, fr*.22, ray*Math.PI*2/7, 7, .34);
          guideCtx.stroke();
        } else if (fractal === 4) {
          // Sierpiński carpet outlines.
          const carpet = (x, y, size, depth) => {
            if (depth === 0) { guideCtx.rect(x, y, size, size); return; }
            const cell = size/3;
            for (let row=0; row<3; row++) for (let column=0; column<3; column++) {
              if (row !== 1 || column !== 1) carpet(x+column*cell, y+row*cell, cell, depth-1);
            }
          };
          guideCtx.beginPath(); carpet(fx-fr*.72, fy-fr*.72, fr*1.44, 4); guideCtx.stroke();
        } else if (fractal === 5) {
          // Recursive triangle gasket.
          const gasket = (ax, ay, bx, by, cx, cy, depth) => {
            if (depth === 0) { guideCtx.moveTo(ax,ay); guideCtx.lineTo(bx,by); guideCtx.lineTo(cx,cy); guideCtx.closePath(); return; }
            const abx=(ax+bx)/2, aby=(ay+by)/2, bcx=(bx+cx)/2, bcy=(by+cy)/2, cax=(cx+ax)/2, cay=(cy+ay)/2;
            gasket(ax,ay,abx,aby,cax,cay,depth-1); gasket(abx,aby,bx,by,bcx,bcy,depth-1); gasket(cax,cay,bcx,bcy,cx,cy,depth-1);
          };
          guideCtx.beginPath(); gasket(fx,fy-fr,fx-fr*.9,fy+fr*.72,fx+fr*.9,fy+fr*.72,5); guideCtx.stroke();
        } else if (fractal === 6) {
          // Nested circle packing.
          const circles = (x, y, radius, depth) => {
            if (depth <= 0 || radius < 2) return;
            guideCtx.moveTo(x+radius,y); guideCtx.arc(x,y,radius,0,Math.PI*2);
            const child=radius*.43;
            for (let n=0;n<3;n++) { const a=-Math.PI/2+n*Math.PI*2/3; circles(x+Math.cos(a)*radius*.54,y+Math.sin(a)*radius*.54,child,depth-1); }
          };
          guideCtx.beginPath(); circles(fx,fy,fr*.88,5); guideCtx.stroke();
        } else if (fractal === 7) {
          // Koch snowflake.
          const koch = (x1,y1,x2,y2,depth) => {
            if (depth===0) { guideCtx.moveTo(x1,y1); guideCtx.lineTo(x2,y2); return; }
            const ax=x1+(x2-x1)/3, ay=y1+(y2-y1)/3, bx=x1+(x2-x1)*2/3, by=y1+(y2-y1)*2/3;
            const px=(ax+bx)/2-Math.sqrt(3)*(by-ay)/2, py=(ay+by)/2+Math.sqrt(3)*(bx-ax)/2;
            koch(x1,y1,ax,ay,depth-1); koch(ax,ay,px,py,depth-1); koch(px,py,bx,by,depth-1); koch(bx,by,x2,y2,depth-1);
          };
          const points=[[fx,fy-fr*.9],[fx-fr*.78,fy+fr*.45],[fx+fr*.78,fy+fr*.45]];
          guideCtx.beginPath(); koch(...points[0],...points[1],4); koch(...points[1],...points[2],4); koch(...points[2],...points[0],4); guideCtx.stroke();
        } else if (fractal === 8) {
          // Heighway dragon folding curve.
          let turns=[1];
          for (let iteration=0; iteration<11; iteration++) turns=turns.concat([1],turns.slice().reverse().map(turn=>-turn));
          let angle=0, x=0, y=0; const points=[[0,0]];
          turns.forEach(turn=>{ x+=Math.cos(angle); y+=Math.sin(angle); angle+=turn*Math.PI/2; points.push([x,y]); });
          const xs=points.map(point=>point[0]), ys=points.map(point=>point[1]);
          const minX=Math.min(...xs), maxX=Math.max(...xs), minY=Math.min(...ys), maxY=Math.max(...ys);
          const scale=fr*1.65/Math.max(maxX-minX,maxY-minY);
          guideCtx.beginPath();
          points.forEach((point,index)=>{ const px=fx+(point[0]-(minX+maxX)/2)*scale, py=fy+(point[1]-(minY+maxY)/2)*scale; if(index===0)guideCtx.moveTo(px,py);else guideCtx.lineTo(px,py); });
          guideCtx.stroke();
        } else {
          // Coarse Julia-set dust.
          const columns=190, rows=140, scale=fr*1.8/columns;
          for(let row=0;row<rows;row++) for(let column=0;column<columns;column++) {
            let zx=(column/columns)*3-1.5, zy=(row/rows)*2.2-1.1, iteration=0;
            while(zx*zx+zy*zy<4 && iteration<28){ const next=zx*zx-zy*zy-.745; zy=2*zx*zy+.113; zx=next; iteration++; }
            if(iteration>10) guideCtx.fillRect(fx+(column-columns/2)*scale,fy+(row-rows/2)*scale,1,1);
          }
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0992) {
        // Formulae only: a faded mathematics board without plotted curves.
        const equations = [
          'x(t) = sin(3t + π/4)',
          'y(t) = sin(4t)',
          'r = a cos(kθ)',
          '∫₀²π r² dθ / 2',
          'x²/a² + y²/b² = 1',
          '∂u/∂t = α∇²u',
          'eⁱᶿ = cos θ + i sin θ',
        ];
        guideCtx.save();
        guideCtx.globalAlpha = 0.27;
        guideCtx.fillStyle = ink('#34485b');
        guideCtx.textAlign = 'left';
        guideCtx.textBaseline = 'middle';
        for (let line = 0; line < 5; line++) {
          const equation = equations[(line + Math.floor(Math.random() * equations.length)) % equations.length];
          const size = Math.round(Math.min(w, h) * (0.025 + Math.random() * 0.012));
          guideCtx.font = 'italic ' + size + 'px serif';
          guideCtx.fillText(equation, w * (0.18 + Math.random() * 0.08), h * (0.2 + line * 0.14) + shift());
        }
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.0995) {
        // An isometric three-axis plane with randomly raised wire columns.
        const cell = Math.min(w, h) * 0.052;
        const originX = w / 2 + shift();
        const originY = h * 0.61 + shift();
        const project = (x, y, z = 0) => ({
          x: originX + (x - y) * cell,
          y: originY + (x + y) * cell * 0.5 - z * cell,
        });
        const edge = (a, b) => {
          guideCtx.moveTo(a.x, a.y);
          guideCtx.lineTo(b.x, b.y);
        };
        guideCtx.save();
        guideCtx.globalAlpha = 0.3;
        guideCtx.strokeStyle = guideInk;
        guideCtx.lineWidth = 1.1;
        guideCtx.beginPath();
        const extent = 4;
        for (let line = -extent; line <= extent; line++) {
          edge(project(-extent, line), project(extent, line));
          edge(project(line, -extent), project(line, extent));
        }
        edge(project(0, 0), project(extent + 1, 0));
        edge(project(0, 0), project(0, extent + 1));
        edge(project(0, 0), project(0, 0, extent + 1));
        for (let column = 0; column < 6; column++) {
          const x = -3 + Math.floor(Math.random() * 6);
          const y = -3 + Math.floor(Math.random() * 6);
          const height = 0.7 + Math.random() * 3.3;
          const a = project(x, y);
          const b = project(x + 0.62, y);
          const c = project(x + 0.62, y + 0.62);
          const d = project(x, y + 0.62);
          const at = project(x, y, height);
          const bt = project(x + 0.62, y, height);
          const ct = project(x + 0.62, y + 0.62, height);
          const dt = project(x, y + 0.62, height);
          edge(a, at); edge(b, bt); edge(c, ct); edge(d, dt);
          edge(at, bt); edge(bt, ct); edge(ct, dt); edge(dt, at);
        }
        guideCtx.stroke();
        guideCtx.restore();
        guideCtx.beginPath();
      } else if (guideRoll < 0.10) {
        // Five families of small topographic maps: square survey grid,
        // numbered contour loops, and both engineered and wandering roads.
        const mapLayouts = [
          [[0.27,0.34],[0.68,0.63]],
          [[0.22,0.68],[0.52,0.31],[0.78,0.56]],
          [[0.34,0.48],[0.73,0.28]],
          [[0.2,0.27],[0.5,0.67],[0.8,0.35]],
          [[0.38,0.3],[0.62,0.72]],
        ];
        const mapStyle = Math.floor(Math.random() * mapLayouts.length);
        const mapW = w * 0.68;
        const mapH = h * 0.64;
        const mapX = (w - mapW) / 2 + shift();
        const mapY = (h - mapH) / 2 + shift();
        guideCtx.save();
        guideCtx.globalAlpha = 0.25;
        guideCtx.strokeStyle = ink('#35526a');
        guideCtx.fillStyle = ink('#35526a');
        guideCtx.lineWidth = 1;
        guideCtx.beginPath();
        guideCtx.rect(mapX, mapY, mapW, mapH);
        guideCtx.clip();

        // Survey grid.
        guideCtx.beginPath();
        for (let column = 0; column <= 10; column++) {
          const x = mapX + mapW * column / 10;
          guideCtx.moveTo(x, mapY);
          guideCtx.lineTo(x, mapY + mapH);
        }
        for (let row = 0; row <= 8; row++) {
          const y = mapY + mapH * row / 8;
          guideCtx.moveTo(mapX, y);
          guideCtx.lineTo(mapX + mapW, y);
        }
        guideCtx.stroke();

        // Closed elevation rings, slightly irregular but never noisy.
        guideCtx.globalAlpha = 0.34;
        mapLayouts[mapStyle].forEach((peak, peakIndex) => {
          for (let ring = 0; ring < 5; ring++) {
            const rx = mapW * (0.12 - ring * 0.017);
            const ry = mapH * (0.14 - ring * 0.019);
            const centerX = mapX + mapW * peak[0];
            const centerY = mapY + mapH * peak[1];
            guideCtx.beginPath();
            for (let point = 0; point <= 56; point++) {
              const angle = Math.PI * 2 * point / 56;
              const wobble = 1 + Math.sin(angle * (3 + mapStyle) + peakIndex) * 0.035 + Math.sin(angle * 7) * 0.018;
              const x = centerX + Math.cos(angle) * rx * wobble;
              const y = centerY + Math.sin(angle) * ry * wobble;
              if (point === 0) guideCtx.moveTo(x, y);
              else guideCtx.lineTo(x, y);
            }
            guideCtx.closePath();
            guideCtx.stroke();
            if (ring === 1 || ring === 3) {
              guideCtx.font = Math.max(8, Math.round(Math.min(w, h) * 0.009)) + 'px serif';
              guideCtx.textAlign = 'left';
              guideCtx.textBaseline = 'middle';
              guideCtx.fillText(String(200 + peakIndex * 100 + ring * 120), centerX + rx * 0.72, centerY - ry * 0.7);
            }
          }
        });

        // One surveyed straight road and one softer road following the land.
        guideCtx.globalAlpha = 0.42;
        guideCtx.lineWidth = 2;
        guideCtx.beginPath();
        if (mapStyle % 2 === 0) {
          guideCtx.moveTo(mapX, mapY + mapH * 0.82);
          guideCtx.lineTo(mapX + mapW, mapY + mapH * 0.18);
        } else {
          guideCtx.moveTo(mapX + mapW * 0.14, mapY);
          guideCtx.lineTo(mapX + mapW * 0.78, mapY + mapH);
        }
        guideCtx.stroke();
        guideCtx.beginPath();
        guideCtx.moveTo(mapX - 10, mapY + mapH * (0.28 + mapStyle * 0.05));
        guideCtx.bezierCurveTo(
          mapX + mapW * 0.24, mapY + mapH * 0.05,
          mapX + mapW * 0.62, mapY + mapH * 0.92,
          mapX + mapW + 10, mapY + mapH * 0.58
        );
        guideCtx.stroke();
        guideCtx.restore();
        guideCtx.beginPath();
      } else {
        guideCtx.moveTo(left, top);
        guideCtx.lineTo(right, top);
        guideCtx.lineTo(right, bottom);
        guideCtx.lineTo(left, bottom);
        guideCtx.closePath();
      }
      guideCtx.stroke();
      // The nine-cross variant already owns the centre and needs no darker
      // duplicate. Every other guide keeps the familiar registration cross.
      if (!(guideRoll >= 0.05 && guideRoll < 0.075) && !(guideRoll >= 0.0825 && guideRoll < 0.10)) {
        const cx = Math.round(w / 2) + 0.5 + shift();
        const cy = Math.round(h / 2) + 0.5 + shift();
        const arm = Math.max(18, Math.round(Math.min(w, h) * 0.035)) + shift();
        guideCtx.beginPath();
        guideCtx.moveTo(cx - arm, cy);
        guideCtx.lineTo(cx + arm, cy);
        guideCtx.moveTo(cx, cy - arm);
        guideCtx.lineTo(cx, cy + arm);
        guideCtx.stroke();
      }

      const creationDate = new Intl.DateTimeFormat('en-GB', {
        day: 'numeric',
        month: 'long',
        year: 'numeric',
      }).format(new Date()).toLowerCase();
      guideCtx.globalAlpha = 0.62;
      guideCtx.fillStyle = '#504d48';
      guideCtx.font = Math.max(15, Math.round(Math.min(w, h) * 0.018)) + 'px serif';
      guideCtx.textAlign = 'right';
      guideCtx.textBaseline = 'bottom';
      guideCtx.fillText(creationDate, w - 18, h - 14);
      guideCtx.restore();
      applyTool();
    }

    function applyTrace() {
      traceBtn.classList.toggle('active', trace);
      traceBtn.textContent = trace ? 'Trace on' : 'Trace';
      traceBtn.title = trace ? 'Hide tracing reference' : 'Show reference under the board';
      traceRef.hidden = !trace || !images.length;
      if (trace && img.src) {
        traceRef.src = img.src;
      }
    }

    function toggleTrace() {
      trace = !trace;
      localStorage.setItem('pictogrepStoryTrace', trace ? '1' : '0');
      applyTrace();
      status(trace ? 'Trace on' : 'Trace off');
    }

    function applyTool() {
      brushValue.textContent = brush.value;
      penBtn.classList.toggle('active', tool === 'pen');
      eraserBtn.classList.toggle('active', tool === 'eraser');
      shadeBtn.classList.toggle('active', tool === 'shade');
      shadeColour.hidden = tool !== 'shade';
      canvas.style.cursor = tool === 'eraser' ? 'cell' : tool === 'shade' ? 'crosshair' : 'crosshair';
    }

    function setTool(nextTool) {
      tool = nextTool;
      applyTool();
      status(tool === 'eraser' ? 'Eraser' : tool === 'shade' ? 'Colour pencil lasso — draw a closed shape' : 'Pen');
    }

    function showImage() {
      cancelScheduledSave();
      if (!images.length) {
        img.hidden = true;
        traceRef.hidden = true;
        traceRef.removeAttribute('src');
        empty.hidden = false;
        refName.textContent = '';
        progress.max = 1;
        progress.value = 0;
        counter.textContent = '0/0';
        return;
      }
      const item = images[index];
      img.hidden = false;
      empty.hidden = true;
      img.src = item.url + '?t=' + Date.now();
      img.alt = item.name;
      traceRef.src = img.src;
      refName.textContent = item.name;
      progress.max = images.length;
      progress.value = index + 1;
      counter.textContent = (index + 1) + '/' + images.length;
      resetCanvas();
      applyTrace();
    }

    async function loadImages() {
      if (dirty) await saveCurrent(false);
      if (document.activeElement === clipSearch) clipSearch.blur();
      const q = clipSearch.value.trim();
      if (q) {
        status('Searching...');
        const res = await fetch('/api/search?q=' + encodeURIComponent(q) + '&tag=' + encodeURIComponent(collection.value));
        const data = await res.json();
        if (!res.ok) {
          status(data.error || 'Search failed');
          return;
        }
        images = data.images;
        index = 0;
        infoSavePath.textContent = data.out || '';
        status(data.selected + ' CLIP matches for: ' + q);
        showImage();
        return;
      }
      const opt = mode.options[mode.selectedIndex];
      count.hidden = !opt.dataset.custom;
      const n = opt.dataset.custom ? count.value : (opt.dataset.count || count.value);
      const url = '/api/images?mode=' + encodeURIComponent(mode.value) + '&count=' + encodeURIComponent(n) + '&tag=' + encodeURIComponent(collection.value);
      const res = await fetch(url);
      const data = await res.json();
      images = data.images;
      index = 0;
      infoSavePath.textContent = data.out || '';
      status(data.total + ' available, ' + images.length + ' selected');
      showImage();
    }

    function pointerPos(e) {
      const rect = canvas.getBoundingClientRect();
      const frameRect = boardFrame.getBoundingClientRect();
      const inside = e.clientX >= rect.left && e.clientX <= rect.right &&
        e.clientY >= rect.top && e.clientY <= rect.bottom;
      const insideBoardFrame = e.clientX >= frameRect.left && e.clientX <= frameRect.right &&
        e.clientY >= frameRect.top && e.clientY <= frameRect.bottom;
      return {
        x: (e.clientX - rect.left) * canvas.width / rect.width,
        y: (e.clientY - rect.top) * canvas.height / rect.height,
        pressure: e.pointerType === 'pen' && e.pressure > 0 ? e.pressure : 0.5,
        // Shade shapes may begin in the surrounding dark area. Their points
        // can sit beyond the canvas edge; Canvas clips the final fill cleanly.
        inside: inside || (tool === 'shade' && insideBoardFrame),
      };
    }

    function pushUndo() {
      undoStack.push({
        pen: ctx.getImageData(0, 0, canvas.width, canvas.height),
        fill: fillCtx.getImageData(0, 0, fill.width, fill.height),
      });
      if (undoStack.length > 20) undoStack.shift();
    }

    function captureStrokeUndo() {
      if (strokeUndoCaptured) return;
      pushUndo();
      strokeUndoCaptured = true;
    }

    function dabColour(alpha) {
      return 'rgba(0,0,0,' + alpha + ')';
    }

    function paintDab(p) {
      const radius = Math.max(0.6, Number(brush.value) / 2);
      if (tool === 'pen') {
        const rawPressure = Math.max(0.12, Math.min(1, p.pressure || 0.5));
        const pressure = Math.max(0.05, Math.min(1, 0.5 + (rawPressure - 0.5) * 1.2));
        const width = Math.max(0.7, Number(brush.value) * 0.62 * (0.45 + pressure * 0.9));
        const height = width * 2.15;
        ctx.save();
        ctx.globalCompositeOperation = 'source-over';
        ctx.fillStyle = 'rgba(35,34,31,' + (0.38 + pressure * 0.32) + ')';
        ctx.beginPath();
        ctx.roundRect(
          p.x - width / 2,
          p.y - height / 2,
          width,
          height,
          Math.min(width / 2, 1.5)
        );
        ctx.fill();
        ctx.restore();
        return;
      }
      const soft = Math.max(0.35, radius * 0.22);
      const prevComposite = ctx.globalCompositeOperation;
      ctx.globalCompositeOperation = tool === 'eraser' ? 'destination-out' : 'source-over';
      const grad = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, radius + soft);
      grad.addColorStop(0, dabColour(0.98));
      grad.addColorStop(Math.max(0.72, radius / (radius + soft)), dabColour(0.94));
      grad.addColorStop(1, dabColour(0));
      ctx.fillStyle = grad;
      ctx.beginPath();
      ctx.arc(p.x, p.y, radius + soft, 0, Math.PI * 2);
      ctx.fill();
      ctx.globalCompositeOperation = prevComposite;
    }

    function paintBetween(a, b) {
      const radius = Math.max(0.6, Number(brush.value) / 2);
      const step = Math.max(0.45, radius * 0.42);
      const dx = b.x - a.x;
      const dy = b.y - a.y;
      const dist = Math.hypot(dx, dy);
      const count = Math.max(1, Math.ceil(dist / step));
      for (let i = 1; i <= count; i++) {
        const t = i / count;
        paintDab({
          x: a.x + dx * t,
          y: a.y + dy * t,
          pressure: (a.pressure || 0.5) + ((b.pressure || 0.5) - (a.pressure || 0.5)) * t,
        });
      }
    }

    function paintEvent(e) {
      const raw = pointerPos(e);
      if (!raw.inside) {
        lastPoint = null;
        return false;
      }
      captureStrokeUndo();
      // A small low-pass filter takes the edge off hand jitter while keeping
      // the pen responsive. The eraser stays exact so it remains precise.
      const p = tool === 'pen' && lastPoint
        ? {
            x: lastPoint.x + (raw.x - lastPoint.x) * 0.64,
            y: lastPoint.y + (raw.y - lastPoint.y) * 0.64,
            pressure: lastPoint.pressure + (raw.pressure - lastPoint.pressure) * 0.64,
            inside: true,
          }
        : raw;
      if (!lastPoint) {
        paintDab(p);
      } else {
        paintBetween(lastPoint, p);
      }
      lastPoint = p;
      return true;
    }

    function drawLassoPreview() {
      lassoCtx.clearRect(0, 0, lasso.width, lasso.height);
      if (lassoPoints.length < 2) return;
      lassoCtx.save();
      lassoCtx.strokeStyle = shadeColour.value;
      lassoCtx.globalAlpha = 0.8;
      lassoCtx.lineWidth = 2;
      lassoCtx.setLineDash([8, 6]);
      lassoCtx.beginPath();
      lassoCtx.moveTo(lassoPoints[0].x, lassoPoints[0].y);
      for (const point of lassoPoints.slice(1)) lassoCtx.lineTo(point.x, point.y);
      lassoCtx.stroke();
      lassoCtx.restore();
    }

    function finishLasso() {
      lassoCtx.clearRect(0, 0, lasso.width, lasso.height);
      if (lassoPoints.length < 3) return false;
      captureStrokeUndo();
      fillCtx.save();
      fillCtx.beginPath();
      fillCtx.moveTo(lassoPoints[0].x, lassoPoints[0].y);
      for (const point of lassoPoints.slice(1)) fillCtx.lineTo(point.x, point.y);
      fillCtx.closePath();
      fillCtx.clip();

      let minX = Infinity;
      let maxX = -Infinity;
      let minY = Infinity;
      let maxY = -Infinity;
      for (const point of lassoPoints) {
        minX = Math.min(minX, point.x);
        maxX = Math.max(maxX, point.x);
        minY = Math.min(minY, point.y);
        maxY = Math.max(maxY, point.y);
      }
      const colour = shadeColour.value;

      // A very pale waxy base prevents large shapes from looking empty.
      fillCtx.globalAlpha = 0.075;
      fillCtx.fillStyle = colour;
      fillCtx.fillRect(minX, minY, maxX - minX, maxY - minY);

      // Scatter translucent pigment into the clipped area. This gives the
      // lasso colored-pencil grain without diagonal or repeating linework.
      const rgb = colour.match(/[a-f\d]{2}/gi).map(value => parseInt(value, 16));
      const grainCanvas = document.createElement('canvas');
      grainCanvas.width = Math.max(1, Math.ceil(maxX - minX));
      grainCanvas.height = Math.max(1, Math.ceil(maxY - minY));
      const grainCtx = grainCanvas.getContext('2d');
      const grainImage = grainCtx.createImageData(grainCanvas.width, grainCanvas.height);
      const grainPixels = grainImage.data;
      for (let i = 0; i < grainPixels.length; i += 4) {
        if (Math.random() > 0.46) continue;
        grainPixels[i] = rgb[0];
        grainPixels[i + 1] = rgb[1];
        grainPixels[i + 2] = rgb[2];
        grainPixels[i + 3] = 10 + Math.floor(Math.random() * 34);
      }
      grainCtx.putImageData(grainImage, 0, 0);
      fillCtx.globalAlpha = 1;
      fillCtx.drawImage(grainCanvas, Math.floor(minX), Math.floor(minY));
      fillCtx.restore();
      return true;
    }

    function markStrokeChanged() {
      strokeChanged = true;
      dirty = true;
      drawingPresent = true;
      changeRevision++;
      saveState.textContent = 'Unsaved changes';
    }

    function canStartStroke(e) {
      if (e.target.closest('button, input, select, label, .divider, .tools, .bar, .board-ref')) return false;
      return Boolean(e.target.closest('.board-frame, .left-pane .frame'));
    }

    workArea.addEventListener('pointerdown', e => {
      if (!canStartStroke(e)) return;
      if (e.button !== undefined && e.button !== 0) return;
      e.preventDefault();
      cancelScheduledSave();
      drawing = true;
      strokeChanged = false;
      strokeUndoCaptured = false;
      lastPoint = null;
      lassoPoints = [];
      workArea.setPointerCapture(e.pointerId);
      applyTool();
      if (tool === 'shade') {
        const p = pointerPos(e);
        if (p.inside) lassoPoints.push(p);
      } else if (paintEvent(e)) markStrokeChanged();
    });

    workArea.addEventListener('pointermove', e => {
      if (!drawing) return;
      e.preventDefault();
      applyTool();
      if (tool === 'shade') {
        const p = pointerPos(e);
        if (p.inside) {
          lassoPoints.push(p);
          drawLassoPreview();
        }
        return;
      }
      const events = typeof e.getCoalescedEvents === 'function' ? e.getCoalescedEvents() : [e];
      let painted = false;
      for (const event of events) {
        if (paintEvent(event)) painted = true;
      }
      if (painted) markStrokeChanged();
    });

    function endStroke() {
      if (!drawing) return;
      drawing = false;
      lastPoint = null;
      if (tool === 'shade' && finishLasso()) markStrokeChanged();
      lassoPoints = [];
      if (strokeChanged) scheduleSave();
    }
    workArea.addEventListener('pointerup', endStroke);
    workArea.addEventListener('pointercancel', endStroke);
    workArea.addEventListener('contextmenu', e => {
      if (canStartStroke(e)) e.preventDefault();
    });
    workArea.addEventListener('dragstart', e => e.preventDefault());
    img.addEventListener('dragstart', e => e.preventDefault());

    divider.addEventListener('pointerdown', e => {
      if (e.target.closest('button')) return;
      resizing = true;
      divider.classList.add('dragging');
      divider.setPointerCapture(e.pointerId);
      setPaneSplit(e.clientX);
    });
    divider.addEventListener('pointermove', e => {
      if (resizing) setPaneSplit(e.clientX);
    });
    function endResize() {
      resizing = false;
      divider.classList.remove('dragging');
    }
    divider.addEventListener('pointerup', endResize);
    divider.addEventListener('pointercancel', endResize);
    divider.addEventListener('dblclick', resetView);
    viewReset.onclick = e => {
      e.preventDefault();
      e.stopPropagation();
      resetView();
    };
    refSmaller.onclick = () => setReferenceScale(refScale - 0.1);
    refBigger.onclick = () => setReferenceScale(refScale + 0.1);
    refMirror.onclick = toggleReferenceMirror;
    boardSmaller.onclick = () => setBoardScale(boardScale - 0.1);
    boardBigger.onclick = () => setBoardScale(boardScale + 0.1);
    window.addEventListener('resize', fitCanvasStack);

    function scheduleSave() {
      cancelScheduledSave();
      saveTimer = setTimeout(() => {
        saveTimer = null;
        const run = () => {
          saveIdleHandle = null;
          if (!drawing && dirty) saveCurrent(false);
        };
        if ('requestIdleCallback' in window) {
          saveIdleHandle = requestIdleCallback(run, {timeout: 1800});
        } else {
          saveTimer = setTimeout(run, 0);
        }
      }, 1400);
    }

    function boardHasDrawing() {
      if (drawingPresent) return true;
      for (const layer of [ctx, fillCtx]) {
        const pixels = layer.getImageData(0, 0, canvas.width, canvas.height).data;
        for (let i = 3; i < pixels.length; i += 4) {
          if (pixels[i] !== 0) return true;
        }
      }
      return false;
    }

    function canvasDataUrlAsync(source) {
      return new Promise((resolve, reject) => {
        source.toBlob(blob => {
          if (!blob) {
            reject(new Error('Could not encode drawing'));
            return;
          }
          const reader = new FileReader();
          reader.onload = () => resolve(reader.result);
          reader.onerror = () => reject(reader.error || new Error('Could not read drawing'));
          reader.readAsDataURL(blob);
        }, 'image/png');
      });
    }

    async function saveCurrent(showStatus = true) {
      if (!images.length) return false;
      cancelScheduledSave();
      if (saving) {
        saveAgain = true;
        return true;
      }
      if (!boardHasDrawing()) {
        dirty = false;
        saveState.textContent = 'Empty drawing not saved';
        if (showStatus) status('Draw something before saving, or use Skip.');
        return false;
      }
      saving = true;
      const savingRevision = changeRevision;
      const item = images[index];
      try {
        const payload = {
          index: index + 1,
          imageId: item.id,
          imageName: item.name,
          query: clipSearch.value.trim(),
          aspect: aspect.value,
          hasDrawing: true,
          dataUrl: await canvasDataUrlAsync(exportCanvas()),
        };
        const res = await fetch('/api/save', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        const data = await res.json();
        if (!res.ok || !data.ok) {
          const message = data.error || 'unknown error';
          saveState.textContent = 'Save failed';
          status('Save failed: ' + message);
          throw new Error(message);
        }
        if (changeRevision === savingRevision) dirty = false;
        saveState.textContent = 'Saved ' + data.file;
        if (showStatus) status('Saved ' + data.file);
        return true;
      } finally {
        saving = false;
        if (saveAgain) {
          saveAgain = false;
          if (dirty) scheduleSave();
        }
      }
    }

    async function next(save) {
      if (!images.length) return;
      if (save) {
        const saved = await saveCurrent(false);
        if (!saved) {
          status('Empty drawing not saved. Use Skip to move on.');
          return;
        }
      }
      if (index >= images.length - 1) {
        status('Finished ' + images.length + ' images.');
        saveState.textContent = save ? 'Saved final image' : 'Final image skipped';
        return;
      }
      index = Math.min(images.length - 1, index + 1);
      showImage();
    }

    function undo() {
      const prev = undoStack.pop();
      if (!prev) return;
      ctx.putImageData(prev.pen, 0, 0);
      fillCtx.putImageData(prev.fill, 0, 0);
      drawingPresent = false;
      drawingPresent = boardHasDrawing();
      applyTool();
      dirty = true;
      changeRevision++;
      saveState.textContent = 'Unsaved changes';
      scheduleSave();
    }

    function clearBoard() {
      pushUndo();
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      fillCtx.clearRect(0, 0, fill.width, fill.height);
      drawingPresent = false;
      dirty = true;
      changeRevision++;
      saveState.textContent = 'Unsaved changes';
      scheduleSave();
    }

    function exportCanvas() {
      const out = document.createElement('canvas');
      out.width = canvas.width;
      out.height = canvas.height;
      const outCtx = out.getContext('2d');
      // Warm drawing-paper base: this is baked into the PNG, not just shown
      // as a browser background.
      outCtx.fillStyle = '#e0dedb';
      outCtx.fillRect(0, 0, out.width, out.height);
      outCtx.drawImage(paper, 0, 0);
      outCtx.drawImage(fill, 0, 0);
      outCtx.drawImage(guide, 0, 0);
      outCtx.drawImage(canvas, 0, 0);
      return out;
    }

    document.getElementById('start').onclick = loadImages;
    clipSearch.addEventListener('keydown', e => {
      if (e.key === 'Enter') {
        e.preventDefault();
        loadImages();
      } else if (e.key === 'Escape') {
        clipSearch.value = '';
        loadImages();
      }
    });
    document.getElementById('save').onclick = () => saveCurrent(true);
    document.getElementById('next').onclick = () => next(true);
    document.getElementById('skip').onclick = () => next(false);
    document.getElementById('prev').onclick = async () => {
      if (dirty) await saveCurrent(false);
      if (index <= 0) {
        status('Already at the first image.');
        return;
      }
      index = Math.max(0, index - 1);
      showImage();
    };
    document.getElementById('clear').onclick = clearBoard;
    document.getElementById('undo').onclick = undo;
    penBtn.onclick = () => setTool('pen');
    eraserBtn.onclick = () => setTool('eraser');
    shadeBtn.onclick = () => setTool('shade');
    traceBtn.onclick = toggleTrace;
    brush.oninput = applyTool;
    addRefs.onclick = () => refFiles.click();
    refFiles.onchange = async () => {
      const available = Math.max(0, 5 - boardReferences.length);
      const files = Array.from(refFiles.files || []).slice(0, available);
      if (!files.length) {
        status(available ? 'Choose an image to add.' : 'Five-reference limit reached.');
        refFiles.value = '';
        return;
      }
      status('Adding reference images...');
      for (const file of files) {
        const dataUrl = await new Promise((resolve, reject) => {
          const reader = new FileReader();
          reader.onload = () => resolve(reader.result);
          reader.onerror = () => reject(reader.error);
          reader.readAsDataURL(file);
        });
        const res = await fetch('/api/references', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({name: file.name, dataUrl}),
        });
        const data = await res.json();
        if (!res.ok) {
          status(data.error || 'Could not add reference image.');
          break;
        }
      }
      refFiles.value = '';
      await loadBoardReferences();
      status(boardReferences.length + ' board reference' + (boardReferences.length === 1 ? '' : 's'));
    };
    aspect.onchange = async () => {
      if (dirty) await saveCurrent(false);
      resetCanvas();
    };
    mode.onchange = () => { count.hidden = !mode.options[mode.selectedIndex].dataset.custom; };
    collection.onchange = loadImages;

    async function loadCollections() {
      const res = await fetch('/api/collections');
      if (!res.ok) return;
      const data = await res.json();
      const escapeHTML = value => String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
      collection.innerHTML = '<option value="">All tags</option>' + data.collections.map(item =>
        '<option value="' + escapeHTML(item.name) + '">' + escapeHTML(item.name) + ' (' + item.count + ')</option>'
      ).join('');
    }
    document.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
        e.preventDefault();
        next(true);
        return;
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z') {
        e.preventDefault();
        e.stopPropagation();
        undo();
        return;
      }
      if (e.target.matches('input, select, textarea')) return;
      if (e.key === '1' || e.key.toLowerCase() === 'p') {
        e.preventDefault();
        setTool('pen');
        return;
      }
      if (e.key === '2' || e.key.toLowerCase() === 'e') {
        e.preventDefault();
        setTool('eraser');
        return;
      }
      if (e.key === '3' || e.key.toLowerCase() === 's') {
        e.preventDefault();
        setTool('shade');
        return;
      }
      if (e.key === '4' || e.key.toLowerCase() === 't') {
        e.preventDefault();
        toggleTrace();
        return;
      }
      if (e.key.toLowerCase() === 'q' || e.key.toLowerCase() === 'w') {
        e.preventDefault();
        const delta = e.key.toLowerCase() === 'q' ? -1 : 1;
        brush.value = String(Math.max(Number(brush.min), Math.min(Number(brush.max), Number(brush.value) + delta)));
        applyTool();
        status('Pen size ' + brush.value);
        return;
      }
      if (e.key.toLowerCase() === 'z') {
        e.preventDefault();
        undo();
      }
    }, true);

    restorePaneSplit();
    setReferenceScale(refScale);
    setBoardScale(boardScale, false);
    applyReferenceMirror();
    resetCanvas();
    loadBoardReferences();
    loadCollections().then(loadImages);
  </script>
</body>
</html>"""


class StoryboardHandler(BaseHTTPRequestHandler):
    server_version = "PictogrepStoryboard/1.0"

    def image_path_from_request(self):
        try:
            image_id = int(urlparse(self.path).path.rsplit("/", 1)[1])
            return self.server.paths[image_id]
        except (ValueError, IndexError):
            return None

    def send_image(self, path, include_body=True):
        if not path or not path.exists():
            self.send_error(404)
            return
        ctype = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(path.stat().st_size))
        self.end_headers()
        if include_body:
            with path.open("rb") as fh:
                self.wfile.write(fh.read())

    def send_json(self, data, status=200):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/":
            body = page_html().encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if parsed.path == "/api/images":
            params = parse_qs(parsed.query)
            mode = params.get("mode", ["recent"])[0]
            try:
                count = max(1, int(params.get("count", ["30"])[0]))
            except ValueError:
                count = 30
            tag = params.get("tag", [""])[0]
            try:
                paths = collection_images(tag) if tag else self.server.paths
            except ValueError as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
                return
            images = image_payload(paths, mode, count, lambda _, path: image_record(self.server.id_for_path(path), path))
            self.send_json({
                "images": images,
                "selected": len(images),
                "total": len(paths),
                "out": str(self.server.out_dir),
            })
            return
        if parsed.path == "/api/collections":
            collections = []
            for name in collection_names():
                collections.append({"name": name, "count": len(collection_images(name))})
            self.send_json({"collections": collections})
            return
        if parsed.path == "/api/references":
            references = [
                {"name": path.name, "url": "/reference/" + quote(path.name)}
                for path in self.server.reference_paths()
            ]
            self.send_json({"references": references, "limit": 5})
            return
        if parsed.path == "/api/search":
            params = parse_qs(parsed.query)
            query = params.get("q", [""])[0].strip()
            tag = params.get("tag", [""])[0]
            try:
                tagged = {path.resolve() for path in collection_images(tag)} if tag else None
            except ValueError as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
                return
            if not query:
                paths = list(tagged) if tagged is not None else self.server.paths
                images = image_payload(paths, "all", 80, lambda _, path: image_record(self.server.id_for_path(path), path))
                self.send_json({
                    "images": images,
                    "selected": len(images),
                    "total": len(paths),
                    "out": str(self.server.out_dir),
                })
                return
            try:
                results = clip_search(query, limit=80)
                images = []
                for result in results:
                    path = Path(result["path"]).expanduser().resolve()
                    if not path.exists():
                        continue
                    if tagged is not None and path not in tagged:
                        continue
                    image_id = self.server.id_for_path(path)
                    images.append(image_record(image_id, path))
                self.send_json({
                    "images": images,
                    "selected": len(images),
                    "total": len(self.server.paths),
                    "query": query,
                    "out": str(self.server.out_dir),
                })
            except Exception as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
            return
        if parsed.path.startswith("/image/"):
            self.send_image(self.image_path_from_request())
            return
        if parsed.path.startswith("/reference/"):
            name = Path(unquote(parsed.path.split("/reference/", 1)[1])).name
            self.send_image(self.server.reference_dir / name)
            return
        self.send_error(404)

    def do_HEAD(self):
        parsed = urlparse(self.path)
        if parsed.path.startswith("/image/"):
            self.send_image(self.image_path_from_request(), include_body=False)
            return
        if parsed.path.startswith("/reference/"):
            name = Path(unquote(parsed.path.split("/reference/", 1)[1])).name
            self.send_image(self.server.reference_dir / name, include_body=False)
            return
        self.send_error(404)

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path == "/api/references":
            length = int(self.headers.get("Content-Length", "0"))
            try:
                if len(self.server.reference_paths()) >= 5:
                    self.send_json({"ok": False, "error": "five-reference limit reached"}, status=400)
                    return
                data = json.loads(self.rfile.read(length))
                header, encoded = data["dataUrl"].split(",", 1)
                mime = header.split(";", 1)[0].split(":", 1)[-1]
                if not mime.startswith("image/"):
                    raise ValueError("reference must be an image")
                raw = base64.b64decode(encoded, validate=True)
                if len(raw) > 15 * 1024 * 1024:
                    raise ValueError("reference image is larger than 15 MB")
                name = clean_reference_name(data.get("name", "reference"), mime)
                stem, suffix = Path(name).stem, Path(name).suffix
                path = self.server.reference_dir / name
                counter = 2
                while path.exists():
                    path = self.server.reference_dir / f"{stem}-{counter}{suffix}"
                    counter += 1
                path.write_bytes(raw)
                self.send_json({"ok": True, "name": path.name})
            except Exception as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
            return
        if parsed.path != "/api/save":
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        try:
            data = json.loads(self.rfile.read(length))
            if data.get("hasDrawing") is not True:
                self.send_json({"ok": False, "error": "empty drawing"}, status=400)
                return
            header, encoded = data["dataUrl"].split(",", 1)
            raw = base64.b64decode(encoded)
            aspect = data.get("aspect", "16:9").replace(":", "x")
            index = int(data.get("index", 1))
            name = clean_name(data.get("imageName", "image"))
            filename = f"{index:04d}_{aspect}_{name}.png"
            path = self.server.out_dir / filename
            path.write_bytes(raw)
            source = self.server.paths[int(data["imageId"])].resolve()
            meta = {
                "file": filename,
                "source": str(source),
                "aspect": data.get("aspect", "16:9"),
                "tags": tags_for_image(source),
                "query": str(data.get("query", "")).strip(),
            }
            path.with_suffix(".json").write_text(json.dumps(meta, indent=2))
            self.send_json({"ok": True, "file": filename})
        except Exception as exc:
            self.send_json({"ok": False, "error": str(exc)}, status=400)

    def do_DELETE(self):
        parsed = urlparse(self.path)
        if parsed.path != "/api/references":
            self.send_error(404)
            return
        name = Path(parse_qs(parsed.query).get("name", [""])[0]).name
        path = self.server.reference_dir / name
        if not name or not path.is_file():
            self.send_json({"ok": False, "error": "reference not found"}, status=404)
            return
        path.unlink()
        self.send_json({"ok": True})

    def log_message(self, fmt, *args):
        return


class StoryboardServer(ThreadingHTTPServer):
    def __init__(self, address, handler, paths, out_dir):
        super().__init__(address, handler)
        self.paths = list(paths)
        self.path_ids = {p.resolve(): i for i, p in enumerate(self.paths)}
        self.out_dir = out_dir
        self.reference_dir = out_dir / "references"
        self.reference_dir.mkdir(parents=True, exist_ok=True)

    def reference_paths(self):
        return sorted(
            (path for path in self.reference_dir.iterdir() if path.is_file()),
            key=lambda path: path.stat().st_mtime,
        )[:5]

    def id_for_path(self, path):
        path = path.resolve()
        if path not in self.path_ids:
            self.path_ids[path] = len(self.paths)
            self.paths.append(path)
        return self.path_ids[path]


def main(argv=None):
    parser = argparse.ArgumentParser(description="Open the Pictogrep storyboard browser.")
    parser.add_argument("folder", nargs="?", help="optional image folder; defaults to the indexed library")
    parser.add_argument("--out", help="output folder for PNG boards")
    parser.add_argument("--project", action="store_true", help="use refs/visual and storyboards/inbox from the current Milklily project")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--no-open", action="store_true", help="do not open the browser automatically")
    args = parser.parse_args(argv)

    folder = args.folder
    out = args.out
    if args.project:
        project = find_milklily_project()
        if not project:
            parser.error("--project needs a Milklily project (no milklily.conf found)")
        if folder is None:
            candidate = project / "refs" / "visual"
            if image_files(candidate):
                folder = str(candidate)
        if out is None:
            out = str(project / "storyboards" / "inbox")
    paths = collect_images(folder)
    out_dir = Path(out or BASE / "storyboards").expanduser().resolve()
    out_dir.mkdir(parents=True, exist_ok=True)
    try:
        server = StoryboardServer(("127.0.0.1", args.port), StoryboardHandler, paths, out_dir)
    except OSError as exc:
        # The default is convenient, but it should never prevent drawing when
        # another local app (or an older Pictogrep window) already uses it.
        if args.port != DEFAULT_PORT or exc.errno != 98:
            parser.error(f"could not start storyboard server on port {args.port}: {exc}")
        server = StoryboardServer(("127.0.0.1", 0), StoryboardHandler, paths, out_dir)
        print(f"Port {args.port} is already in use; using port {server.server_port} instead.")
    url = f"http://127.0.0.1:{server.server_port}/"
    print(f"Pictogrep storyboard: {url}")
    print(f"{len(paths)} images available. Saving to: {out_dir}")
    if not args.no_open:
        webbrowser.open(url)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nStoryboard server stopped.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
