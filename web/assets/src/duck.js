let activeOverlay = null;
let removalTimer = 0;

export function celebrate(iconURL) {
  cleanupCelebration();
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const overlay = document.createElement("div");
  overlay.className = `duck-overlay${reduced ? " is-reduced" : ""}`;
  overlay.setAttribute("aria-hidden", "true");
  const fragment = document.createDocumentFragment();
  const count = reduced ? 3 : 64;

  for (let index = 0; index < count; index += 1) {
    const duck = document.createElement("span");
    duck.className = "duck";
    configureDuck(duck, index, count, reduced, iconURL);
    fragment.append(duck);
  }

  const message = document.createElement("div");
  message.className = "duck-message";
  message.textContent = "MESSAGE SENT.";
  fragment.append(message);
  overlay.append(fragment);
  document.body.append(overlay);
  activeOverlay = overlay;
  removalTimer = window.setTimeout(cleanupCelebration, reduced ? 2500 : 3800);
}

export function cleanupCelebration() {
  window.clearTimeout(removalTimer);
  removalTimer = 0;
  activeOverlay?.remove();
  activeOverlay = null;
}

function configureDuck(duck, index, count, reduced, iconURL) {
  duck.style.setProperty("--duck-mask", `url("${iconURL}")`);
  duck.style.setProperty("--duck-size", `${random(24, 68)}px`);
  duck.style.setProperty("--duck-scale", random(0.72, 1.45).toFixed(2));
  duck.style.setProperty("--duck-start-rotation", `${random(-45, 45)}deg`);
  duck.style.setProperty("--duck-mid-rotation", `${random(-100, 100)}deg`);
  duck.style.setProperty("--duck-end-rotation", `${random(-160, 160)}deg`);

  if (reduced) {
    const positions = [[-95, 65], [0, 90], [95, 60]];
    duck.style.setProperty("--reduced-x", `${positions[index][0]}px`);
    duck.style.setProperty("--reduced-y", `${positions[index][1]}px`);
    return;
  }

  const edge = index % 4;
  const along = ((index / count) * 100 + random(-7, 7) + 100) % 100;
  let startX;
  let startY;
  let travelX;
  let travelY;
  if (edge === 0) {
    startX = -12;
    startY = along;
    travelX = random(116, 136);
    travelY = random(-45, 45);
  } else if (edge === 1) {
    startX = 112;
    startY = along;
    travelX = random(-136, -116);
    travelY = random(-45, 45);
  } else if (edge === 2) {
    startX = along;
    startY = -14;
    travelX = random(-48, 48);
    travelY = random(118, 138);
  } else {
    startX = along;
    startY = 114;
    travelX = random(-48, 48);
    travelY = random(-138, -118);
  }

  duck.style.setProperty("--duck-start-x", `${startX}vw`);
  duck.style.setProperty("--duck-start-y", `${startY}vh`);
  duck.style.setProperty("--duck-travel-x", `${travelX}vw`);
  duck.style.setProperty("--duck-travel-y", `${travelY}vh`);
  duck.style.setProperty("--duck-mid-x", `${travelX * 0.5}vw`);
  duck.style.setProperty("--duck-mid-y", `${travelY * 0.42 + random(-24, 24)}vh`);
  duck.style.setProperty("--duck-duration", `${random(1.65, 2.85).toFixed(2)}s`);
  duck.style.setProperty("--duck-delay", `${random(0, 0.72).toFixed(2)}s`);
}

function random(minimum, maximum) {
  return minimum + Math.random() * (maximum - minimum);
}
