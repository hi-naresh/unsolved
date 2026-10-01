// Resolve the saved appearance before paint. Storage can be unavailable in private mode.
(function () {
  "use strict";
  var media = window.matchMedia("(prefers-color-scheme: dark)");
  var choice = "system";
  try { choice = localStorage.getItem("unsolved-appearance") || "system"; } catch (_) {}
  function apply() {
    var dark = choice === "dark" || (choice === "system" && media.matches);
    document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
    var button = document.querySelector("[data-theme-toggle]");
    if (button) {
      var next = choice === "system" ? "light" : choice === "light" ? "dark" : "system";
      var label = "Appearance: " + choice + ". Switch to " + next;
      button.setAttribute("aria-label", label);
      button.setAttribute("title", label);
    }
  }
  apply();
  media.addEventListener("change", apply);
  document.addEventListener("DOMContentLoaded", apply);
  document.addEventListener("click", function (e) {
    if (!e.target.closest("[data-theme-toggle]")) return;
    choice = choice === "system" ? "light" : choice === "light" ? "dark" : "system";
    try { localStorage.setItem("unsolved-appearance", choice); } catch (_) {}
    apply();
  });
})();
