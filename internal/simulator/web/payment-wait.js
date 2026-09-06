(() => {
  const root = document.querySelector('#payment-wait');
  const id = root?.dataset.id || '';
  const statusUrl = root?.dataset.statusUrl || '';
  const parentOrigin = (() => {
    try { return new URL(document.referrer).origin; } catch { return '*'; }
  })();
  let polling = false;
  let timer = 0;
  let completed = false;

  function terminal(status) {
    return ['authorized', 'captured', 'cancelled', 'declined', 'expired', 'failed', 'released'].includes(status);
  }

  function notify(status) {
    window.parent.postMessage({
      source: 'myscoutee-payment-simulator',
      type: 'payment-authorization',
      id,
      status
    }, parentOrigin);
  }

  function showResult(status) {
    if (!root || completed) return;
    completed = true;
    window.clearInterval(timer);
    const icon = root.querySelector('.result-icon');
    const title = root.querySelector('h1');
    const description = root.querySelector('p');
    const success = status === 'authorized' || status === 'captured';
    const timeout = status === 'expired';
    root.classList.add('is-result', success ? 'is-success' : timeout ? 'is-timeout' : 'is-failure');
    if (icon) icon.textContent = success ? '✓' : timeout ? '⌛' : '×';
    if (title) title.textContent = success ? 'Payment confirmed' : timeout ? 'Confirmation timed out' : 'Payment was not confirmed';
    if (description) description.textContent = success
      ? 'The simulator accepted the external confirmation.'
      : timeout
        ? 'The three-minute confirmation window has expired.'
        : 'The simulator returned an unsuccessful result.';
    window.setTimeout(() => notify(status), 1600);
  }

  function updateCountdown(expiresAt) {
    const countdown = root?.querySelector('#countdown');
    if (!countdown) return;
    const remainingSeconds = Math.max(0, Math.ceil(Number(expiresAt) - Date.now() / 1000));
    const minutes = Math.floor(remainingSeconds / 60);
    const seconds = remainingSeconds % 60;
    countdown.textContent = `${minutes}:${String(seconds).padStart(2, '0')}`;
  }

  async function refresh() {
    if (polling || !id || !statusUrl) return;
    polling = true;
    try {
      const response = await fetch(statusUrl, { cache: 'no-store' });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) return;
      const status = `${body.status || ''}`.trim().toLowerCase();
      if (terminal(status)) {
        showResult(status);
      } else {
        updateCountdown(body.expiresAt);
      }
    } finally {
      polling = false;
    }
  }

  void refresh();
  timer = window.setInterval(refresh, 1000);
})();
