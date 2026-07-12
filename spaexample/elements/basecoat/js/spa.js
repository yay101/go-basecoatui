// spa.js — minimal fetch + swap navigator.
//
// Intercepts clicks on <a data-spa> links, fetches the page fragment
// from the server (same URL, ?fragment=1), tears down the outgoing
// page's basecoat components via basecoat.destroyAll, swaps innerHTML
// of #app, then runs basecoat.initAll on the new content. Updates the
// menu active state, the page header, history.pushState, and handles
// popstate so back/forward work.
(function () {
  "use strict";

  var PAGE_META = {
    "/": {
      title: "Home",
      subtitle: "Team, cookie, payment and chat cards. Use the menu to swap pages without a reload."
    },
    "/dashboard": {
      title: "Dashboard",
      subtitle: "A distinct dashboard view — stats, the live clock, and the report form. Navigating here from the menu fetches only this fragment from the server."
    }
  };

  function setActiveNav(path) {
    document.querySelectorAll("[data-spa-nav] [data-spa]").forEach(function (a) {
      var isActive = a.getAttribute("href") === path;
      a.dataset.active = isActive ? "true" : "false";
    });
  }

  function updateHeader(path) {
    var meta = PAGE_META[path] || PAGE_META["/"];
    var t = document.querySelector("[data-page-title]");
    var s = document.querySelector("[data-page-subtitle]");
    if (t) t.textContent = meta.title;
    if (s) s.textContent = meta.subtitle;
    document.title = meta.title + " — basecoat SPA example";
  }

  // Destroy outgoing components, swap content, init incoming.
  function swapContent(html, path) {
    var app = document.getElementById("app");
    if (!app) return;
    if (window.basecoat && window.basecoat.destroyAll) {
      window.basecoat.destroyAll(app);
    }
    app.innerHTML = html;
    setActiveNav(path);
    updateHeader(path);
    if (window.basecoat && window.basecoat.initAll) {
      window.basecoat.initAll(app);
    }
  }

  // Fetch a page fragment. The server returns just the inner content
  // of <main id="app"> when ?fragment=1 is present (no shell, no
  // <html>/<head>/<script> tags). On error, fall back to a full
  // navigation so the user always lands somewhere.
  function navigate(path, push) {
    fetch(path + "?fragment=1", { headers: { "Accept": "text/html" } })
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.text();
      })
      .then(function (html) {
        swapContent(html, path);
        if (push) {
          history.pushState({ path: path }, "", path);
        }
      })
      .catch(function (err) {
        if (window.toast) {
          window.toast("error", "Navigation failed", err.message || String(err));
        }
        // Last resort: do a full page load so the user is never stuck.
        window.location.href = path;
      });
  }

  document.addEventListener("click", function (e) {
    var a = e.target.closest("a[data-spa]");
    if (!a) return;
    var href = a.getAttribute("href");
    if (!href || href === "#") return;
    // Let modifier-clicks open a new tab as the browser would.
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    if (href === window.location.pathname) return;
    navigate(href, true);
  });

  window.addEventListener("popstate", function (e) {
    var path = (e.state && e.state.path) || window.location.pathname;
    navigate(path, false);
  });

  // Seed history.state on first load so popstate has something to
  // restore when the user navigates back to the initial page.
  if (!history.state) {
    history.replaceState({ path: window.location.pathname }, "", window.location.pathname);
  }
  setActiveNav(window.location.pathname);
  updateHeader(window.location.pathname);
})();