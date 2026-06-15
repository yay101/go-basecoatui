basecoat.register('chat', '#example-chat:not([data-chat-initialized])', function(el) {
  var input = el.querySelector('[data-chat-input]');
  var sendBtn = el.querySelector('[data-chat-send]');
  var thread = el.querySelector('[data-chat-thread]');

  input.addEventListener('input', function() {
    if (input.value.trim()) {
      sendBtn.removeAttribute('disabled');
    } else {
      sendBtn.setAttribute('disabled', '');
    }
  });

  sendBtn.addEventListener('click', function() {
    var text = input.value.trim();
    if (!text) return;
    var bubble = document.createElement('div');
    bubble.className = 'flex w-max max-w-[75%] flex-col gap-2 rounded-lg px-3 py-2 text-sm ml-auto bg-primary text-primary-foreground';
    bubble.textContent = text;
    thread.appendChild(bubble);
    input.value = '';
    sendBtn.setAttribute('disabled', '');
    window.postJSON('/api/chat', {message: text}, function(resp) {
      window.toast('success', resp.title || 'Sent', text);
    });
  });

  input.addEventListener('keydown', function(e) {
    if (e.key === 'Enter' && !sendBtn.hasAttribute('disabled')) {
      e.preventDefault();
      sendBtn.click();
    }
  });

  el.dataset.chatInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
