(() => {
  "use strict";

  const state = {
    page: 1,
    pageSize: 12,
    totalPages: 1,
    folders: [],
    currentFolder: null,
    currentTracks: [],
    currentTrack: -1,
    selected: new Set(),
    taskTimer: null
  };

  const $ = (selector) => document.querySelector(selector);
  const $$ = (selector) => [...document.querySelectorAll(selector)];
  const escapeHTML = (value = "") => String(value).replace(/[&<>"']/g, char => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;"
  })[char]);
  const api = async (url, options) => {
    const response = await fetch(url, options);
    const json = await response.json().catch(() => ({ msg: "服务返回了无法解析的数据" }));
    if (!response.ok || json.code !== 200) throw new Error(json.msg || `请求失败 (${response.status})`);
    return json.data;
  };
  const assetPath = (folder, file) => encodeURI(`/${folder.baseDir}/${folder.name}/${file.path}`.replace(/\/+/g, "/"));
  const mediaFiles = folder => (folder.files || []).filter(file => !file.isDir && /\.(mp3|wav|flac|m4a|ogg)$/i.test(file.name));
  const coverFile = folder => (folder.files || []).find(file => !file.isDir && /\.(jpe?g|png|webp)$/i.test(file.name));
  const initials = value => (String(value || "AR").match(/[A-Za-z0-9]+/)?.[0] || "AR").slice(0, 2).toUpperCase();

  let player;
  document.addEventListener("DOMContentLoaded", () => {
    player = new Plyr("#player", {
      controls: ["play", "progress", "current-time", "mute", "volume"],
      keyboard: { focused: true, global: true }
    });
    bindEvents();
    Promise.allSettled([loadStatus(), loadFolders(), loadTasks()]);
    state.taskTimer = window.setInterval(loadTasks, 3500);
  });

  function bindEvents() {
    $$(".nav-item").forEach(button => button.addEventListener("click", () => switchView(button.dataset.view)));
    $("#prevPage").addEventListener("click", () => { if (state.page > 1) { state.page--; loadFolders(); } });
    $("#nextPage").addEventListener("click", () => { if (state.page < state.totalPages) { state.page++; loadFolders(); } });
    $("#libraryFilter").addEventListener("input", renderFolders);
    $("#searchForm").addEventListener("submit", searchWorks);
    $("#downloadSelected").addEventListener("click", queueDownloads);
    $("#refreshTasks").addEventListener("click", loadTasks);
    $("#closePlayer").addEventListener("click", () => { player.pause(); $("#playerDock").hidden = true; });
    player.on("ended", playNext);
    document.addEventListener("keydown", event => {
      if (event.target.matches("input")) return;
      if (event.key.toLowerCase() === "l") switchView("library");
      if (event.key.toLowerCase() === "s") { switchView("discover"); $("#searchInput").focus(); }
    });
  }

  function switchView(name) {
    $$(".nav-item").forEach(item => item.classList.toggle("is-active", item.dataset.view === name));
    $$(".view").forEach(view => view.classList.toggle("is-active", view.id === `view-${name}`));
    if (name === "tasks") loadTasks();
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  async function loadStatus() {
    try {
      const data = await api("/api/status");
      $("#serverDot").classList.add("online");
      $("#serverLabel").textContent = data.configured ? "本地服务运行中" : "配置需要完善";
      $("#dataFolderLabel").textContent = data.dataFolder;
      $("#dataFolderLabel").title = data.dataFolder;
      $("#libraryCount").textContent = data.libraryCount;
    } catch (error) {
      $("#serverLabel").textContent = "无法连接本地服务";
      toast(error.message, true);
    }
  }

  async function loadFolders() {
    $("#folderList").innerHTML = '<div class="skeleton-card"></div><div class="skeleton-card"></div><div class="skeleton-card"></div>';
    try {
      const data = await api(`/api/list?page=${state.page}&pageSize=${state.pageSize}`);
      state.folders = data.infos || [];
      state.totalPages = Math.max(1, Math.ceil(data.total / data.pageSize));
      $("#pageInfo").textContent = `${data.page} / ${state.totalPages}`;
      $("#prevPage").disabled = state.page <= 1;
      $("#nextPage").disabled = state.page >= state.totalPages;
      renderFolders();
    } catch (error) {
      renderError($("#folderList"), error.message, loadFolders);
    }
  }

  function renderFolders() {
    const query = $("#libraryFilter").value.trim().toLowerCase();
    const folders = state.folders.filter(folder => `${folder.title} ${folder.mediaId} ${folder.name}`.toLowerCase().includes(query));
    if (!folders.length) {
      $("#folderList").innerHTML = `<div class="empty-wide"><span>◌</span><p>${query ? "当前页没有匹配作品" : "资料库还是空的，请先下载作品"}</p></div>`;
      return;
    }
    $("#folderList").innerHTML = folders.map(folder => {
      const cover = coverFile(folder);
      const selected = state.currentFolder?.id === folder.id;
      return `<button class="folder-card${selected ? " is-active" : ""}" type="button" data-folder-id="${folder.id}">
        <span class="cover">${cover ? `<img src="${assetPath(folder, cover)}" alt="">` : escapeHTML(initials(folder.mediaId))}</span>
        <span class="folder-copy">
          <span class="folder-title">${escapeHTML(folder.title || folder.name)}</span>
          <span class="folder-meta"><span>${escapeHTML(folder.mediaId)}</span><span>${mediaFiles(folder).length} 首</span>${folder.hasSubtitles ? "<span>有字幕</span>" : ""}</span>
        </span>
      </button>`;
    }).join("");
    $$(".folder-card").forEach(card => card.addEventListener("click", () => selectFolder(Number(card.dataset.folderId))));
  }

  function selectFolder(id) {
    const folder = state.folders.find(item => item.id === id);
    if (!folder) return;
    state.currentFolder = folder;
    state.currentTracks = mediaFiles(folder);
    state.currentTrack = -1;
    renderFolders();
    $("#trackEmpty").hidden = true;
    $("#trackContent").hidden = false;
    $("#selectedTitle").textContent = folder.title || folder.name;
    $("#selectedMeta").textContent = folder.mediaId || "LOCAL";
    $("#selectedDetail").textContent = `${folder.date || "日期未知"} · ${folder.hasSubtitles ? "包含字幕" : "无字幕标记"}`;
    $("#trackCount").textContent = `${state.currentTracks.length} 首`;
    setCover($("#selectedCover"), folder);
    renderTracks();
  }

  function setCover(element, folder) {
    const cover = coverFile(folder);
    element.innerHTML = cover ? `<img src="${assetPath(folder, cover)}" alt="">` : `<span>${escapeHTML(initials(folder.mediaId))}</span>`;
  }

  function renderTracks() {
    if (!state.currentTracks.length) {
      $("#mediaList").innerHTML = '<li class="empty-wide"><p>这个目录中没有支持的音频文件</p></li>';
      return;
    }
    $("#mediaList").innerHTML = state.currentTracks.map((file, index) => {
      const format = file.name.split(".").pop();
      return `<li><button class="track-item${index === state.currentTrack ? " is-active" : ""}" type="button" data-track="${index}">
        <span class="track-index">${index === state.currentTrack ? "▶" : String(index + 1).padStart(2, "0")}</span>
        <span class="track-name">${escapeHTML(file.name)}</span><span class="track-format">${escapeHTML(format)}</span>
      </button></li>`;
    }).join("");
    $$(".track-item").forEach(item => item.addEventListener("click", () => playTrack(Number(item.dataset.track))));
  }

  function playTrack(index) {
    const file = state.currentTracks[index];
    if (!file || !state.currentFolder) return;
    state.currentTrack = index;
    const src = assetPath(state.currentFolder, file);
    const subtitle = (state.currentFolder.files || []).find(item => item.path === `${file.path}.vtt`);
    player.source = {
      type: "audio",
      title: file.name,
      sources: [{ src, type: audioType(file.name) }],
      tracks: subtitle ? [{ kind: "captions", label: "中文", srclang: "zh", src: assetPath(state.currentFolder, subtitle), default: true }] : []
    };
    $("#playerDock").hidden = false;
    $("#nowTitle").textContent = file.name;
    $("#nowWork").textContent = state.currentFolder.title || state.currentFolder.name;
    setCover($("#nowCover"), state.currentFolder);
    renderTracks();
    player.play().catch(() => toast("浏览器阻止了自动播放，请点击播放按钮"));
  }

  function playNext() {
    if (state.currentTrack + 1 < state.currentTracks.length) playTrack(state.currentTrack + 1);
  }

  function audioType(name) {
    const ext = name.split(".").pop().toLowerCase();
    return ({ mp3: "audio/mpeg", wav: "audio/wav", flac: "audio/flac", m4a: "audio/mp4", ogg: "audio/ogg" })[ext] || "audio/mpeg";
  }

  async function searchWorks(event) {
    event.preventDefault();
    const query = $("#searchInput").value.trim();
    if (!query) return;
    state.selected.clear();
    updateSelection();
    $("#searchSummary").textContent = `正在搜索“${query}”…`;
    $("#searchResults").innerHTML = Array(8).fill('<div class="skeleton-card"></div>').join("");
    try {
      const data = await api(`/api/search?q=${encodeURIComponent(query)}`);
      renderSearchResults(data.works || []);
      $("#searchSummary").textContent = `找到 ${data.pagination?.totalCount ?? data.works?.length ?? 0} 个相关作品`;
    } catch (error) {
      renderError($("#searchResults"), error.message, () => $("#searchForm").requestSubmit());
      $("#searchSummary").textContent = "搜索没有完成";
    }
  }

  function renderSearchResults(works) {
    if (!works.length) {
      $("#searchResults").innerHTML = '<div class="empty-wide"><span>◌</span><p>没有找到相关作品，试试更简短的关键词</p></div>';
      return;
    }
    $("#searchResults").innerHTML = works.map(work => {
      const cover = work.thumbnailCoverUrl || work.samCoverUrl || work.mainCoverUrl;
      return `<button class="result-card" type="button" data-source-id="${escapeHTML(work.source_id)}" aria-pressed="false">
        <span class="result-cover">${cover ? `<img src="${escapeHTML(cover)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : "♪"}<span class="result-check">✓</span></span>
        <span class="result-copy"><h2>${escapeHTML(work.title)}</h2>
          <span class="result-meta"><span class="result-id">${escapeHTML(work.source_id)}</span><span>★ ${Number(work.rate_average_2dp || 0).toFixed(1)}</span>${work.has_subtitle ? "<span>字幕</span>" : ""}</span>
        </span>
      </button>`;
    }).join("");
    $$(".result-card").forEach(card => card.addEventListener("click", () => toggleResult(card)));
  }

  function toggleResult(card) {
    const id = card.dataset.sourceId;
    state.selected.has(id) ? state.selected.delete(id) : state.selected.add(id);
    card.classList.toggle("is-selected", state.selected.has(id));
    card.setAttribute("aria-pressed", String(state.selected.has(id)));
    updateSelection();
  }

  function updateSelection() {
    $("#selectedCount").textContent = `已选 ${state.selected.size} 项`;
    $("#downloadSelected").disabled = state.selected.size === 0;
  }

  async function queueDownloads() {
    const ids = [...state.selected];
    $("#downloadSelected").disabled = true;
    $("#downloadSelected").textContent = "正在加入…";
    try {
      await api("/api/downloads", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ids })
      });
      state.selected.clear();
      $$(".result-card").forEach(card => { card.classList.remove("is-selected"); card.setAttribute("aria-pressed", "false"); });
      updateSelection();
      toast(`已将 ${ids.length} 部作品加入下载队列`);
      await loadTasks();
      switchView("tasks");
    } catch (error) {
      toast(error.message, true);
    } finally {
      $("#downloadSelected").textContent = "加入下载";
      updateSelection();
    }
  }

  async function loadTasks() {
    try {
      const tasks = await api("/api/tasks");
      const running = tasks.filter(task => task.status === "running" || task.status === "queued");
      $("#runningCount").textContent = running.length;
      $("#completedCount").textContent = tasks.filter(task => task.status === "completed").length;
      $("#failedCount").textContent = tasks.filter(task => task.status === "failed").length;
      $("#taskBadge").hidden = running.length === 0;
      $("#taskBadge").textContent = running.length;
      renderTasks(tasks);
    } catch (error) {
      if ($("#view-tasks").classList.contains("is-active")) toast(error.message, true);
    }
  }

  function renderTasks(tasks) {
    if (!tasks.length) {
      $("#taskList").innerHTML = '<div class="empty-wide"><span>↓</span><p>还没有下载任务，从“发现作品”开始吧</p></div>';
      return;
    }
    $("#taskList").innerHTML = tasks.map(task => {
      const pct = task.total ? Math.round(task.completed / task.total * 100) : 0;
      const labels = { queued: "等待中", running: "下载中", completed: "已完成", failed: "下载失败" };
      return `<article class="task-card">
        <div class="task-symbol">${task.status === "completed" ? "✓" : task.status === "failed" ? "!" : "↓"}</div>
        <div class="task-copy"><strong>${escapeHTML(task.label)}</strong><span>${task.completed} / ${task.total} 部作品${task.error ? ` · ${escapeHTML(task.error)}` : ""}</span><div class="task-progress"><span style="width:${pct}%"></span></div></div>
        <span class="task-status ${escapeHTML(task.status)}">${labels[task.status] || task.status}</span>
      </article>`;
    }).join("");
  }

  function renderError(container, message, retry) {
    container.innerHTML = `<div class="empty-wide"><span>!</span><p>${escapeHTML(message)}</p><button class="button ghost" type="button">重试</button></div>`;
    container.querySelector("button").addEventListener("click", retry);
  }

  function toast(message, isError = false) {
    const item = document.createElement("div");
    item.className = `toast${isError ? " error" : ""}`;
    item.textContent = message;
    $("#toastRegion").appendChild(item);
    window.setTimeout(() => item.remove(), 3800);
  }
})();
