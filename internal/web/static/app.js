(() => {
  const param = new URLSearchParams(window.location.search).get("scoutTheme");
  const theme = param || (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
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
