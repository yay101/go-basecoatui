basecoat.register('dropdown-menu', '.dropdown-menu:not([data-dropdown-menu-initialized])', function(el) {
  var trigger = el.querySelector(':scope > button');
  var popover = el.querySelector(':scope > [data-popover]');
  var menu   = popover.querySelector('[role="menu"]');
  var items  = Array.from(menu.querySelectorAll('[role^="menuitem"]'));
  var idx    = -1;

  function open() {
    document.querySelectorAll('.dropdown-menu [aria-expanded="true"]').forEach(function(b) {
      b.setAttribute('aria-expanded', 'false');
    });
    trigger.setAttribute('aria-expanded', 'true');
    popover.setAttribute('aria-hidden', 'false');
    idx = -1;
  }
  function close(refocus) {
    trigger.setAttribute('aria-expanded', 'false');
    popover.setAttribute('aria-hidden', 'true');
    if (refocus !== false) trigger.focus();
    idx = -1;
  }
  function highlight(n) {
    if (idx > -1 && items[idx]) items[idx].classList.remove('active');
    idx = n;
    if (idx > -1 && items[idx]) items[idx].classList.add('active');
  }

  trigger.addEventListener('click', function() {
    trigger.getAttribute('aria-expanded') === 'true' ? close() : open();
  });
  el.addEventListener('keydown', function(e) {
    if (e.key === 'Escape') { close(); return; }
    if (trigger.getAttribute('aria-expanded') !== 'true') {
      if (e.key === 'ArrowDown') { e.preventDefault(); open(); }
      return;
    }
    switch (e.key) {
      case 'ArrowDown': e.preventDefault(); highlight(idx < items.length - 1 ? idx + 1 : 0); break;
      case 'ArrowUp':   e.preventDefault(); highlight(idx > 0 ? idx - 1 : items.length - 1); break;
      case 'Enter':
      case ' ':
        e.preventDefault();
        if (idx > -1) { items[idx].click(); close(); }
        break;
    }
  });
  menu.addEventListener('click', function(e) {
    if (e.target.closest('[role^="menuitem"]')) close();
  });
  document.addEventListener('click', function(e) {
    if (!el.contains(e.target)) close(false);
  });

  el.dataset.dropdownMenuInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
