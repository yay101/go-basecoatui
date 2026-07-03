// lifecycle.js — basecoat component lifecycle extension.
//
// Appended to basecoat.js immediately after the upstream runtime
// (parent mode only). Wraps window.basecoat.register to accept an
// optional destroy(el) callback and exposes
// basecoat.destroy(el) / destroyAll(root) / unregister(name) so
// SPA shells can tear down a page's components before swapping
// innerHTML. Idempotent: loading it twice does not double-wrap.
(function () {
  "use strict";
  if (typeof window === "undefined" || !window.basecoat) return;
  var b = window.basecoat;
  if (b.__lifecycle) return;

  var destroys = Object.create(null);
  var origRegister = b.register;

  b.register = function (name, selector, init, destroy) {
    destroys[name] = typeof destroy === "function" ? destroy : null;
    origRegister(name, selector, init);
  };

  function attrFor(name) {
    return "data-" + name + "-initialized";
  }

  // Run fn against every element inside (and including) root that
  // carries the initialised marker for `name`.
  function eachInitialised(root, name, fn) {
    var attr = attrFor(name);
    var sel = "[" + attr + "]";
    if (root && root.matches && root.matches(sel)) fn(root);
    if (root && root.querySelectorAll) {
      root.querySelectorAll(sel).forEach(fn);
    }
  }

  // Destroy every initialised component whose root element is `el`
  // or lives inside `el`. Clears the marker so a later initAll()
  // re-runs init cleanly. Soft-fails per component.
  b.destroy = function (el) {
    if (!el) return;
    Object.keys(destroys).forEach(function (name) {
      var d = destroys[name];
      if (!d) return;
      eachInitialised(el, name, function (node) {
        try { d(node); } catch (e) {
          console.warn("basecoat destroy failed:", name, e);
        }
        node.removeAttribute(attrFor(name));
      });
    });
  };

  // Sugar: destroy everything inside root (inclusive).
  b.destroyAll = function (root) {
    b.destroy(root || document.body);
  };

  // Drop a component from the destroy registry. The underlying
  // basecoat registry has no public unregister, so init() still
  // runs for matching selectors; we simply stop calling destroy.
  b.unregister = function (name) {
    delete destroys[name];
  };

  b.__lifecycle = true;
})();