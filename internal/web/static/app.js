(() => {
  const param = new URLSearchParams(window.location.search).get("scoutTheme");
  const theme = param === "light" || param === "dark"
    ? param
    : (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  document.documentElement.setAttribute("data-theme", theme);
})();

document.addEventListener("alpine:init", () => {
  Alpine.data("paperlens", () => ({
    dragging: false,
    uploadError: "",
    toggleTheme() {
      document.documentElement.dataset.theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    },
    get dropClass() { return this.dragging ? "dragging" : ""; },
    dragOver() { this.dragging = true; },
    dragLeave() { this.dragging = false; },
    dropFile(event) {
      this.dragging = false;
      const input = document.getElementById("document-file");
      if (event.dataTransfer.files.length === 1) {
        input.files = event.dataTransfer.files;
        this.uploadError = "";
      } else {
        this.uploadError = "Drop one document at a time.";
      }
    }
  }));
});
document.addEventListener("htmx:error", () => {
  const region = document.getElementById("upload-result") || document.getElementById("results");
  if (region) {
    region.textContent = "The request could not finish. Check your connection. If you were uploading, check Paperless before trying again.";
    region.setAttribute("role", "alert");
  }
});
document.addEventListener("htmx:after:request", event => {
  const response = event.detail?.ctx?.response;
  if (response && response.status >= 400) {
    event.preventDefault();
    const region = document.getElementById("upload-result") || document.getElementById("results");
    if (region) {
      region.textContent = `Request failed (${response.status}). Reload the page before trying again. An upload may already have been accepted; check Paperless first.`;
      region.setAttribute("role", "alert");
    }
  }
});

(() => {
  const pending = new Map();
  let currentResults;

  function setExpanded(card, expanded) {
    card.toggleAttribute("data-preview-open", expanded);
    const title = card.querySelector("h3 [data-inline-preview]");
    title.setAttribute("aria-controls", card.querySelector(".inline-preview").id);
    title.setAttribute("aria-expanded", String(expanded));
    title.setAttribute("aria-label", `${expanded ? "Hide preview of" : "Preview"} ${card.dataset.documentTitle}`);
  }

  function enhanceResults() {
    const results = document.getElementById("results");
    if (results === currentResults) return;
    currentResults = results;
    if (!results) return;
    results.querySelectorAll(".result-card").forEach(card => setExpanded(card, false));
    const status = document.getElementById("search-status");
    const message = results.dataset.searchMessage;
    if (status && status.textContent !== message) status.textContent = message;
    document.dispatchEvent(new CustomEvent("paperlens:results", { detail: results }));
  }

  function closePreview(card) {
    pending.get(card)?.abort();
    pending.delete(card);
    const region = card.querySelector(".inline-preview");
    if (region.contains(document.activeElement)) {
      card.querySelector("h3 [data-inline-preview]").focus();
    }
    setExpanded(card, false);
    region.hidden = true;
    region.querySelector(".viewer-content").replaceChildren();
  }

  async function openPreview(card, url) {
    const controller = new AbortController();
    pending.set(card, controller);
    const region = card.querySelector(".inline-preview");
    const content = region.querySelector(".viewer-content");
    const loading = document.createElement("p");
    loading.className = "viewer-loading";
    loading.setAttribute("role", "status");
    loading.textContent = "Loading document preview...";
    content.replaceChildren(loading);
    setExpanded(card, true);
    region.hidden = false;
    try {
      const response = await fetch(url, {
        headers: { "X-Paperlens-Preview": "inline", Accept: "text/html" },
        credentials: "same-origin",
        signal: controller.signal
      });
      if (!response.ok) throw new Error(`Preview could not load (HTTP ${response.status}).`);
      if (!response.headers.get("Content-Type")?.startsWith("text/html")) {
        throw new Error("The server returned an unexpected preview format.");
      }
      const html = await response.text();
      if (controller.signal.aborted || pending.get(card) !== controller || !card.isConnected) return;
      // Only escaped, server-rendered viewer markup from the canonical document route.
      content.innerHTML = html;
    } catch (error) {
      if (controller.signal.aborted || !card.isConnected) return;
      const notice = document.createElement("p");
      notice.className = "notice error";
      notice.setAttribute("role", "alert");
      const message = error instanceof TypeError ? "Preview could not load. Check your connection." : error.message;
      notice.textContent = `${message} Close and reopen the preview to retry, or use the document links.`;
      content.replaceChildren(notice);
    } finally {
      if (pending.get(card) === controller) pending.delete(card);
    }
  }

  document.addEventListener("click", event => {
    if (!(event.target instanceof Element) || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const control = event.target.closest("[data-inline-preview], [data-close-preview]");
    if (!control) return;
    const card = control.closest(".result-card");
    event.preventDefault();
    if (card.hasAttribute("data-preview-open")) {
      closePreview(card);
    } else if (control.matches("[data-inline-preview]")) {
      openPreview(card, control.href);
    }
  });

  document.addEventListener("load", event => {
    if (event.target instanceof HTMLImageElement && event.target.matches("[data-thumbnail]")) {
      event.target.closest(".result-thumbnail").classList.add("thumbnail-loaded");
    }
  }, true);

  document.addEventListener("error", event => {
    const image = event.target;
    if (!(image instanceof HTMLImageElement)) return;
    if (image.matches("[data-thumbnail]")) {
      image.hidden = true;
      const link = image.closest(".result-thumbnail");
      link.classList.remove("thumbnail-loaded");
      link.querySelector("[data-thumbnail-status]").textContent = "Preview unavailable";
    } else if (image.matches("[data-document-image]")) {
      image.hidden = true;
      image.nextElementSibling.hidden = false;
    }
  }, true);

  document.addEventListener("DOMContentLoaded", () => {
    enhanceResults();
    new MutationObserver(() => {
      for (const [card, controller] of pending) {
        if (!card.isConnected) {
          controller.abort();
          pending.delete(card);
        }
      }
      enhanceResults();
    }).observe(document.body, { childList: true, subtree: true });
  });
})();

// Reloadable search URLs and private recent-search history.
(() => {
  const STORAGE_KEY = "paperlens.recentSearches.v1";
  const MAX_ITEMS = 10;
  const PARAMS = ["after", "before", "correspondent", "field", "mode", "q", "tag", "type", "value"];
  const STATUSES = ["pending", "none", "complete", "partial", "unavailable"];
  let replaceURL = null;
  let shownSearch = window.location.pathname + window.location.search;

  // Browser storage is untrusted: only rebuild same-origin home URLs from known parameters.
  function normalizeURL(raw) {
    if (typeof raw !== "string" || raw.length > 4096 || !raw.startsWith("/?")) return null;
    let parsed;
    try {
      parsed = new URL(raw, window.location.origin);
    } catch {
      return null;
    }
    if (parsed.origin !== window.location.origin || parsed.pathname !== "/") return null;
    const clean = new URLSearchParams();
    for (const key of PARAMS) {
      const value = parsed.searchParams.get(key);
      if (value !== null && value !== "" && value.length <= 500) clean.set(key, value);
    }
    const query = clean.get("q")?.trim();
    if (!query) return null;
    return "/?" + clean.toString();
  }

  function sanitize(item) {
    if (!item || typeof item !== "object") return null;
    const url = normalizeURL(item.url);
    if (!url) return null;
    const at = Number(item.at);
    return {
      url,
      query: new URLSearchParams(url.slice(1)).get("q").slice(0, 500),
      keyword: new URLSearchParams(url.slice(1)).get("mode") === "keyword",
      at: Number.isFinite(at) && at > 0 ? at : 0,
      jobId: typeof item.jobId === "string" && /^[A-Za-z0-9]{1,64}$/.test(item.jobId) ? item.jobId : "",
      credits: typeof item.credits === "string" ? item.credits.slice(0, 120) : "",
      status: STATUSES.includes(item.status) ? item.status : "unavailable"
    };
  }

  function storage() {
    try {
      return window.localStorage;
    } catch {
      return null;
    }
  }

  function load() {
    const store = storage();
    if (!store) return { items: [], ok: false };
    try {
      const data = JSON.parse(store.getItem(STORAGE_KEY) || "null");
      if (!data || data.v !== 1 || !Array.isArray(data.items)) return { items: [], ok: true };
      const seen = new Set();
      const items = [];
      for (const raw of data.items.slice(0, MAX_ITEMS * 2)) {
        const item = sanitize(raw);
        if (item && !seen.has(item.url)) {
          seen.add(item.url);
          items.push(item);
        }
        if (items.length === MAX_ITEMS) break;
      }
      return { items, ok: true };
    } catch {
      return { items: [], ok: false };
    }
  }

  function save(items) {
    const store = storage();
    if (!store) return false;
    try {
      store.setItem(STORAGE_KEY, JSON.stringify({ v: 1, items }));
      return true;
    } catch {
      return false;
    }
  }

  function formatTime(at) {
    if (!at) return "";
    try {
      return new Date(at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
    } catch {
      return new Date(at).toISOString();
    }
  }

  function render(items, ok) {
    const panel = document.querySelector("[data-recent]");
    if (!panel) return;
    panel.hidden = false;
    const list = panel.querySelector("[data-recent-list]");
    const rows = items.map(item => {
      const li = document.createElement("li");
      const link = document.createElement("a");
      link.href = item.url;
      link.className = "recent-link";
      const query = document.createElement("span");
      query.className = "recent-query";
      query.textContent = item.query;
      const meta = document.createElement("span");
      meta.className = "recent-meta";
      const mode = document.createElement("span");
      mode.textContent = item.keyword ? "Keyword" : "AI";
      const time = document.createElement("time");
      if (item.at) time.dateTime = new Date(item.at).toISOString();
      time.textContent = formatTime(item.at);
      const credits = document.createElement("span");
      credits.className = `recent-credits credits-${item.status}`;
      credits.textContent = item.credits || "AI credits not reported yet";
      meta.append(mode, time, credits);
      link.append(query, meta);
      li.append(link);
      return li;
    });
    list.replaceChildren(...rows);
    panel.querySelector("[data-recent-empty]").hidden = items.length > 0;
    panel.querySelector("[data-recent-clear]").disabled = items.length === 0;
    const error = panel.querySelector("[data-recent-error]");
    error.hidden = ok;
    error.textContent = ok ? "" : "Recent searches can't be saved in this browser (storage is blocked or full). Searching still works.";
  }

  function update(mutate) {
    const state = load();
    const items = mutate(state.items).slice(0, MAX_ITEMS);
    const saved = state.ok && save(items);
    render(items, saved);
  }

  function withPresentation(url) {
    const theme = new URLSearchParams(window.location.search).get("scoutTheme");
    return theme ? `${url}&scoutTheme=${encodeURIComponent(theme)}` : url;
  }

  document.addEventListener("paperlens:results", event => {
    const results = event.detail;
    const url = normalizeURL(results.dataset.searchUrl);
    const jobId = results.dataset.jobId;
    if (!url || !jobId) return;
    const entry = {
      url, jobId,
      at: Date.now(),
      credits: results.dataset.credits || "",
      status: STATUSES.includes(results.dataset.creditsStatus) ? results.dataset.creditsStatus : "pending"
    };
    if (results.dataset.searchAccepted === "true") {
      const target = withPresentation(url);
      const replace = replaceURL === url;
      replaceURL = null;
      if (replace) {
        history.replaceState(null, "", target);
      } else if (window.location.pathname + window.location.search !== target) {
        history.pushState(null, "", target);
      }
      shownSearch = window.location.pathname + window.location.search;
      const panel = document.querySelector("[data-recent]");
      if (panel) panel.open = false;
      update(items => [sanitize(entry), ...items.filter(item => item.url !== url)]);
      return;
    }
    // Late or paginated responses only update the run they belong to.
    update(items => items.map(item => item.jobId === jobId
      ? { ...item, credits: entry.credits, status: entry.status }
      : item));
  });

  document.addEventListener("click", event => {
    if (!(event.target instanceof Element) || !event.target.closest("[data-recent-clear]")) return;
    const store = storage();
    let ok = Boolean(store);
    try {
      store?.removeItem(STORAGE_KEY);
    } catch {
      ok = false;
    }
    render([], ok);
  });

  // Back/Forward restores the form without unexpectedly starting a new run.
  window.addEventListener("popstate", () => {
    const current = window.location.pathname + window.location.search;
    if (window.location.pathname === "/" && current !== shownSearch) {
      history.replaceState({ suppressAutorun: true }, "", current);
      window.location.reload();
    }
  });

  document.addEventListener("DOMContentLoaded", () => {
    const state = load();
    render(state.items, state.ok);
    const restored = history.state?.suppressAutorun === true;
    if (restored) {
      history.replaceState(null, "", window.location.pathname + window.location.search);
      const notice = document.querySelector("[data-history-restored]");
      if (notice) notice.hidden = false;
    }
    const form = document.getElementById("search-form");
    if (!restored && form?.hasAttribute("data-autorun")) {
      const panel = document.querySelector("[data-recent]");
      if (panel) panel.open = false;
      // Let HTMX finish initializing so the rerun uses the same POST flow.
      setTimeout(() => {
        replaceURL = normalizeURL(window.location.pathname + window.location.search);
        form.requestSubmit();
      });
    }
  });
})();
