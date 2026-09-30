// Small progressive enhancements. Every page works without this file.
(function () {
  "use strict";
  var wide = window.matchMedia("(min-width: 1024px)");

  // Explorer: on wide screens a problem card opens in the preview pane
  // instead of navigating; the hash keeps the selection shareable.
  function openInPane(a, remember) {
    var pane = document.getElementById("pane");
    if (!pane || !window.htmx) return false;
    var id = a.getAttribute("data-pane-link");
    markActive(id);
    htmx.ajax("GET", a.getAttribute("href"), { target: "#pane", swap: "innerHTML" });
    if (remember) history.replaceState(null, "", "#" + id);
    return true;
  }
  function markActive(id) {
    document.querySelectorAll("a[data-pane-link]").forEach(function (el) {
      el.setAttribute("aria-current", el.getAttribute("data-pane-link") === id ? "true" : "false");
    });
  }
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("a[data-pane-link]");
    if (!a || !wide.matches || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    if (openInPane(a, true)) e.preventDefault();
  });
  // On load, open the problem named in the hash, else the first one.
  document.addEventListener("DOMContentLoaded", function () {
    if (!wide.matches || !document.getElementById("pane")) return;
    var id = location.hash.slice(1);
    var link = (id && document.querySelector('a[data-pane-link="' + CSS.escape(id) + '"]')) ||
      document.querySelector("a[data-pane-link]");
    if (link) openInPane(link, false);
  });
  // Keyboard: j / k move through the list when focus isn't in a field.
  document.addEventListener("keydown", function (e) {
    if ((e.key !== "j" && e.key !== "k") || e.metaKey || e.ctrlKey || e.altKey) return;
    if (/^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName) || !document.getElementById("pane")) return;
    var links = Array.prototype.slice.call(document.querySelectorAll("a[data-pane-link]"));
    var i = links.findIndex(function (l) { return l.getAttribute("aria-current") === "true"; });
    var next = links[Math.max(0, Math.min(links.length - 1, i + (e.key === "j" ? 1 : -1)))];
    if (next && wide.matches) { openInPane(next, true); next.scrollIntoView({ block: "nearest" }); next.focus({ preventScroll: true }); }
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
