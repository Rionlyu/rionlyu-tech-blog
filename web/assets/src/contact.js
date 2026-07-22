import htmx from "htmx.org";
import { celebrate, cleanupCelebration } from "./duck.js";

let initialized = false;
let lastSuccess = "";

function init() {
  if (initialized) return;
  initialized = true;
  window.htmx = htmx;

  document.body.addEventListener("htmx:beforeSwap", (event) => {
    const response = event.detail.xhr;
    if (response.getResponseHeader("X-Contact-Fragment") === "1" && response.status >= 400) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  });

  document.body.addEventListener("htmx:afterSwap", () => {
    const success = document.querySelector("[data-contact-success]");
    if (success) {
      triggerSuccess(success);
      success.focus({ preventScroll: true });
      return;
    }
    const invalid = document.querySelector("#contact-form-region [aria-invalid='true']");
    (invalid ?? document.querySelector("#contact-form-region"))?.focus({ preventScroll: false });
  });

  document.body.addEventListener("htmx:beforeHistorySave", cleanupCelebration);
  window.addEventListener("pagehide", cleanupCelebration);
  const initialSuccess = document.querySelector("[data-contact-success]");
  if (initialSuccess) triggerSuccess(initialSuccess);
}

function triggerSuccess(element) {
  const id = element.dataset.contactSuccess;
  if (!id || id === lastSuccess || wasSeen(id)) return;
  lastSuccess = id;
  remember(id);
  const page = element.closest("[data-duck-icon]") ?? document.querySelector("[data-duck-icon]");
  const iconURL = page?.dataset.duckIcon;
  if (iconURL) celebrate(iconURL);
}

function wasSeen(id) {
  try {
    return sessionStorage.getItem("rionlyu-contact-success") === id;
  } catch {
    return false;
  }
}

function remember(id) {
  try {
    sessionStorage.setItem("rionlyu-contact-success", id);
  } catch {
    // The in-memory guard still prevents duplicate events for this page.
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init, { once: true });
} else {
  init();
}
