window.toast = function(category, title, description) {
  var toaster = document.getElementById('toaster');
  if (!toaster || typeof toaster.toast !== 'function') return;
  toaster.toast({
    category: category || 'info',
    title: title,
    description: description
  });
};

window.postJSON = function(url, data, onSuccess) {
  fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data)
  })
  .then(function(r) {
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.json();
  })
  .then(onSuccess)
  .catch(function(err) {
    window.toast('error', 'Request failed', err.message || String(err));
  });
};
