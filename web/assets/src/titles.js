import {
  layoutNextLineRange,
  materializeLineRange,
  prepareWithSegments,
} from "@chenglou/pretext";

const preparedCache = new Map();
const states = new Map();
const finePointer = window.matchMedia("(hover: hover) and (pointer: fine)");
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
let observer;
let resizeTimer = 0;

function typographyFor(element) {
  const style = getComputedStyle(element);
  const letterSpacing = style.letterSpacing === "normal" ? 0 : Number.parseFloat(style.letterSpacing) || 0;
  const lineHeight = Number.parseFloat(style.lineHeight);
  const font = `${style.fontStyle} ${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
  return { font, letterSpacing, lineHeight, fontSize: Number.parseFloat(style.fontSize) };
}

function cacheKey(title, typography) {
  return `${title}\u0000${typography.font}\u0000${typography.letterSpacing}`;
}

function prepareState(heading) {
  if (states.has(heading) || !finePointer.matches || reducedMotion.matches) return;
  const semantic = heading.querySelector(".post-title-semantic");
  const visual = heading.querySelector(".post-title-visual");
  if (!semantic || !visual) return;

  const typography = typographyFor(semantic);
  const height = semantic.getBoundingClientRect().height;
  if (!Number.isFinite(typography.lineHeight) || height < typography.lineHeight * 1.7) return;

  const title = semantic.textContent.trim();
  const key = cacheKey(title, typography);
  let prepared = preparedCache.get(key);
  if (!prepared) {
    prepared = prepareWithSegments(title, typography.font, { letterSpacing: typography.letterSpacing });
    preparedCache.set(key, prepared);
  }

  const state = {
    heading,
    semantic,
    visual,
    prepared,
    typography,
    frame: 0,
    resetTimer: 0,
    pointer: null,
  };
  const onEnter = (event) => activate(state, event);
  const onMove = (event) => queueLayout(state, event);
  const onLeave = () => reset(state);
  heading.addEventListener("pointerenter", onEnter);
  heading.addEventListener("pointermove", onMove);
  heading.addEventListener("pointerleave", onLeave);
  state.removeListeners = () => {
    heading.removeEventListener("pointerenter", onEnter);
    heading.removeEventListener("pointermove", onMove);
    heading.removeEventListener("pointerleave", onLeave);
  };
  states.set(heading, state);
}

function activate(state, event) {
  if (event.pointerType !== "mouse" || reducedMotion.matches || !finePointer.matches) return;
  window.clearTimeout(state.resetTimer);
  state.heading.classList.remove("is-pretext-resetting");
  state.heading.classList.add("is-pretext-active");
  state.pointer = localPointer(state, event);
  draw(state);
}

function queueLayout(state, event) {
  if (!state.heading.classList.contains("is-pretext-active") || event.pointerType !== "mouse") return;
  state.pointer = localPointer(state, event);
  if (state.frame) return;
  state.frame = requestAnimationFrame(() => {
    state.frame = 0;
    draw(state);
  });
}

function localPointer(state, event) {
  const bounds = state.heading.getBoundingClientRect();
  return { x: event.clientX - bounds.left, y: event.clientY - bounds.top };
}

function draw(state) {
  const width = state.heading.clientWidth;
  const height = state.heading.clientHeight;
  if (width <= 0 || height <= 0 || !state.pointer) return;

  const baseRadius = Math.min(96, Math.max(52, state.typography.fontSize * 0.92));
  let result;
  for (const scale of [1, 0.8, 0.62, 0.48]) {
    result = flowAroundPointer(state, width, height, baseRadius * scale);
    if (result.complete) break;
  }
  if (!result.complete) result = repelledCanonicalLines(state, width, height);
  renderFragments(state.visual, result.fragments);
}

function flowAroundPointer(state, width, height, radius) {
  const fragments = [];
  const lineHeight = state.typography.lineHeight;
  const rows = Math.max(1, Math.floor(height / lineHeight + 0.2));
  const minimumFragment = Math.max(56, state.typography.fontSize * 1.05);
  let cursor = { segmentIndex: 0, graphemeIndex: 0 };

  for (let row = 0; row < rows; row += 1) {
    const y = row * lineHeight;
    const centerY = y + lineHeight / 2;
    const distanceY = Math.abs(centerY - state.pointer.y);
    if (distanceY >= radius) {
      const range = layoutNextLineRange(state.prepared, cursor, width);
      if (range === null) return { fragments, complete: true };
      fragments.push({ text: materializeLineRange(state.prepared, range).text, x: 0, y });
      cursor = range.end;
      continue;
    }

    const chord = Math.sqrt(radius * radius - distanceY * distanceY);
    const gap = Math.max(10, state.typography.fontSize * 0.12);
    const obstacleStart = Math.max(0, state.pointer.x - chord - gap);
    const obstacleEnd = Math.min(width, state.pointer.x + chord + gap);
    const leftWidth = obstacleStart;
    const rightWidth = width - obstacleEnd;
    let placed = false;

    if (leftWidth >= minimumFragment) {
      const leftRange = layoutNextLineRange(state.prepared, cursor, leftWidth);
      if (leftRange === null) return { fragments, complete: true };
      fragments.push({ text: materializeLineRange(state.prepared, leftRange).text, x: 0, y });
      cursor = leftRange.end;
      placed = true;
    }
    if (rightWidth >= minimumFragment) {
      const rightRange = layoutNextLineRange(state.prepared, cursor, rightWidth);
      if (rightRange === null) return { fragments, complete: true };
      fragments.push({ text: materializeLineRange(state.prepared, rightRange).text, x: obstacleEnd, y });
      cursor = rightRange.end;
      placed = true;
    }
    if (!placed) {
      const range = layoutNextLineRange(state.prepared, cursor, width);
      if (range === null) return { fragments, complete: true };
      const direction = state.pointer.x < width / 2 ? Math.min(20, radius * 0.25) : -Math.min(20, radius * 0.25);
      fragments.push({ text: materializeLineRange(state.prepared, range).text, x: direction, y });
      cursor = range.end;
    }
  }
  return { fragments, complete: layoutNextLineRange(state.prepared, cursor, width) === null };
}

function repelledCanonicalLines(state, width, height) {
  const fragments = [];
  const lineHeight = state.typography.lineHeight;
  const rows = Math.max(1, Math.floor(height / lineHeight + 0.2));
  let cursor = { segmentIndex: 0, graphemeIndex: 0 };
  for (let row = 0; row < rows; row += 1) {
    const range = layoutNextLineRange(state.prepared, cursor, width);
    if (range === null) break;
    const y = row * lineHeight;
    const distance = Math.abs(y + lineHeight / 2 - state.pointer.y);
    const influence = Math.max(0, 1 - distance / (lineHeight * 1.5));
    const direction = state.pointer.x < width / 2 ? 1 : -1;
    fragments.push({
      text: materializeLineRange(state.prepared, range).text,
      x: direction * influence * Math.min(22, state.typography.fontSize * 0.28),
      y,
    });
    cursor = range.end;
  }
  return { fragments, complete: true };
}

function renderFragments(layer, fragments) {
  const fragment = document.createDocumentFragment();
  for (const line of fragments) {
    const span = document.createElement("span");
    span.className = "pretext-fragment";
    span.textContent = line.text;
    span.style.setProperty("--fragment-x", `${line.x}px`);
    span.style.setProperty("--fragment-y", `${line.y}px`);
    fragment.append(span);
  }
  layer.replaceChildren(fragment);
}

function reset(state) {
  if (state.frame) cancelAnimationFrame(state.frame);
  state.frame = 0;
  state.heading.classList.add("is-pretext-resetting");
  state.heading.classList.remove("is-pretext-active");
  state.resetTimer = window.setTimeout(() => {
    state.heading.classList.remove("is-pretext-resetting");
    state.visual.replaceChildren();
  }, 180);
}

function cleanupAll() {
  observer?.disconnect();
  for (const state of states.values()) {
    if (state.frame) cancelAnimationFrame(state.frame);
    window.clearTimeout(state.resetTimer);
    state.removeListeners();
    state.visual.replaceChildren();
    state.heading.classList.remove("is-pretext-active", "is-pretext-resetting");
  }
  states.clear();
}

function init() {
  if (!finePointer.matches || reducedMotion.matches) return;
  observer?.disconnect();
  observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting) {
        prepareState(entry.target);
        observer.unobserve(entry.target);
      }
    }
  }, { rootMargin: "160px" });
  for (const title of document.querySelectorAll("[data-pretext-title]")) {
    if (!states.has(title)) observer.observe(title);
  }
}

const fontsReady = document.fonts?.ready ?? Promise.resolve();
fontsReady.then(init);
document.addEventListener("htmx:beforeHistorySave", cleanupAll);
document.addEventListener("htmx:afterSwap", () => fontsReady.then(init));
window.addEventListener("resize", () => {
  window.clearTimeout(resizeTimer);
  resizeTimer = window.setTimeout(() => {
    cleanupAll();
    fontsReady.then(init);
  }, 120);
}, { passive: true });
finePointer.addEventListener("change", () => {
  cleanupAll();
  fontsReady.then(init);
});
reducedMotion.addEventListener("change", () => {
  cleanupAll();
  fontsReady.then(init);
});
window.addEventListener("pagehide", () => {
  window.clearTimeout(resizeTimer);
  cleanupAll();
}, { once: true });
