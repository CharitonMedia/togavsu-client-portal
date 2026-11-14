// Client Portal page script. The frame is sandboxed with no same-origin access, so the
// page keeps no cookie or storage: the context token rides on every request. When the
// module answers 401 (the token expired), the page asks its parent frame for a fresh
// token (togavsu:context.refresh, CORE-09) and retries once.
(function () {
  "use strict";
  var retriedParam = "_retried";

  function askParent(cb) {
    if (window.parent === window) { cb(null); return; }
    var id = "r" + Math.random().toString(36).slice(2);
    function on(e) {
      if (e.source !== window.parent) return;
      var d = e.data || {};
      if (d.request_id !== id) return;
      window.removeEventListener("message", on);
      cb(d.type === "togavsu:context.token" ? d.token : null);
    }
    window.addEventListener("message", on);
    window.parent.postMessage({ type: "togavsu:context.refresh", request_id: id }, "*");
  }

  function setToken(t) {
    var b = document.body;
    if (!b || !t) return;
    b.setAttribute("data-context", t);
    b.setAttribute("hx-vals", JSON.stringify({ context: t }));
    var links = document.querySelectorAll("a[href*='context='], form[action*='context=']");
    for (var i = 0; i < links.length; i++) {
      var attr = links[i].tagName === "A" ? "href" : "action";
      var u = new URL(links[i].getAttribute(attr), location.href);
      u.searchParams.set("context", t);
      links[i].setAttribute(attr, u.pathname + u.search);
    }
  }

  function token() { return document.body ? document.body.getAttribute("data-context") : ""; }

  // An expired page: get a fresh token and reload once; a second failure asks the
  // client to sign in again.
  function expiredPage() {
    var u = new URL(location.href);
    if (u.searchParams.get(retriedParam)) { showSignIn(); return; }
    askParent(function (t) {
      if (!t) { showSignIn(); return; }
      u.searchParams.set("context", t);
      u.searchParams.set(retriedParam, "1");
      location.replace(u.pathname + u.search);
    });
  }

  function showSignIn() {
    var a = document.getElementById("expired-note"), b = document.getElementById("expired-signin");
    if (a) a.hidden = true;
    if (b) b.hidden = false;
  }

  // Each file goes as its own request, so one bad file fails alone; every result,
  // failures included, stays listed.
  function uploads(form) {
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var input = form.querySelector("input[type=file]");
      var list = document.getElementById("upload-results");
      var files = Array.prototype.slice.call(input.files || []);
      var landed = 0;
      function next(i, retried) {
        if (i >= files.length) { if (landed) setTimeout(function () { location.reload(); }, 1500); return; }
        var f = files[i];
        var u = new URL(form.getAttribute("data-upload"), location.href);
        u.searchParams.set("context", token());
        u.searchParams.set("matter_id", form.getAttribute("data-matter"));
        u.searchParams.set("name", f.name);
        u.searchParams.set("size", String(f.size));
        fetch(u.pathname + u.search, { method: "POST", body: f, headers: { "Content-Type": "application/octet-stream" } })
          .then(function (resp) {
            if (resp.status === 401 && !retried) {
              askParent(function (t) { if (t) { setToken(t); next(i, true); } else { report(f, false, "Sign in again to upload."); next(i + 1); } });
              return null;
            }
            return resp.json().then(function (body) {
              if (resp.ok) { landed++; report(f, true, "Uploaded"); } else { report(f, false, body.message || "Not uploaded"); }
              next(i + 1);
            });
          })
          .catch(function () { report(f, false, "Not uploaded. Check your connection and try again."); next(i + 1); });
      }
      function report(f, ok, text) {
        var li = document.createElement("li");
        li.className = ok ? "ok" : "failed";
        li.textContent = f.name + ": " + text;
        list.appendChild(li);
      }
      next(0);
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    if (document.body.getAttribute("data-expired")) { expiredPage(); return; }
    var form = document.getElementById("upload-form");
    if (form) uploads(form);
  });

  if (window.htmx) {
    // Swap error pages too (the refusal stays on screen), except 401, which retries.
    htmx.config.responseHandling = [{ code: "204", swap: false }, { code: "401", swap: false, error: true }, { code: "...", swap: true }];
    document.addEventListener("htmx:responseError", function (e) {
      var d = e.detail;
      if (!d.xhr || d.xhr.status !== 401 || d.elt.getAttribute("data-retried")) return;
      askParent(function (t) {
        if (!t) return;
        setToken(t);
        d.elt.setAttribute("data-retried", "1");
        var params = {};
        var p = d.requestConfig.parameters || {};
        if (p instanceof FormData) { p.forEach(function (v, k) { params[k] = v; }); } else { params = p; }
        params.context = t;
        var u = new URL(d.requestConfig.path, location.href);
        u.searchParams.set("context", t);
        htmx.ajax(d.requestConfig.verb.toUpperCase(), u.pathname + u.search, { source: d.elt, values: params, target: d.target });
      });
    });
  }
})();
