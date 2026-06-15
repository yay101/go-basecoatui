basecoat.register('cookie-settings', '#example-cookies:not([data-cookie-settings-initialized])', function(el) {
  var saveBtn = el.querySelector('[data-cookies-save]');
  saveBtn.addEventListener('click', function() {
    var prefs = {};
    el.querySelectorAll('input[type="checkbox"]').forEach(function(cb) {
      prefs[cb.name] = cb.checked;
    });
    window.postJSON('/api/cookie-settings', prefs, function(resp) {
      var summary = Object.keys(prefs).map(function(k) { return k + '=' + prefs[k]; }).join(', ');
      window.toast('success', resp.title || 'Preferences saved', summary);
    });
  });

  el.dataset.cookieSettingsInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
