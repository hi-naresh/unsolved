// Small progressive enhancements. Every page works without this file.
(function () {
  "use strict";
  var wide = window.matchMedia("(min-width: 1024px)");

  // Explorer: on wide screens a problem card opens in the preview pane
  // instead of navigating; the URL still updates so it can be shared.
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("a[data-pane-link]");
    if (!a || !wide.matches || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    var pane = document.getElementById("pane");
    if (!pane || !window.htmx) return;
    e.preventDefault();
    markActive(a.getAttribute("data-pane-link"));
    htmx.ajax("GET", a.getAttribute("href"), { target: "#pane", swap: "innerHTML" }).then(function () {
      pane.scrollTop = 0;
    });
    history.replaceState(null, "", "#" + a.getAttribute("data-pane-link"));
  });

  function markActive(id) {
    document.querySelectorAll("a[data-pane-link]").forEach(function (el) {
      el.setAttribute("aria-current", el.getAttribute("data-pane-link") === id ? "true" : "false");
    });
  }
  // Deep link: /#<problem-id> opens that problem in the pane.
  document.addEventListener("DOMContentLoaded", function () {
    var id = location.hash.slice(1);
    var link = id && document.querySelector('a[data-pane-link="' + CSS.escape(id) + '"]');
    if (link && wide.matches) link.click();
    else if (wide.matches) {
      var first = document.querySelector("a[data-pane-link]");
      if (first) markActive(first.getAttribute("data-pane-link"));
    }
  });

  // "Show more" toggles for clamped text and collapsed step lists.
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-toggle]");
    if (!b) return;
    var t = document.getElementById(b.getAttribute("data-toggle"));
    if (!t) return;
    var open = t.classList.toggle("open");
    b.textContent = open ? (b.getAttribute("data-less") || "Show less") : (b.getAttribute("data-more") || "Show more");
  });

  // Hide "show more" buttons whose text isn't actually clamped.
  function pruneToggles(root) {
    (root || document).querySelectorAll("[data-clamp]").forEach(function (el) {
      var b = document.querySelector('[data-toggle="' + el.id + '"]');
      if (b && el.scrollHeight <= el.clientHeight + 2) b.hidden = true;
    });
  }
  document.addEventListener("DOMContentLoaded", function () { pruneToggles(); });
  document.addEventListener("htmx:afterSettle", function (e) { pruneToggles(e.target); });

  // Live length guidance on long form fields: <textarea data-aim="600">.
  document.addEventListener("input", function (e) {
    var el = e.target;
    if (!el.matches || !el.matches("[data-aim]")) return;
    var out = document.getElementById(el.id + "-count");
    if (!out) return;
    var n = el.value.length, aim = +el.getAttribute("data-aim"), max = +el.getAttribute("maxlength") || 0;
    out.textContent = n + (max ? " / " + max : "") + (n > aim ? " · shorter reads better" : "");
    out.classList.toggle("text-amber-600", n > aim);
  });
})();
