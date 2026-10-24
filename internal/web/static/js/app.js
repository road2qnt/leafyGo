(() => {
  "use strict";

  function dismiss(flash) {
    const hadFocus = flash.contains(document.activeElement);
    flash.remove();
    if (hadFocus) {
      document.getElementById("main")?.focus();
    }
  }

  for (const button of document.querySelectorAll("[data-reveal]")) {
    const input = document.getElementById(button.dataset.reveal);
    if (!input) continue;
    button.hidden = false;
    button.addEventListener("click", () => {
      const reveal = input.type === "password";
      input.type = reveal ? "text" : "password";
      button.setAttribute("aria-pressed", String(reveal));
    });
  }

  const dropdowns = document.querySelectorAll("details[data-dropdown]");
  document.addEventListener("click", (event) => {
    for (const dropdown of dropdowns) {
      if (dropdown.open && !dropdown.contains(event.target)) dropdown.open = false;
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    for (const dropdown of dropdowns) {
      if (!dropdown.open) continue;
      dropdown.open = false;
      dropdown.querySelector("summary").focus();
    }
  });

  for (const flash of document.querySelectorAll("[data-flash]")) {
    const close = flash.querySelector(".flash__close");
    const timer = flash.querySelector(".flash__timer");
    close.hidden = false;
    close.addEventListener("click", () => dismiss(flash));
    if (timer) {
      timer.addEventListener("animationend", () => dismiss(flash));
      flash.classList.add("flash--timed");
    }
  }
})();
