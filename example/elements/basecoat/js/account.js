basecoat.register('create-account', '#example-account:not([data-create-account-initialized])', function(el) {
  function exampleToast(action, payload) {
    window.postJSON('/api/create-account', {action: action, payload: payload || {}}, function(resp) {
      window.toast('info', 'This is an example', resp.message || 'Account creation is not really wired up');
    });
  }

  el.querySelectorAll('[data-account-provider]').forEach(function(btn) {
    btn.addEventListener('click', function() {
      exampleToast(btn.dataset.accountProvider, {});
    });
  });

  var submitBtn = el.querySelector('[data-account-submit]');
  var form = el.querySelector('[data-account-form]');
  submitBtn.addEventListener('click', function() {
    var data = {};
    new FormData(form).forEach(function(value, key) { data[key] = value; });
    exampleToast('email', data);
  });

  el.dataset.createAccountInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
