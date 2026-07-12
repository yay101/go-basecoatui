// A live clock that ticks every second. Demonstrates the optional
// destroy(el) callback: when basecoat.destroy(el) runs (or
// destroyAll(root)), the interval is cleared so it doesn't keep
// ticking on a detached node. Try the "Destroy clock" button below
// the card, then "Re-init clock" — the clock stops, then restarts.
basecoat.register(
  'live-clock',
  '[data-live-clock]:not([data-live-clock-initialized])',
  function (el) {
    el.setAttribute('data-live-clock-initialized', 'true');
    var display = el.querySelector('[data-live-clock-time]');
    function tick() {
      var d = new Date();
      display.textContent =
        String(d.getHours()).padStart(2, '0') + ':' +
        String(d.getMinutes()).padStart(2, '0') + ':' +
        String(d.getSeconds()).padStart(2, '0');
    }
    tick();
    var timer = setInterval(tick, 1000);
    // Stash the handle so the destroy fn can clear it. Using a field
    // on the element keeps init/destroy paired per-node, which is
    // what destroy() walks over.
    el.__liveClockTimer = timer;
    el.dispatchEvent(new CustomEvent('basecoat:initialized'));
  },
  function (el) {                                   // destroy
    if (el.__liveClockTimer) {
      clearInterval(el.__liveClockTimer);
      el.__liveClockTimer = null;
    }
  }
);