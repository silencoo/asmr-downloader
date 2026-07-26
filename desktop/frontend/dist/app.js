(() => {
  "use strict";
  const state = {
    works: [],
    currentWork: null,
    currentTrack: -1,
    playQueue: [],
    playQueueIndex: -1,
    playQueueContext: "",
    selected: new Set(),
    settings: null,
    searchQuery: "",
    searchWorks: [],
    searchPage: 1,
    searchPageSize: 20,
    searchTotalPages: 0,
    siteTab: "works",
    siteInitialized: false,
    catalogs: {},
    catalogKind: "circles",
    catalogQuery: "",
    catalogPage: 1,
    catalogPageSize: 60,
    catalogTotalPages: 0,
    detail: null,
    detailPlayableTracks: [],
    tasks: [],
    taskSelected: new Set()
  };
  const $ = selector => document.querySelector(selector);
  const $$ = selector => [...document.querySelectorAll(selector)];
  const backend = () => window.go?.main?.App;
  const escapeHTML = (value = "") => String(value).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[c]);
  const compactNumber = value => Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);

  document.addEventListener("DOMContentLoaded", async () => {
    bindEvents();
    try {
      const appState = await backend().GetState();
      state.settings = appState.settings;
      $("#versionLabel").textContent = appState.version;
      $("#setupNotice").hidden = appState.configured;
      fillSettings(appState.settings);
      $("#loginCurrentAccount").textContent = appState.settings.account || "guest";
      await Promise.all([loadLibrary(), loadTasks()]);
      window.runtime?.EventsOn("tasks:updated", renderTasks);
    } catch (error) {
      toast(normalizeError(error), true);
    }
  });

  function bindEvents() {
    $$(".nav-button").forEach(button => button.addEventListener("click", () => showPage(button.dataset.page)));
    $$("[data-open-settings]").forEach(button => button.addEventListener("click", () => showPage("settings")));
    $("#libraryFilter").addEventListener("input", debounce(loadLibrary, 250));
    $("#openLibrary").addEventListener("click", async () => call(() => backend().RevealDownloadDirectory()));
    $("#searchForm").addEventListener("submit", search);
    $("#queueSelected").addEventListener("click", queueSelected);
    $("#searchPrev").addEventListener("click", () => changeSearchPage(state.searchPage - 1));
    $("#searchNext").addEventListener("click", () => changeSearchPage(state.searchPage + 1));
    $("#searchPageInput").addEventListener("change", () => changeSearchPage(Number($("#searchPageInput").value)));
    $("#sitePageSize").addEventListener("change", changeSitePageSize);
    $$(".site-tab").forEach(button => button.addEventListener("click", () => activateSiteTab(button.dataset.siteTab)));
    $("#shuffleAgain").addEventListener("click", loadSearchPage);
    $("#siteExternal").addEventListener("click", () => backend().OpenASMRWebsite());
    $("#openMirror").addEventListener("click", () => backend().OpenASMRMirror());
    $("#catalogFilter").addEventListener("input", debounce(() => {
      state.catalogQuery = $("#catalogFilter").value.trim();
      state.catalogPage = 1;
      renderCatalog();
    }, 180));
    $("#catalogPrev").addEventListener("click", () => changeCatalogPage(state.catalogPage - 1));
    $("#catalogNext").addEventListener("click", () => changeCatalogPage(state.catalogPage + 1));
    $("#catalogPageInput").addEventListener("change", () => changeCatalogPage(Number($("#catalogPageInput").value)));
    $("#detailBack").addEventListener("click", closeWorkDetail);
    $("#siteLoginForm").addEventListener("submit", siteLogin);
    $("#refreshTasks").addEventListener("click", loadTasks);
    $("#taskSelectAll").addEventListener("change", toggleAllTasks);
    $("#retrySelectedTasks").addEventListener("click", retrySelectedTasks);
    $("#cancelSelectedTasks").addEventListener("click", cancelSelectedTasks);
    $("#deleteSelectedTasks").addEventListener("click", deleteSelectedTasks);
    $("#chooseDirectory").addEventListener("click", chooseDirectory);
    $("#settingsForm").addEventListener("submit", saveSettings);
    $("#closePlayer").addEventListener("click", closePlayer);
    $("#audioPlayer").addEventListener("ended", playNext);
    document.addEventListener("keydown", event => {
      if (event.target.matches("input, select")) return;
      if (event.key.toLowerCase() === "l") showPage("library");
      if (event.key.toLowerCase() === "s") {
        showPage("discover");
        if (!$("#searchForm").hidden) $("#searchInput").focus();
      }
    });
  }

  function showPage(name) {
    $$(".nav-button").forEach(button => button.classList.toggle("active", button.dataset.page === name));
    $$(".page").forEach(page => page.classList.toggle("active", page.id === `page-${name}`));
    if (name === "tasks") loadTasks();
    if (name === "discover" && !state.siteInitialized) {
      state.siteInitialized = true;
      activateSiteTab("works");
    }
  }

  async function loadLibrary() {
    $("#workGrid").innerHTML = Array(6).fill('<div class="skeleton"></div>').join("");
    try {
      const result = await backend().GetLibrary($("#libraryFilter").value);
      state.works = result.works || [];
      $("#librarySummary").textContent = `${result.total} 部作品已整理在本地`;
      renderLibrary();
    } catch (error) {
      renderError($("#workGrid"), normalizeError(error), loadLibrary);
    }
  }

  function renderLibrary() {
    if (!state.works.length) {
      $("#workGrid").innerHTML = '<div class="empty-state wide"><span>◌</span><h2>资料库还是空的</h2><p>从“站点浏览”页面选择作品并加入下载。</p></div>';
      return;
    }
    $("#workGrid").innerHTML = state.works.map(work => `<button class="work-card${state.currentWork?.folder === work.folder ? " active" : ""}" type="button" data-folder="${escapeHTML(work.folder)}">
      <span class="work-cover">${work.coverUrl ? `<img src="${escapeHTML(work.coverUrl)}" alt="">` : "♪"}</span>
      <span class="work-copy"><strong>${escapeHTML(work.title)}</strong><span class="work-meta"><i>${escapeHTML(work.id)}</i><span>${work.tracks.length} 首</span>${work.hasSubtitles ? "<span>字幕</span>" : ""}</span></span>
    </button>`).join("");
    $$(".work-card").forEach(card => card.addEventListener("click", () => selectWork(card.dataset.folder)));
  }

  function selectWork(folder) {
    state.currentWork = state.works.find(work => work.folder === folder);
    state.currentTrack = -1;
    renderLibrary();
    renderTrackPanel();
  }

  function renderTrackPanel() {
    const work = state.currentWork;
    if (!work) return;
    $("#trackPanel").innerHTML = `<div class="selected-work">
      <span class="selected-cover">${work.coverUrl ? `<img src="${escapeHTML(work.coverUrl)}" alt="">` : "♪"}</span>
      <div><span class="chip">${escapeHTML(work.id)}</span><h2>${escapeHTML(work.title)}</h2><p>${escapeHTML(formatDate(work.date))} · ${work.hasSubtitles ? "包含字幕" : "无字幕标记"}</p></div>
    </div><div class="track-heading"><span>TRACKS</span><span>${work.tracks.length} 首</span></div>
    <ol class="tracks">${work.tracks.length ? work.tracks.map((track, index) => `<li><button class="track-row${index === state.currentTrack ? " active" : ""}" type="button" data-track="${index}"><span>${index === state.currentTrack ? "▶" : String(index + 1).padStart(2, "0")}</span><strong>${escapeHTML(track.name)}</strong><small>${escapeHTML(track.type)}</small></button></li>`).join("") : '<li class="empty-state"><p>没有发现可播放音频</p></li>'}</ol>`;
    $$(".track-row").forEach(button => button.addEventListener("click", () => playTrack(Number(button.dataset.track))));
  }

  function playTrack(index) {
    const work = state.currentWork;
    const track = work?.tracks[index];
    if (!track) return;
    state.currentTrack = index;
    state.playQueue = work.tracks.map(item => ({
      title: item.name,
      url: item.url,
      coverUrl: work.coverUrl
    }));
    state.playQueueIndex = index;
    state.playQueueContext = work.title;
    playQueuedTrack();
    renderTrackPanel();
  }

  function playQueuedTrack() {
    const track = state.playQueue[state.playQueueIndex];
    if (!track) return;
    const audio = $("#audioPlayer");
    audio.src = track.url;
    $("#playerBar").hidden = false;
    $("#nowTrack").textContent = track.title;
    $("#nowWork").textContent = state.playQueueContext;
    $("#nowCover").innerHTML = track.coverUrl ? `<img src="${escapeHTML(track.coverUrl)}" alt="">` : "♪";
    audio.play().catch(() => toast("请点击播放器的播放按钮"));
  }

  function playNext() {
    if (state.playQueueIndex + 1 >= state.playQueue.length) return;
    state.playQueueIndex += 1;
    if (state.currentWork && state.playQueueContext === state.currentWork.title) {
      state.currentTrack = state.playQueueIndex;
      renderTrackPanel();
    }
    playQueuedTrack();
  }

  function closePlayer() {
    $("#audioPlayer").pause();
    $("#audioPlayer").removeAttribute("src");
    $("#playerBar").hidden = true;
    state.playQueue = [];
    state.playQueueIndex = -1;
    state.playQueueContext = "";
  }

  async function search(event) {
    event.preventDefault();
    state.searchQuery = $("#searchInput").value.trim();
    state.searchPage = 1;
    await loadSearchPage();
  }

  async function loadSearchPage() {
    const section = state.siteTab;
    if (!["works", "popular", "random"].includes(section)) return;
    const query = section === "random" ? "" : state.searchQuery;
    state.selected.clear();
    updateSelection();
    const loadingLabel = section === "random" ? "正在随机挑选一部作品…" : query ? `正在搜索“${query}” · 第 ${state.searchPage} 页…` : `正在读取第 ${state.searchPage} 页…`;
    $("#searchSummary").textContent = loadingLabel;
    $("#searchGrid").innerHTML = Array(8).fill('<div class="skeleton"></div>').join("");
    $("#searchPagination").hidden = true;
    try {
      const result = await backend().BrowseSiteWorks(section, query, state.searchPage, state.searchPageSize);
      state.searchPage = result.page;
      state.searchTotalPages = result.totalPages;
      renderSearch(result.works || []);
      if (section === "random") {
        $("#searchSummary").textContent = "从站点作品库中随机选出";
      } else if (query) {
        $("#searchSummary").textContent = `找到 ${result.total} 个与“${query}”相关的作品`;
      } else {
        $("#searchSummary").textContent = `${result.total} 部作品 · 每页 ${result.pageSize} 部`;
      }
      renderSearchPagination(section !== "random");
    } catch (error) {
      renderError($("#searchGrid"), normalizeError(error), loadSearchPage);
      $("#searchSummary").textContent = "站点数据读取未完成";
    }
  }

  function renderSearchPagination(enabled = true) {
    const pagination = $("#searchPagination");
    pagination.hidden = !enabled || state.searchTotalPages <= 1;
    $("#searchPageLabel").textContent = `第 ${state.searchPage} / ${Math.max(1, state.searchTotalPages)} 页`;
    $("#searchPageInput").value = state.searchPage;
    $("#searchPageInput").max = Math.max(1, state.searchTotalPages);
    $("#searchPrev").disabled = state.searchPage <= 1;
    $("#searchNext").disabled = state.searchPage >= state.searchTotalPages;
  }

  function changeSearchPage(page) {
    if (!Number.isFinite(page)) return;
    const target = Math.min(Math.max(1, Math.trunc(page)), Math.max(1, state.searchTotalPages));
    if (target === state.searchPage) {
      $("#searchPageInput").value = state.searchPage;
      return;
    }
    state.searchPage = target;
    loadSearchPage();
    $("#siteWorksView").scrollIntoView({ behavior: "smooth", block: "start" });
  }

  async function changeSitePageSize() {
    const size = Number($("#sitePageSize").value);
    if (![20, 30, 40, 50].includes(size)) return;
    state.searchPageSize = size;
    state.searchPage = 1;
    $("#settingPageSize").value = String(size);
    if (state.settings) {
      state.settings.browsePageSize = size;
      backend().SaveSettings(state.settings).catch(error => toast(normalizeError(error), true));
    }
    await loadSearchPage();
  }

  async function activateSiteTab(tab, query = null) {
    state.siteTab = tab;
    $$(".site-tab").forEach(button => button.classList.toggle("active", button.dataset.siteTab === tab));
    $("#siteWorksView").hidden = !["works", "popular", "random"].includes(tab);
    $("#siteCatalogView").hidden = !["circles", "tags", "vas"].includes(tab);
    $("#siteDetailView").hidden = true;
    $("#siteLoginView").hidden = tab !== "login";
    $("#siteAboutView").hidden = tab !== "about";

    if (["circles", "tags", "vas"].includes(tab)) {
      state.catalogKind = tab;
      state.catalogQuery = "";
      state.catalogPage = 1;
      $("#catalogFilter").value = "";
      await loadCatalog();
      return;
    }
    if (tab === "login") {
      $("#siteLoginAccount").value = state.settings?.account || "guest";
      $("#siteLoginPassword").value = state.settings?.password || "";
      $("#loginCurrentAccount").textContent = state.settings?.account || "guest";
      return;
    }
    if (tab === "about") return;

    if (query !== null) {
      state.searchQuery = query;
      $("#searchInput").value = query;
    } else {
      state.searchQuery = "";
      $("#searchInput").value = "";
    }
    state.searchPage = 1;
    const sectionCopy = {
      works: ["ALL WORKS", "全部作品", "搜索标题、社团、标签、声优或 RJ 编号"],
      popular: ["POPULAR", "热门作品", "在热门作品中继续筛选"],
      random: ["SHUFFLE", "随机发现", ""]
    }[tab];
    $("#siteSectionEyebrow").textContent = sectionCopy[0];
    $("#siteSectionTitle").textContent = sectionCopy[1];
    $("#searchInput").placeholder = sectionCopy[2];
    $("#searchForm").hidden = tab === "random";
    $(".page-size-control").hidden = tab === "random";
    $("#shuffleAgain").hidden = tab !== "random";
    await loadSearchPage();
  }

  async function loadCatalog() {
    const kind = state.catalogKind;
    const labels = {
      circles: ["CIRCLES", "全部社团", "按作品数量排列的站点社团目录"],
      tags: ["TAGS", "全部标签", "浏览站点公开标签并进入对应作品"],
      vas: ["VOICE ACTORS", "全部声优", "浏览声优目录并查找相关作品"]
    }[kind];
    $("#catalogEyebrow").textContent = labels[0];
    $("#catalogTitle").textContent = labels[1];
    $("#catalogSummary").textContent = labels[2];
    $("#catalogGrid").innerHTML = Array(12).fill('<div class="catalog-skeleton"></div>').join("");
    $("#catalogPagination").hidden = true;
    if (!state.catalogs[kind]) {
      try {
        state.catalogs[kind] = await backend().GetSiteCatalog(kind);
      } catch (error) {
        renderError($("#catalogGrid"), normalizeError(error), loadCatalog);
        return;
      }
    }
    renderCatalog();
  }

  function renderCatalog() {
    const source = state.catalogs[state.catalogKind] || [];
    const query = state.catalogQuery.toLocaleLowerCase();
    const filtered = query ? source.filter(item => item.name.toLocaleLowerCase().includes(query)) : source;
    state.catalogTotalPages = Math.max(1, Math.ceil(filtered.length / state.catalogPageSize));
    state.catalogPage = Math.min(Math.max(1, state.catalogPage), state.catalogTotalPages);
    const start = (state.catalogPage - 1) * state.catalogPageSize;
    const items = filtered.slice(start, start + state.catalogPageSize);
    $("#catalogSummary").textContent = `${filtered.length} 项 · 点击名称查看相关作品`;
    if (!items.length) {
      $("#catalogGrid").innerHTML = '<div class="empty-state wide"><span>◌</span><h2>没有匹配目录项</h2><p>试试更短的筛选词。</p></div>';
    } else {
      $("#catalogGrid").innerHTML = items.map((item, index) => `<button class="catalog-card" type="button" data-facet="${escapeHTML(item.name)}">
        <span>${String(start + index + 1).padStart(2, "0")}</span><strong>${escapeHTML(item.name)}</strong><small>${compactNumber(item.count)} 部作品</small><i>→</i>
      </button>`).join("");
      $$(".catalog-card").forEach(card => card.addEventListener("click", () => activateSiteTab("works", card.dataset.facet)));
    }
    const pagination = $("#catalogPagination");
    pagination.hidden = state.catalogTotalPages <= 1;
    $("#catalogPageLabel").textContent = `第 ${state.catalogPage} / ${state.catalogTotalPages} 页`;
    $("#catalogPageInput").value = state.catalogPage;
    $("#catalogPageInput").max = state.catalogTotalPages;
    $("#catalogPrev").disabled = state.catalogPage <= 1;
    $("#catalogNext").disabled = state.catalogPage >= state.catalogTotalPages;
  }

  function changeCatalogPage(page) {
    if (!Number.isFinite(page)) return;
    const target = Math.min(Math.max(1, Math.trunc(page)), Math.max(1, state.catalogTotalPages));
    if (target === state.catalogPage) {
      $("#catalogPageInput").value = state.catalogPage;
      return;
    }
    state.catalogPage = target;
    renderCatalog();
    try {
      $("#siteCatalogView").scrollIntoView({ behavior: "smooth", block: "start" });
    } catch {}
  }

  async function siteLogin(event) {
    event.preventDefault();
    const account = $("#siteLoginAccount").value.trim();
    const password = $("#siteLoginPassword").value;
    const button = $("#siteLoginSubmit");
    button.disabled = true;
    button.textContent = "正在登录…";
    $("#siteLoginHint").textContent = "正在验证站点账号";
    try {
      const result = await backend().SiteLogin(account, password);
      state.settings.account = account;
      state.settings.password = password;
      fillSettings(state.settings);
      $("#loginCurrentAccount").textContent = result.name || account;
      $("#siteLoginHint").textContent = `已作为 ${result.name || account} 登录，后续 API 请求将使用此账号。`;
      $("#setupNotice").hidden = true;
      toast("站点账号已登录并保存");
    } catch (error) {
      $("#siteLoginHint").textContent = normalizeError(error);
      toast(normalizeError(error), true);
    } finally {
      button.disabled = false;
      button.textContent = "登录并保存";
    }
  }

  async function openWorkDetail(sourceId) {
    $("#siteWorksView").hidden = true;
    $("#siteCatalogView").hidden = true;
    $("#siteLoginView").hidden = true;
    $("#siteAboutView").hidden = true;
    $("#siteDetailView").hidden = false;
    $("#detailContent").innerHTML = '<div class="detail-loading"><div class="detail-cover-skeleton"></div><div><span></span><span></span><span></span></div></div>';
    $("#siteDetailView").scrollIntoView({ behavior: "smooth", block: "start" });
    try {
      state.detail = await backend().GetSiteWorkDetail(sourceId);
      renderWorkDetail();
    } catch (error) {
      renderError($("#detailContent"), normalizeError(error), () => openWorkDetail(sourceId));
    }
  }

  function closeWorkDetail() {
    $("#siteDetailView").hidden = true;
    $("#siteWorksView").hidden = !["works", "popular", "random"].includes(state.siteTab);
    $("#siteCatalogView").hidden = !["circles", "tags", "vas"].includes(state.siteTab);
    $("#siteLoginView").hidden = state.siteTab !== "login";
    $("#siteAboutView").hidden = state.siteTab !== "about";
  }

  function renderWorkDetail() {
    const detail = state.detail;
    if (!detail) return;
    state.detailPlayableTracks = [];
    const tracksHTML = renderDetailTracks(detail.tracks || []);
    $("#detailContent").innerHTML = `<section class="detail-hero">
      <div class="detail-cover">${detail.coverUrl ? `<img src="${escapeHTML(detail.coverUrl)}" alt="" referrerpolicy="no-referrer">` : "<span>♪</span>"}</div>
      <div class="detail-copy">
        <div class="detail-kicker"><span>${escapeHTML(detail.sourceId)}</span><span>${escapeHTML(detail.release || "日期未知")}</span></div>
        <h2>${escapeHTML(detail.title)}</h2>
        <p class="detail-circle">${escapeHTML(detail.circle || "未知社团")}</p>
        <div class="detail-stats"><span><strong>★ ${Number(detail.rating || 0).toFixed(1)}</strong><small>评分</small></span><span><strong>${compactNumber(detail.downloadCount)}</strong><small>下载</small></span><span><strong>${formatDuration(detail.duration)}</strong><small>时长</small></span><span><strong>${detail.playableCount || 0}</strong><small>可播放音轨</small></span></div>
        <div class="detail-tags">${(detail.tags || []).map(tag => `<span>${escapeHTML(tag)}</span>`).join("")}</div>
        <div class="detail-actions"><button id="detailDownload" class="button primary" type="button">下载这部作品</button>${detail.hasSubtitle ? '<span class="detail-subtitle">✓ 包含字幕</span>' : ""}</div>
      </div>
    </section>
    <section class="detail-body">
      <div class="online-tracks">
        <div class="detail-section-heading"><div><p>ONLINE PLAYER</p><h3>音轨与在线播放</h3></div><span>${detail.playableCount || 0} 首音频</span></div>
        <div class="detail-track-tree">${tracksHTML || '<div class="empty-state"><span>♪</span><h2>没有可播放音轨</h2></div>'}</div>
      </div>
      <aside class="detail-facts">
        <div><span>声优</span><p>${(detail.voiceActors || []).length ? detail.voiceActors.map(escapeHTML).join("、") : "暂无信息"}</p></div>
        <div><span>社团</span><p>${escapeHTML(detail.circle || "暂无信息")}</p></div>
        <div><span>作品编号</span><p>${escapeHTML(detail.sourceId)}</p></div>
        <div><span>发售日期</span><p>${escapeHTML(detail.release || "暂无信息")}</p></div>
      </aside>
    </section>`;
    $("#detailDownload").addEventListener("click", queueDetailDownload);
    $$("[data-online-track]").forEach(button => button.addEventListener("click", () => playOnlineTrack(Number(button.dataset.onlineTrack))));
  }

  function renderDetailTracks(tracks, depth = 0) {
    return tracks.map(track => {
      if (track.type === "folder") {
        return `<details class="detail-track-folder" ${depth < 1 ? "open" : ""}><summary><span>⌄</span><strong>${escapeHTML(track.title)}</strong><small>${countPlayableTracks(track.children || [])} 首</small></summary><div>${renderDetailTracks(track.children || [], depth + 1)}</div></details>`;
      }
      if (track.type === "audio" && track.streamUrl) {
        const index = state.detailPlayableTracks.length;
        state.detailPlayableTracks.push({ title: track.title, url: track.streamUrl, coverUrl: state.detail?.coverUrl || "" });
        return `<button class="online-track-row" type="button" data-online-track="${index}"><span>▶</span><strong>${escapeHTML(track.title)}</strong><small>${escapeHTML(fileType(track.title))}</small></button>`;
      }
      return `<div class="detail-file-row"><span>·</span><strong>${escapeHTML(track.title)}</strong><small>${escapeHTML(track.type || "file")}</small></div>`;
    }).join("");
  }

  function countPlayableTracks(tracks) {
    return tracks.reduce((count, track) => count + (track.type === "audio" && track.streamUrl ? 1 : 0) + countPlayableTracks(track.children || []), 0);
  }

  function playOnlineTrack(index) {
    if (!state.detailPlayableTracks[index]) return;
    state.currentWork = null;
    state.currentTrack = -1;
    state.playQueue = state.detailPlayableTracks;
    state.playQueueIndex = index;
    state.playQueueContext = state.detail?.title || "在线作品";
    playQueuedTrack();
  }

  async function queueDetailDownload() {
    const detail = state.detail;
    if (!detail) return;
    const button = $("#detailDownload");
    button.disabled = true;
    button.textContent = "正在加入…";
    try {
      await backend().QueueDownloadItems([{
        sourceId: detail.sourceId,
        title: detail.title,
        coverUrl: detail.coverUrl,
        circle: detail.circle
      }]);
      toast(`${detail.sourceId} 已加入下载`);
      await loadTasks();
    } catch (error) {
      toast(normalizeError(error), true);
    } finally {
      button.disabled = false;
      button.textContent = "下载这部作品";
    }
  }

  function renderSearch(works) {
    state.searchWorks = works;
    if (!works.length) {
      $("#searchGrid").innerHTML = '<div class="empty-state wide"><span>◌</span><h2>没有匹配作品</h2><p>试试更短或不同的关键词。</p></div>';
      return;
    }
    $("#searchGrid").innerHTML = works.map(work => `<article class="search-card" data-id="${escapeHTML(work.sourceId)}">
      <button class="check-mark" type="button" data-select-work="${escapeHTML(work.sourceId)}" aria-label="选择 ${escapeHTML(work.sourceId)}" aria-pressed="false">✓</button>
      <button class="search-card-open" type="button" data-open-work="${escapeHTML(work.sourceId)}"><span class="search-cover">${work.coverUrl ? `<img src="${escapeHTML(work.coverUrl)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : "♪"}</span>
      <span class="search-copy"><span class="search-kicker"><i>${escapeHTML(work.sourceId)}</i><span>${escapeHTML(work.release || "日期未知")}</span></span><strong>${escapeHTML(work.title)}</strong>
      <span class="search-circle">${escapeHTML(work.circle || "未知社团")}</span>
      <span class="search-tags">${(work.tags || []).slice(0, 3).map(tag => `<em>${escapeHTML(tag)}</em>`).join("")}</span>
      <span class="search-meta"><span>★ ${Number(work.rating || 0).toFixed(1)}</span><span>${compactNumber(work.downloadCount)} DL</span><span>${formatDuration(work.duration)}</span>${work.hasSubtitle ? "<b>字幕</b>" : ""}</span></span></button>
    </article>`).join("");
    $$("[data-select-work]").forEach(button => button.addEventListener("click", () => toggleSelection(button.closest(".search-card"))));
    $$("[data-open-work]").forEach(button => button.addEventListener("click", () => openWorkDetail(button.dataset.openWork)));
  }

  function toggleSelection(card) {
    const id = card.dataset.id;
    state.selected.has(id) ? state.selected.delete(id) : state.selected.add(id);
    card.classList.toggle("selected", state.selected.has(id));
    const button = card.querySelector("[data-select-work]");
    button.setAttribute("aria-pressed", String(state.selected.has(id)));
    updateSelection();
  }

  function updateSelection() {
    $("#selectedSummary").textContent = `已选 ${state.selected.size} 项`;
    $("#queueSelected").disabled = state.selected.size === 0;
  }

  async function queueSelected() {
    const ids = [...state.selected];
    const requests = ids.map(id => {
      const work = state.searchWorks.find(item => item.sourceId === id);
      return {
        sourceId: id,
        title: work?.title || "",
        coverUrl: work?.coverUrl || "",
        circle: work?.circle || ""
      };
    });
    const button = $("#queueSelected");
    button.disabled = true;
    button.textContent = "正在加入…";
    try {
      await backend().QueueDownloadItems(requests);
      state.selected.clear();
      updateSelection();
      toast(`${ids.length} 部作品已加入下载`);
      await loadTasks();
      showPage("tasks");
    } catch (error) {
      toast(normalizeError(error), true);
    } finally {
      button.textContent = "加入下载";
      updateSelection();
    }
  }

  async function loadTasks() {
    try { renderTasks(await backend().GetTasks()); }
    catch (error) { toast(normalizeError(error), true); }
  }

  function renderTasks(tasks = []) {
    state.tasks = tasks;
    const availableIDs = new Set(tasks.map(task => task.id));
    state.taskSelected = new Set([...state.taskSelected].filter(id => availableIDs.has(id)));
    const running = tasks.filter(task => ["queued", "running", "retrying"].includes(task.status));
    $("#metricRunning").textContent = running.length;
    $("#metricComplete").textContent = tasks.filter(task => task.status === "completed").length;
    $("#metricFailed").textContent = tasks.filter(task => ["failed", "cancelled"].includes(task.status)).length;
    $("#taskBadge").hidden = running.length === 0;
    $("#taskBadge").textContent = running.length;
    $("#taskBulkToolbar").hidden = tasks.length === 0;
    if (!tasks.length) {
      state.taskSelected.clear();
      $("#taskList").innerHTML = '<div class="empty-state wide"><span>↓</span><h2>还没有下载任务</h2><p>从“站点浏览”页面选择作品开始下载。</p></div>';
      updateTaskSelectionUI();
      return;
    }
    const labels = { queued: "等待中", running: "下载中", retrying: "自动续传", completed: "已完成", failed: "失败", cancelled: "已取消" };
    $("#taskList").innerHTML = tasks.map(task => {
      const active = ["queued", "running", "retrying"].includes(task.status);
      const retryable = ["failed", "cancelled"].includes(task.status);
      const selected = state.taskSelected.has(task.id);
      const fileFraction = task.totalBytesKnown && task.totalBytes > 0
        ? Math.min(1, task.downloadedBytes / task.totalBytes)
        : (task.filesTotal > 0 ? Math.min(1, task.filesCompleted / task.filesTotal) : 0);
      const progress = task.status === "completed"
        ? 100
        : (task.total ? Math.round(Math.min(1, (task.completed + fileFraction) / task.total) * 100) : 0);
      const waitingForPlan = active && !task.filesTotal;
      const items = task.items || [];
      const primary = items[task.current] || items.find(item => item.status === "running") || items[0] || {};
      const title = primary.title || task.label || primary.sourceId || "下载任务";
      const sourceID = primary.sourceId || "";
      const speedText = task.status === "retrying"
        ? `约 ${Math.max(1, Number(task.retryWaitSeconds) || 1)} 秒后重连`
        : (active ? formatSpeed(task.speedBytesPerSecond) : "—");
      const transferStats = task.filesTotal ? `<div class="task-transfer-stats">
        <span><b>传输</b>${formatBytes(task.downloadedBytes)}${task.totalBytesKnown ? ` / ${formatBytes(task.totalBytes)}` : " / 总大小计算中"}</span>
        <span><b>速度</b>${speedText}</span>
        <span><b>文件</b>${task.filesCompleted} / ${task.filesTotal}</span>
        ${task.currentFile ? `<span class="task-current-file" title="${escapeHTML(task.currentFile)}"><b>当前</b>${escapeHTML(task.currentFile)}</span>` : ""}
      </div>` : (waitingForPlan ? '<div class="task-transfer-stats loading"><span><b>准备</b>正在读取文件清单与大小…</span></div>' : "");
      const detailRows = items.length > 1 ? `<details class="task-items">
        <summary>查看全部 ${items.length} 部作品 <span>${task.completed} / ${task.total}</span></summary>
        <div>${items.map(item => `<div class="task-item-row">
          <span class="task-item-cover">${item.coverUrl ? `<img src="${escapeHTML(item.coverUrl)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : "♪"}</span>
          <span><strong>${escapeHTML(item.title || item.sourceId)}</strong><small>${escapeHTML(item.sourceId)}${item.circle ? ` · ${escapeHTML(item.circle)}` : ""}${item.error ? ` · ${escapeHTML(item.error)}` : ""}</small></span>
          <em class="${escapeHTML(item.status)}">${labels[item.status] || item.status}</em>
        </div>`).join("")}</div>
      </details>` : "";
      return `<article class="task-card ${escapeHTML(task.status)}${selected ? " selected" : ""}" data-task-card="${escapeHTML(task.id)}">
        <label class="task-select" title="选择任务"><input type="checkbox" data-task-select="${escapeHTML(task.id)}" aria-label="选择 ${escapeHTML(title)}"${selected ? " checked" : ""}><span></span></label>
        <span class="task-thumb">${primary.coverUrl ? `<img src="${escapeHTML(primary.coverUrl)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : "<b>♪</b>"}</span>
        <div class="task-copy">
          <div class="task-title-row"><span>${escapeHTML(sourceID || `${task.total} 部作品`)}</span><strong>${escapeHTML(title)}</strong></div>
          <div class="task-meta"><span>${escapeHTML(primary.circle || "站点作品")}</span><span>${formatTaskTime(task.createdAt)}</span><span>${task.completed} / ${task.total} 部</span></div>
          <div class="task-progress-row"><div class="progress${waitingForPlan ? " indeterminate" : ""}" role="progressbar" aria-label="${escapeHTML(title)} 下载进度" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${progress}"><i style="width:${progress}%"></i></div><b>${waitingForPlan ? "准备中" : `${progress}%`}</b></div>
          ${transferStats}
          ${task.retryMessage ? `<p class="task-retry-notice"><strong>自动续传 · 第 ${task.autoRetryCount || 1} 次</strong><span>${escapeHTML(task.retryMessage)}，无需手动操作。</span></p>` : ""}
          ${task.error ? `<p class="task-error"><strong>下载未完成</strong><span>${escapeHTML(task.error)}</span></p>` : ""}
          ${detailRows}
        </div>
        <div class="task-actions"><span class="task-status ${escapeHTML(task.status)}">${labels[task.status] || task.status}</span>
          <div class="task-action-buttons">
            ${retryable ? `<button class="task-action retry" type="button" data-retry="${escapeHTML(task.id)}">重试</button>` : ""}
            ${active ? `<button class="task-action" type="button" data-cancel="${escapeHTML(task.id)}">取消</button>` : ""}
            <button class="task-action delete" type="button" data-delete="${escapeHTML(task.id)}">删除</button>
          </div>
        </div>
      </article>`;
    }).join("");
    $$("[data-task-select]").forEach(input => input.addEventListener("change", () => {
      if (input.checked) state.taskSelected.add(input.dataset.taskSelect);
      else state.taskSelected.delete(input.dataset.taskSelect);
      updateTaskSelectionUI();
    }));
    $$("[data-retry]").forEach(button => button.addEventListener("click", () => retryTask(button.dataset.retry, button)));
    $$("[data-cancel]").forEach(button => button.addEventListener("click", () => cancelTask(button.dataset.cancel, button)));
    $$("[data-delete]").forEach(button => button.addEventListener("click", () => deleteTask(button.dataset.delete)));
    updateTaskSelectionUI();
    if (tasks.some(task => task.status === "completed")) loadLibrary();
  }

  function toggleAllTasks(event) {
    state.taskSelected.clear();
    if (event.target.checked) state.tasks.forEach(task => state.taskSelected.add(task.id));
    updateTaskSelectionUI();
  }

  function updateTaskSelectionUI() {
    const selectedTasks = state.tasks.filter(task => state.taskSelected.has(task.id));
    const retryable = selectedTasks.filter(task => ["failed", "cancelled"].includes(task.status));
    const active = selectedTasks.filter(task => ["queued", "running", "retrying"].includes(task.status));
    const selectAll = $("#taskSelectAll");
    selectAll.checked = state.tasks.length > 0 && selectedTasks.length === state.tasks.length;
    selectAll.indeterminate = selectedTasks.length > 0 && selectedTasks.length < state.tasks.length;
    $("#taskSelectedSummary").textContent = `已选 ${selectedTasks.length} 项`;
    $("#retrySelectedTasks").disabled = retryable.length === 0;
    $("#cancelSelectedTasks").disabled = active.length === 0;
    $("#deleteSelectedTasks").disabled = selectedTasks.length === 0;
    $$("[data-task-select]").forEach(input => { input.checked = state.taskSelected.has(input.dataset.taskSelect); });
    $$("[data-task-card]").forEach(card => card.classList.toggle("selected", state.taskSelected.has(card.dataset.taskCard)));
  }

  async function retryTask(id, button) {
    button.disabled = true;
    button.textContent = "重试中…";
    try {
      await backend().RetryTask(id);
      state.taskSelected.delete(id);
      toast("任务已重新开始，将从保留的临时文件继续");
      await loadTasks();
    } catch (error) {
      toast(normalizeError(error), true);
      button.disabled = false;
      button.textContent = "重试";
    }
  }

  async function retrySelectedTasks() {
    const ids = state.tasks.filter(task => state.taskSelected.has(task.id) && ["failed", "cancelled"].includes(task.status)).map(task => task.id);
    if (!ids.length) return;
    const button = $("#retrySelectedTasks");
    button.disabled = true;
    button.textContent = "正在重试…";
    try {
      const count = await backend().RetryTasks(ids);
      ids.forEach(id => state.taskSelected.delete(id));
      toast(`${count} 个任务已重新开始`);
      await loadTasks();
    } catch (error) {
      toast(normalizeError(error), true);
    } finally {
      button.textContent = "重试失败项";
      updateTaskSelectionUI();
    }
  }

  async function cancelTask(id, button) {
    button.disabled = true;
    button.textContent = "取消中…";
    try {
      await backend().CancelTask(id);
      toast("正在取消任务");
    } catch (error) {
      toast(normalizeError(error), true);
      button.disabled = false;
      button.textContent = "取消";
    }
  }

  async function cancelSelectedTasks() {
    const ids = state.tasks.filter(task => state.taskSelected.has(task.id) && ["queued", "running", "retrying"].includes(task.status)).map(task => task.id);
    if (!ids.length) return;
    const count = await backend().CancelTasks(ids);
    ids.forEach(id => state.taskSelected.delete(id));
    toast(`正在取消 ${count} 个任务`);
    updateTaskSelectionUI();
  }

  async function deleteTask(id) {
    const task = state.tasks.find(item => item.id === id);
    if (!task || !await confirmTaskDeletion([task])) return;
    try {
      await backend().DeleteTask(id);
      state.taskSelected.delete(id);
      toast("任务记录已删除");
      await loadTasks();
    } catch (error) {
      toast(normalizeError(error), true);
    }
  }

  async function deleteSelectedTasks() {
    const tasks = state.tasks.filter(task => state.taskSelected.has(task.id));
    if (!tasks.length || !await confirmTaskDeletion(tasks)) return;
    const button = $("#deleteSelectedTasks");
    button.disabled = true;
    button.textContent = "正在删除…";
    try {
      const count = await backend().DeleteTasks(tasks.map(task => task.id));
      tasks.forEach(task => state.taskSelected.delete(task.id));
      toast(`已删除 ${count} 条任务记录`);
      await loadTasks();
    } catch (error) {
      toast(normalizeError(error), true);
    } finally {
      button.textContent = "删除任务";
      updateTaskSelectionUI();
    }
  }

  function confirmTaskDeletion(tasks) {
    const dialog = $("#taskDeleteDialog");
    const activeCount = tasks.filter(task => ["queued", "running", "retrying"].includes(task.status)).length;
    $("#taskDeleteTitle").textContent = tasks.length === 1 ? "删除这个下载任务？" : `删除选中的 ${tasks.length} 个任务？`;
    $("#taskDeleteDescription").textContent = activeCount
      ? `其中 ${activeCount} 个任务仍在运行，删除记录时会先取消。已经下载的文件不会被删除。`
      : "只会删除任务记录，已经下载的文件不会被删除。";
    dialog.returnValue = "";
    dialog.showModal();
    return new Promise(resolve => dialog.addEventListener("close", () => resolve(dialog.returnValue === "delete"), { once: true }));
  }

  function formatTaskTime(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "刚刚";
    return date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
  }

  function formatBytes(value) {
    const bytes = Math.max(0, Number(value) || 0);
    const units = ["B", "KB", "MB", "GB", "TB"];
    let size = bytes;
    let unit = 0;
    while (size >= 1024 && unit < units.length - 1) {
      size /= 1024;
      unit++;
    }
    const digits = unit === 0 ? 0 : (size >= 100 ? 0 : size >= 10 ? 1 : 2);
    return `${size.toFixed(digits)} ${units[unit]}`;
  }

  function formatSpeed(value) {
    const speed = Number(value) || 0;
    return speed > 0 ? `${formatBytes(speed)}/s` : "正在测速…";
  }

  function fillSettings(settings) {
    $("#settingAccount").value = settings.account || "guest";
    $("#settingPassword").value = settings.password || "guest";
    $("#settingApi").value = settings.apiUrl || "";
    $("#settingProxy").value = settings.proxyUrl || "";
    $("#settingDirectory").value = settings.downloadDirectory || "./syncdata";
    $("#settingWorkers").value = settings.maxWorkers || 3;
    $("#settingRetries").value = settings.maxRetries ?? 3;
    $("#settingQps").value = settings.downloadQps || 0.2;
    $("#settingMedia").value = settings.preferMedia || "all";
    $("#settingFolderStyle").value = settings.folderNameStyle || "full";
    $("#settingSanitize").checked = settings.sanitizeFilename !== false;
    const pageSize = [20, 30, 40, 50].includes(Number(settings.browsePageSize)) ? Number(settings.browsePageSize) : 20;
    state.searchPageSize = pageSize;
    $("#settingPageSize").value = String(pageSize);
    $("#sitePageSize").value = String(pageSize);
  }

  async function chooseDirectory() {
    try {
      const path = await backend().ChooseDownloadDirectory();
      if (path) $("#settingDirectory").value = path;
    } catch (error) { toast(normalizeError(error), true); }
  }

  async function saveSettings(event) {
    event.preventDefault();
    const settings = {
      account: $("#settingAccount").value.trim(),
      password: $("#settingPassword").value,
      apiUrl: $("#settingApi").value.trim(),
      proxyUrl: $("#settingProxy").value.trim(),
      downloadDirectory: $("#settingDirectory").value.trim(),
      maxWorkers: Number($("#settingWorkers").value),
      maxRetries: Number($("#settingRetries").value),
      preferMedia: $("#settingMedia").value,
      folderNameStyle: $("#settingFolderStyle").value,
      sanitizeFilename: $("#settingSanitize").checked,
      browsePageSize: Number($("#settingPageSize").value),
      downloadQps: Number($("#settingQps").value)
    };
    $("#saveSettings").disabled = true;
    $("#saveHint").textContent = "正在保存…";
    try {
      await backend().SaveSettings(settings);
      state.settings = settings;
      state.searchPageSize = settings.browsePageSize;
      $("#sitePageSize").value = String(settings.browsePageSize);
      $("#loginCurrentAccount").textContent = settings.account;
      $("#setupNotice").hidden = true;
      $("#saveHint").textContent = "设置已保存";
      toast("设置已保存");
      await loadLibrary();
    } catch (error) {
      $("#saveHint").textContent = "";
      toast(normalizeError(error), true);
    } finally {
      $("#saveSettings").disabled = false;
      setTimeout(() => { $("#saveHint").textContent = ""; }, 2500);
    }
  }

  function renderError(container, message, retry) {
    container.innerHTML = `<div class="empty-state wide"><span>!</span><h2>出现问题</h2><p>${escapeHTML(message)}</p><button class="button secondary" type="button">重试</button></div>`;
    container.querySelector("button").addEventListener("click", retry);
  }

  function toast(message, error = false) {
    const node = document.createElement("div");
    node.className = `toast${error ? " error" : ""}`;
    node.textContent = message;
    $("#toastRegion").appendChild(node);
    setTimeout(() => node.remove(), 3800);
  }

  async function call(action) {
    try { await action(); } catch (error) { toast(normalizeError(error), true); }
  }
  function normalizeError(error) { return String(error?.message || error || "未知错误").replace(/^Error:\s*/, ""); }
  function formatDate(value) { return /^\d{8}$/.test(value || "") ? `${value.slice(0, 4)}-${value.slice(4, 6)}-${value.slice(6)}` : (value || "日期未知"); }
  function formatDuration(seconds) {
    const value = Number(seconds || 0);
    if (!value) return "时长未知";
    if (value >= 3600) return `${(value / 3600).toFixed(value >= 36000 ? 0 : 1)}h`;
    return `${Math.max(1, Math.round(value / 60))}m`;
  }
  function fileType(name) {
    const match = String(name || "").match(/\.([a-z0-9]+)$/i);
    return match ? match[1].toUpperCase() : "AUDIO";
  }
  function debounce(fn, delay) { let timer; return (...args) => { clearTimeout(timer); timer = setTimeout(() => fn(...args), delay); }; }
})();
