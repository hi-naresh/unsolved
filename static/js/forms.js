// Form pages only (/new, /p/{id}/revise, /welcome, /settings). Every form
// works without this file; it adds length meters, the live problem preview,
// step progress, why-note starters and the handle format hint.
(function () {
  "use strict";

  function all(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  // Length guidance. app.js writes "n / maxlength" on input; this listener is
  // registered later so it runs after and rephrases it against the aim.
  function count(el) {
    var n = el.value.length, aim = +el.getAttribute("data-aim") || 1, max = +el.getAttribute("maxlength") || 0;
    var over = n > aim, cap = max && max <= 200 ? max : 0;
    var out = document.getElementById(el.id + "-count");
    if (out) {
      out.textContent = n === 0
        ? (cap ? "0 / " + cap : "Aim for about " + aim + " characters")
        : n + (cap ? " / " + cap : " / ~" + aim) + (over ? " · shorter reads better" : "");
      out.classList.toggle("text-amber-600", over);
    }
    var bar = document.getElementById(el.id + "-meter");
    if (bar) {
      bar.style.width = Math.min(100, (n / aim) * 100) + "%";
      bar.classList.toggle("over", over);
    }
  }

  // "1. Do x", "- Do x", "• Do x" all become "Do x"; one step per line.
  function steps(text) {
    return text.split(/\n+/).map(function (s) {
      return s.replace(/^\s*(?:[-*•]|\d+[.)])\s*/, "").trim();
    }).filter(Boolean);
  }

  function firstLine(text) {
    var t = text.trim().split(/\n/)[0] || "";
    return t.trim();
  }

  function setText(pv, key, text) {
    var el = pv.querySelector('[data-pv="' + key + '"]');
    if (!el) return;
    el.textContent = text || el.getAttribute("data-empty");
    el.classList.toggle("pv-empty", !text);
  }

  var MAX_STEPS = 6;

  function renderPreview(form) {
    var pv = form.querySelector("[data-preview]");
    if (!pv) return;
    var val = function (name) { var f = form.elements[name]; return f && f.value ? f.value : ""; };
    setText(pv, "title", val("title").trim());
    setText(pv, "pain", firstLine(val("pain")));
    var dom = form.querySelector('input[name="domain_id"]:checked');
    if (form.querySelector('input[name="domain_id"]')) setText(pv, "domain", dom ? dom.getAttribute("data-name") : "");

    var list = pv.querySelector("[data-pv-steps]"), more = pv.querySelector("[data-pv-more]"), tip = pv.querySelector("[data-pv-tip]");
    var raw = val("current_process"), s = steps(raw);
    if (s.length) {
      list.textContent = "";
      s.slice(0, MAX_STEPS).forEach(function (t) {
        var li = document.createElement("li");
        li.textContent = t.length > 90 ? t.slice(0, 88).trim() + "…" : t;
        list.appendChild(li);
      });
      list.classList.remove("pv-empty");
    } else if (!list.querySelector(".pv-skel")) {
      list.innerHTML = '<li><span class="pv-skel w-4/5"></span></li><li><span class="pv-skel w-3/5"></span></li><li><span class="pv-skel w-2/3"></span></li>';
    }
    more.hidden = s.length <= MAX_STEPS;
    more.textContent = "+ " + (s.length - MAX_STEPS) + " more step" + (s.length - MAX_STEPS === 1 ? "" : "s");
    tip.hidden = !(s.length === 1 && raw.length > 100);
  }

  // A step is done when its fields are valid (optional ones: when filled in).
  function progress(form) {
    var done = 0, stepsEls = all("[data-step]", form);
    stepsEls.forEach(function (st) {
      var fields = all("input:not([type=hidden]), textarea", st), ok = fields.length > 0;
      fields.forEach(function (f) {
        if (f.type === "radio") { if (f.required && !f.checkValidity()) ok = false; }
        else if (f.required) { if (!f.value.trim() || !f.checkValidity() || (f.minLength > 0 && f.value.trim().length < f.minLength)) ok = false; }
        else if (!f.value.trim()) ok = false;
      });
      st.toggleAttribute("data-done", ok);
      if (ok) done++;
    });
    var n = form.querySelector("[data-progress]");
    if (n) n.textContent = done;
    all("[data-dots] > span", form).forEach(function (d, i) { d.classList.toggle("on", i < done); });
  }

  function refresh(form) {
    renderPreview(form);
    progress(form);
  }

  // Handle field: lowercase as you type and say whether the format is right.
  function handleHint(el) {
    var h = document.getElementById(el.id + "-hint");
    if (!h) return;
    var v = el.value, ok = false, bad = false, msg;
    if (!v) msg = h.getAttribute("data-default");
    else if (/[^a-z0-9_]/.test(v)) { msg = "Only lowercase letters, digits and underscores."; bad = true; }
    else if (v.length < 3) { msg = "A little longer — at least 3 characters."; bad = true; }
    else { msg = "Looks good — you'll appear as @" + v; ok = true; }
    h.textContent = msg;
    h.classList.toggle("hint-ok", ok);
    h.classList.toggle("hint-bad", bad);
  }

  function onInput(e) {
    var el = e.target;
    if (!el.matches) return;
    if (el.matches("[data-handle]")) {
      var lower = el.value.toLowerCase();
      if (lower !== el.value) {
        var p = el.selectionStart;
        el.value = lower;
        el.setSelectionRange(p, p);
      }
      handleHint(el);
    }
    if (el.matches("[data-aim]")) count(el);
    var form = el.closest("[data-preview-form]");
    if (form) refresh(form);
  }
  document.addEventListener("input", onInput);
  document.addEventListener("change", onInput);

  // Why-note starters: fill the field, or add to what's there.
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-fill]");
    if (!b) return;
    var t = document.getElementById(b.getAttribute("data-fill-target"));
    if (!t) return;
    var add = b.getAttribute("data-fill"), cur = t.value.trim();
    if (cur.toLowerCase().indexOf(add.toLowerCase()) === -1) {
      t.value = cur ? cur.replace(/[.;,]\s*$/, "") + "; " + add.charAt(0).toLowerCase() + add.slice(1) : add;
    }
    t.dispatchEvent(new Event("input", { bubbles: true }));
    t.focus();
  });

  function init() {
    all("[data-aim]").forEach(count);
    all("[data-preview-form]").forEach(refresh);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
