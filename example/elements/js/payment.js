basecoat.register('payment-method', '#example-payment:not([data-payment-method-initialized])', function(el) {
  var form = el.querySelector('[data-payment-form]');
  var continueBtn = el.querySelector('[data-payment-continue]');
  continueBtn.addEventListener('click', function() {
    var data = {};
    new FormData(form).forEach(function(value, key) { data[key] = value; });
    window.postJSON('/api/payment-method', data, function(resp) {
      window.toast('info', 'This is an example', 'Submitted: ' + JSON.stringify(data));
    });
  });

  el.dataset.paymentMethodInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
