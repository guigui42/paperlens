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
document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll("[data-query]").forEach(button => {
    button.addEventListener("click", () => {
      const input = document.getElementById("query");
      input.value = button.dataset.query;
      input.focus();
    });
  });
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
