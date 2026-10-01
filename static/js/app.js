// Progressive enhancement: ordinary links and form posts remain the baseline.
(function () {
  "use strict";
  document.documentElement.classList.add("js");
  var wide = window.matchMedia("(min-width: 1024px)");
  var request = null, sequence = 0, selected = null;
  function escapeHTML(s) { return s.replace(/[&<>"']/g, function (c) { return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]; }); }
  function markActive(id) {
    document.querySelectorAll("a[data-pane-link]").forEach(function (a) {
      a.setAttribute("aria-current", a.getAttribute("data-pane-link") === id ? "true" : "false");
    });
  }
  function openInPane(a, remember) {
    var pane = document.getElementById("pane");
    if (!pane || !window.htmx || typeof fetch !== "function") return false;
    if (request) request.abort();
    request = new AbortController();
    var current = ++sequence, id = a.getAttribute("data-pane-link"), href = a.getAttribute("href");
    selected = a;
    markActive("");
    pane.setAttribute("aria-busy", "true");
    pane.innerHTML = '<div class="preview-status"><h2>Opening this problem…</h2><p>Loading its process and proposed solutions.</p></div>';
    fetch(href, { signal: request.signal, headers: {"HX-Request":"true", "HX-Target":"pane"} })
      .then(function (response) { if (!response.ok) throw new Error("preview"); return response.text(); })
      .then(function (html) {
        if (current !== sequence) return;
        pane.innerHTML = html;
        window.htmx.process(pane);
        pane.setAttribute("aria-busy", "false");
        markActive(id);
        enhance();
        if (remember) history.replaceState(null, "", "#" + encodeURIComponent(id));
      })
      .catch(function (error) {
        if (current !== sequence || error.name === "AbortError") return;
        pane.setAttribute("aria-busy", "false");
        pane.innerHTML = '<div class="preview-status"><h2>This preview could not load</h2><p>Try again, or open the full problem.</p><div class="flex flex-wrap gap-3"><button type="button" class="btn" data-preview-retry>Try again</button><a class="btn-secondary" href="' + escapeHTML(href) + '">Open problem</a></div></div>';
      });
    return true;
  }
  document.addEventListener("click", function (e) {
    var target = e.target.closest && e.target.closest("[data-preview-retry]");
    if (target && selected) { openInPane(selected, true); return; }
    var a = e.target.closest && e.target.closest("a[data-pane-link]");
    if (!a || !wide.matches || e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || e.button !== 0) return;
    if (openInPane(a, true)) e.preventDefault();
  });
  function initialPreview() {
    if (!wide.matches || !document.getElementById("pane")) return;
    var id;
    try { id = decodeURIComponent(location.hash.slice(1)); } catch (_) { id = ""; }
    var link = (id && document.querySelector('a[data-pane-link="' + CSS.escape(id) + '"]')) || document.querySelector("a[data-pane-link]");
    if (link) openInPane(link, false);
  }
  wide.addEventListener("change", function () { if (wide.matches && !selected) initialPreview(); });
  document.addEventListener("keydown", function (e) {
    var active = document.activeElement;
    if (e.key === "Escape") document.querySelectorAll(".account-menu[open], .report-link[open]").forEach(function (d) { d.open = false; d.querySelector("summary").focus(); });
    if (/^(INPUT|TEXTAREA|SELECT)$/.test(active.tagName) || active.isContentEditable || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "/") {
      var search = document.querySelector('.nav-search input');
      if (search && search.getClientRects().length) { e.preventDefault(); search.focus(); }
      return;
    }
    if ((e.key !== "j" && e.key !== "k") || !wide.matches || !document.getElementById("pane")) return;
    var links = Array.prototype.slice.call(document.querySelectorAll("a[data-pane-link]"));
    var i = links.indexOf(selected);
    var next = links[Math.max(0, Math.min(links.length - 1, i + (e.key === "j" ? 1 : -1)))];
    if (next) { e.preventDefault(); openInPane(next, true); next.scrollIntoView({block:"nearest"}); next.focus({preventScroll:true}); }
  });
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-toggle]");
    if (!b) return;
    var t = document.getElementById(b.getAttribute("data-toggle"));
    if (!t) return;
    var open = t.classList.toggle("open");
    b.setAttribute("aria-expanded", String(open));
    b.textContent = open ? (b.getAttribute("data-less") || "Show less") : (b.getAttribute("data-more") || "Show more");
  });
  function enhance() {
    document.querySelectorAll("[data-toggle]").forEach(function (b) {
      var t = document.getElementById(b.getAttribute("data-toggle"));
      if (!t) return;
      b.setAttribute("aria-controls", t.id);
      b.setAttribute("aria-expanded", String(t.classList.contains("open")));
      if (t.hasAttribute("data-clamp") && !t.classList.contains("open")) b.hidden = t.scrollHeight <= t.clientHeight + 2;
    });
    document.querySelectorAll(".error[id]").forEach(function (error) {
      var field = document.getElementById(error.id.replace(/-error$/, ""));
      if (field) { field.setAttribute("aria-invalid", "true"); field.setAttribute("aria-describedby", error.id); }
    });
    var path = location.pathname, query = new URLSearchParams(location.search);
    document.querySelectorAll("[data-nav]").forEach(function (a) {
      var key = a.getAttribute("data-nav");
      var current = key === "solved" ? path === "/problems" && query.get("state") === "solved" :
        key === "explore" ? path === "/" || path.indexOf("/p/") === 0 || (path === "/problems" && query.get("state") !== "solved") :
        key === "members" ? path === "/members" || path.indexOf("/u/") === 0 :
        key === "search" ? path === "/search" : path === "/new";
      if (current) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
    });
  }
  document.addEventListener("DOMContentLoaded", function () { enhance(); initialPreview(); });
  document.addEventListener("htmx:afterSettle", enhance);
  document.addEventListener("htmx:beforeRequest", function (e) {
    var el = e.detail.elt;
    if (el.matches("button")) { el.disabled = true; el.setAttribute("aria-busy", "true"); }
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    var el = e.detail.elt;
    if (el.matches("button")) { el.disabled = false; el.removeAttribute("aria-busy"); }
    // A failed duplicate suggestion should not interrupt a contribution.
    if (e.detail.failed && el.id !== "current_process") {
      var feedback = document.getElementById("app-feedback");
      if (feedback) { feedback.textContent = "That request did not complete. Check your connection and try again."; feedback.hidden = false; }
    } else {
      var feedback = document.getElementById("app-feedback");
      if (feedback) feedback.hidden = true;
    }
  });
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
