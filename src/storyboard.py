import argparse
import base64
import json
import mimetypes
from pathlib import Path
import random
import re
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse
import webbrowser

from bildkasten_core import (
    BASE,
    METADATA_PATH,
    collection_images,
    collection_names,
    image_files,
    search as clip_search,
    find_movielily_project,
    tags_for_image,
)


DEFAULT_PORT = 8765


def clean_name(value):
    value = Path(value).stem.lower()
    value = re.sub(r"[^a-z0-9]+", "-", value).strip("-")
    return value[:60] or "image"


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
  <title>Bildkasten Storyboard</title>
  <style>
    :root { color-scheme: light; --bg:#f5f5f2; --paper:#fff; --fg:#171717; --muted:#666; --line:#c9c9c3; --soft:#eeeeea; }
    * { box-sizing: border-box; }
    html, body { height: 100%; overflow: hidden; }
    body { min-height: 100vh; margin: 0; display: flex; flex-direction: column; font: 15px/1.35 system-ui, sans-serif; background: var(--bg); color: var(--fg); }
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
    .board-frame { background: #474744; touch-action: none; user-select: none; -webkit-user-select: none; }
    #reference { width: var(--ref-size, 100%); height: var(--ref-size, 100%); max-width: none; max-height: none; object-fit: contain; -webkit-user-drag: none; user-select: none; }
    #reference.mirrored, .trace-ref.mirrored { transform: scaleX(-1); }
    .canvas-stack { position: relative; background: #fff; border: 1px solid #a7a7a0; user-select: none; -webkit-user-select: none; }
    .canvas-stack canvas, .trace-ref { position: absolute; inset: 0; width: 100%; height: 100%; }
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
      <strong>BILDKASTEN STORYBOARD</strong>
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
          <div class="info-row"><strong>Keys</strong><span>P pen · E eraser · S shade lasso · T trace · Ctrl+Enter save + next · Ctrl+Z undo</span></div>
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
        <div id="empty" class="empty" hidden>No images found. Run bildkasten index /path/to/images or pass a folder to storyboard.</div>
      </div>
      <div class="bar"><progress id="progress" value="0" max="1"></progress><span id="counter">0/0</span></div>
    </section>
    <div id="divider" class="divider" title="Drag to resize"><button id="viewReset" title="Reset view">=</button></div>
    <section class="pane right-pane">
      <div class="label">
        <span class="label-title"><strong>Board</strong><button id="trace" title="Show reference under the board">Trace</button></span>
        <span id="saveState" class="save-state">Not saved yet</span>
      </div>
      <div id="boardFrame" class="frame board-frame">
        <div id="canvasStack" class="canvas-stack">
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
        <button id="shade" title="Draw a closed shape to fill behind pen marks">Shade</button>
        <select id="shadeColour" title="Shade colour">
          <option value="#000000">Grey</option>
          <option value="#bd7d90">Muted pink</option>
          <option value="#c7a75d">Muted yellow</option>
          <option value="#759bb5">Muted blue</option>
          <option value="#7eaa91">Muted green</option>
          <option value="#a48ab5">Muted lilac</option>
        </select>
        <label>Size <input id="brush" type="range" min="1" max="22" value="4"> <span id="brushValue">4</span></label>
        <button id="undo">Undo</button>
        <button id="clear">Clear</button>
        <button id="save">Save now</button>
      </div>
      <div class="status">Autosaves after each stroke. PNG files go to the local storyboards folder.</div>
    </section>
  </main>

  <script>
    const img = document.getElementById('reference');
    const empty = document.getElementById('empty');
    const workArea = document.querySelector('main');
    const canvasStack = document.getElementById('canvasStack');
    const traceRef = document.getElementById('traceRef');
    const boardFrame = document.getElementById('boardFrame');
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
    let images = [];
    let index = 0;
    let drawing = false;
    let dirty = false;
    let undoStack = [];
    let saveTimer = null;
    let tool = 'pen';
    let trace = localStorage.getItem('bildkastenStoryTrace') === '1';
    let refScale = Number(localStorage.getItem('bildkastenStoryRefScale') || 1);
    if (!Number.isFinite(refScale)) refScale = 1;
    let refMirrored = localStorage.getItem('bildkastenStoryRefMirror') === '1';
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
      localStorage.setItem('bildkastenStorySplit', pct.toFixed(1));
      requestAnimationFrame(fitCanvasStack);
    }

    function restorePaneSplit() {
      const pct = Number(localStorage.getItem('bildkastenStorySplit') || 50);
      if (Number.isFinite(pct)) {
        setPaneSplitPercent(pct);
      }
    }

    function setReferenceScale(nextScale, showStatus = true) {
      refScale = Math.max(0.45, Math.min(2.4, nextScale));
      img.style.setProperty('--ref-size', Math.round(refScale * 100) + '%');
      localStorage.setItem('bildkastenStoryRefScale', refScale.toFixed(2));
      if (showStatus) status('Reference image ' + Math.round(refScale * 100) + '%');
    }

    function resetView() {
      setPaneSplitPercent(50);
      setReferenceScale(1, false);
      status('View reset');
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
      localStorage.setItem('bildkastenStoryRefMirror', refMirrored ? '1' : '0');
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
      drawGuides();
      undoStack = [];
      dirty = false;
      saveState.textContent = 'Not saved yet';
    }

    function fitCanvasStack() {
      if (!canvas.width || !canvas.height) return;
      const style = getComputedStyle(boardFrame);
      const padX = parseFloat(style.paddingLeft) + parseFloat(style.paddingRight);
      const padY = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
      const maxW = Math.max(1, boardFrame.clientWidth - padX);
      const maxH = Math.max(1, boardFrame.clientHeight - padY);
      const scale = Math.min(maxW / canvas.width, maxH / canvas.height);
      canvasStack.style.width = Math.floor(canvas.width * scale) + 'px';
      canvasStack.style.height = Math.floor(canvas.height * scale) + 'px';
    }

    function drawGuides() {
      guideCtx.clearRect(0, 0, guide.width, guide.height);
      guideCtx.save();
      guideCtx.strokeStyle = '#17345f';
      guideCtx.lineWidth = 1.25;
      guideCtx.globalAlpha = 0.55;
      const w = guide.width;
      const h = guide.height;
      const mx = Math.round(w * 0.075) + 0.5;
      const my = Math.round(h * 0.075) + 0.5;
      guideCtx.strokeRect(mx, my, Math.max(1, w - mx * 2), Math.max(1, h - my * 2));
      const cx = Math.round(w / 2) + 0.5;
      const cy = Math.round(h / 2) + 0.5;
      const arm = Math.max(18, Math.round(Math.min(w, h) * 0.035));
      guideCtx.beginPath();
      guideCtx.moveTo(cx - arm, cy);
      guideCtx.lineTo(cx + arm, cy);
      guideCtx.moveTo(cx, cy - arm);
      guideCtx.lineTo(cx, cy + arm);
      guideCtx.stroke();
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
      localStorage.setItem('bildkastenStoryTrace', trace ? '1' : '0');
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
      status(tool === 'eraser' ? 'Eraser' : tool === 'shade' ? 'Shade lasso — draw a closed shape' : 'Pen');
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
      const inside = e.clientX >= rect.left && e.clientX <= rect.right &&
        e.clientY >= rect.top && e.clientY <= rect.bottom;
      return {
        x: (e.clientX - rect.left) * canvas.width / rect.width,
        y: (e.clientY - rect.top) * canvas.height / rect.height,
        inside,
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
        paintDab({ x: a.x + dx * t, y: a.y + dy * t });
      }
    }

    function paintEvent(e) {
      const p = pointerPos(e);
      if (!p.inside) {
        lastPoint = null;
        return false;
      }
      captureStrokeUndo();
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
      fillCtx.globalAlpha = 0.4;
      fillCtx.fillStyle = shadeColour.value;
      fillCtx.beginPath();
      fillCtx.moveTo(lassoPoints[0].x, lassoPoints[0].y);
      for (const point of lassoPoints.slice(1)) fillCtx.lineTo(point.x, point.y);
      fillCtx.closePath();
      fillCtx.fill();
      fillCtx.restore();
      return true;
    }

    function markStrokeChanged() {
      strokeChanged = true;
      dirty = true;
      saveState.textContent = 'Unsaved changes';
    }

    function canStartStroke(e) {
      if (e.target.closest('button, input, select, label, .divider, .tools, .bar')) return false;
      return Boolean(e.target.closest('.board-frame, .left-pane .frame'));
    }

    workArea.addEventListener('pointerdown', e => {
      if (!canStartStroke(e)) return;
      if (e.button !== undefined && e.button !== 0) return;
      e.preventDefault();
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
    window.addEventListener('resize', fitCanvasStack);

    function scheduleSave() {
      cancelScheduledSave();
      saveTimer = setTimeout(() => saveCurrent(false), 650);
    }

    function boardHasDrawing() {
      for (const layer of [ctx, fillCtx]) {
        const pixels = layer.getImageData(0, 0, canvas.width, canvas.height).data;
        for (let i = 3; i < pixels.length; i += 4) {
          if (pixels[i] !== 0) return true;
        }
      }
      return false;
    }

    async function saveCurrent(showStatus = true) {
      if (!images.length) return false;
      cancelScheduledSave();
      if (!boardHasDrawing()) {
        dirty = false;
        saveState.textContent = 'Empty drawing not saved';
        if (showStatus) status('Draw something before saving, or use Skip.');
        return false;
      }
      const item = images[index];
      const payload = {
        index: index + 1,
        imageId: item.id,
        imageName: item.name,
        query: clipSearch.value.trim(),
        aspect: aspect.value,
        hasDrawing: true,
        dataUrl: exportCanvas().toDataURL('image/png'),
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
      dirty = false;
      saveState.textContent = 'Saved ' + data.file;
      if (showStatus) status('Saved ' + data.file);
      return true;
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
      applyTool();
      dirty = true;
      saveState.textContent = 'Unsaved changes';
      scheduleSave();
    }

    function clearBoard() {
      pushUndo();
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      fillCtx.clearRect(0, 0, fill.width, fill.height);
      dirty = true;
      saveState.textContent = 'Unsaved changes';
      scheduleSave();
    }

    function exportCanvas() {
      const out = document.createElement('canvas');
      out.width = canvas.width;
      out.height = canvas.height;
      const outCtx = out.getContext('2d');
      outCtx.fillStyle = 'white';
      outCtx.fillRect(0, 0, out.width, out.height);
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
      if (e.key.toLowerCase() === 'p') {
        e.preventDefault();
        setTool('pen');
        return;
      }
      if (e.key.toLowerCase() === 'e') {
        e.preventDefault();
        setTool('eraser');
        return;
      }
      if (e.key.toLowerCase() === 's') {
        e.preventDefault();
        setTool('shade');
        return;
      }
      if (e.key.toLowerCase() === 't') {
        e.preventDefault();
        toggleTrace();
        return;
      }
      if (e.key.toLowerCase() === 'z') {
        e.preventDefault();
        undo();
      }
    }, true);

    restorePaneSplit();
    setReferenceScale(refScale);
    applyReferenceMirror();
    resetCanvas();
    loadCollections().then(loadImages);
  </script>
</body>
</html>"""


class StoryboardHandler(BaseHTTPRequestHandler):
    server_version = "BildkastenStoryboard/1.0"

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
        self.send_error(404)

    def do_HEAD(self):
        if urlparse(self.path).path.startswith("/image/"):
            self.send_image(self.image_path_from_request(), include_body=False)
            return
        self.send_error(404)

    def do_POST(self):
        if urlparse(self.path).path != "/api/save":
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

    def log_message(self, fmt, *args):
        return


class StoryboardServer(ThreadingHTTPServer):
    def __init__(self, address, handler, paths, out_dir):
        super().__init__(address, handler)
        self.paths = list(paths)
        self.path_ids = {p.resolve(): i for i, p in enumerate(self.paths)}
        self.out_dir = out_dir

    def id_for_path(self, path):
        path = path.resolve()
        if path not in self.path_ids:
            self.path_ids[path] = len(self.paths)
            self.paths.append(path)
        return self.path_ids[path]


def main(argv=None):
    parser = argparse.ArgumentParser(description="Open the Bildkasten storyboard browser.")
    parser.add_argument("folder", nargs="?", help="optional image folder; defaults to the indexed library")
    parser.add_argument("--out", help="output folder for PNG boards")
    parser.add_argument("--project", action="store_true", help="use refs/visual and storyboards/inbox from the current Movielily project")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--no-open", action="store_true", help="do not open the browser automatically")
    args = parser.parse_args(argv)

    folder = args.folder
    out = args.out
    if args.project:
        project = find_movielily_project()
        if not project:
            parser.error("--project needs a Movielily project (no movielily.conf found)")
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
        # another local app (or an older Bildkasten window) already uses it.
        if args.port != DEFAULT_PORT or exc.errno != 98:
            parser.error(f"could not start storyboard server on port {args.port}: {exc}")
        server = StoryboardServer(("127.0.0.1", 0), StoryboardHandler, paths, out_dir)
        print(f"Port {args.port} is already in use; using port {server.server_port} instead.")
    url = f"http://127.0.0.1:{server.server_port}/"
    print(f"Bildkasten storyboard: {url}")
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
