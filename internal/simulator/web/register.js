(() => {
  const pathParts = window.location.pathname.split('/').filter(Boolean);
  const registrationId = pathParts[pathParts.length - 1] || '';
  const token = new URLSearchParams(window.location.search).get('token') || '';
  const capabilityQuery = `?token=${encodeURIComponent(token)}`;
  const endpoint = `/public/payment-method-registrations/${encodeURIComponent(registrationId)}`;
  const loading = document.querySelector('#loading');
  const formPanel = document.querySelector('#form-panel');
  const resultPanel = document.querySelector('#result-panel');
  const form = document.querySelector('#card-form');
  const errorBox = document.querySelector('#form-error');
  const saveButton = document.querySelector('#save-button');
  const cancelButton = document.querySelector('#cancel-button');
  const numberInput = document.querySelector('#card-number');
  const monthSelect = document.querySelector('#expiry-month');
  const yearSelect = document.querySelector('#expiry-year');

  const cards = {
    stripe: [
      { number: '4242424242424242', label: 'Stripe test card', detail: '3DS behaviour is controlled by the simulator settings.' }
    ],
    barion: [
      { number: '5555555555554444', label: 'Barion test card', detail: '3DS behaviour is controlled by the simulator settings.' }
    ]
  };

  const parentOrigin = (() => {
    try { return new URL(document.referrer).origin; } catch { return '*'; }
  })();

  function formatNumber(value) {
    return value.replace(/\D/g, '').slice(0, 16).replace(/(.{4})/g, '$1 ').trim();
  }

  function notifyParent(status) {
    window.parent.postMessage({ type: 'myscoutee:payment-method-registration', registrationId, status }, parentOrigin);
  }

  function showError(message) {
    errorBox.textContent = message || 'The simulator could not complete this request.';
    errorBox.classList.remove('hidden');
  }

  function showResult(status) {
    loading.classList.add('hidden');
    formPanel.classList.add('hidden');
    resultPanel.classList.remove('hidden');
    const cancelled = status !== 'completed';
    document.querySelector('#result-icon').textContent = cancelled ? '×' : '✓';
    document.querySelector('#result-icon').classList.toggle('cancelled', cancelled);
    document.querySelector('#result-title').textContent = cancelled ? 'Registration closed' : 'Card saved';
    document.querySelector('#result-message').textContent = cancelled
      ? 'No card details were saved. You can return to MyScoutee.'
      : 'The provider token is ready. You can return to MyScoutee.';
    notifyParent(status);
  }

  function renderProvider(registration) {
    const provider = registration.provider === 'barion' ? 'barion' : 'stripe';
    document.body.classList.toggle('provider-barion', provider === 'barion');
    document.querySelector('#provider-mark').textContent = provider === 'barion'
      ? 'Barion Smart Gateway · simulator'
      : 'Stripe · simulator';
    const testCards = document.querySelector('#test-cards');
    testCards.replaceChildren(...cards[provider].map(card => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'test-card';
      const strong = document.createElement('strong');
      strong.textContent = card.label;
      const code = document.createElement('code');
      code.textContent = formatNumber(card.number);
      const span = document.createElement('span');
      span.textContent = card.detail;
      button.append(strong, code, span);
      button.addEventListener('click', () => {
        numberInput.value = formatNumber(card.number);
        numberInput.focus();
      });
      return button;
    }));
  }

  async function request(path, options = {}) {
    const response = await fetch(path, {
      ...options,
      headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || 'The simulator rejected this request.');
    return body;
  }

  async function initialize() {
    const now = new Date();
    for (let month = 1; month <= 12; month += 1) {
      const option = document.createElement('option');
      option.value = String(month);
      option.textContent = String(month).padStart(2, '0');
      monthSelect.append(option);
    }
    monthSelect.value = String(now.getMonth() + 1);
    for (let year = now.getFullYear(); year <= now.getFullYear() + 10; year += 1) {
      const option = document.createElement('option');
      option.value = String(year);
      option.textContent = String(year);
      yearSelect.append(option);
    }
    yearSelect.value = String(now.getFullYear() + 3);
    try {
      const registration = await request(endpoint + capabilityQuery);
      if (registration.status !== 'pending') {
        showResult(registration.status);
        return;
      }
      renderProvider(registration);
      loading.classList.add('hidden');
      formPanel.classList.remove('hidden');
    } catch (error) {
      loading.classList.add('hidden');
      formPanel.classList.remove('hidden');
      showError(error.message);
      saveButton.disabled = true;
    }
  }

  numberInput.addEventListener('input', () => { numberInput.value = formatNumber(numberInput.value); });

  form.addEventListener('submit', async event => {
    event.preventDefault();
    errorBox.classList.add('hidden');
    saveButton.disabled = true;
    cancelButton.disabled = true;
    try {
      const registration = await request(endpoint + '/complete' + capabilityQuery, {
        method: 'POST',
        body: JSON.stringify({
          cardNumber: numberInput.value,
          expiryMonth: Number(monthSelect.value),
          expiryYear: Number(yearSelect.value),
          cardholderName: document.querySelector('#cardholder').value,
          securityCode: document.querySelector('#security-code').value
        })
      });
      numberInput.value = '';
      document.querySelector('#security-code').value = '';
      showResult(registration.status);
    } catch (error) {
      showError(error.message);
      saveButton.disabled = false;
      cancelButton.disabled = false;
    }
  });

  cancelButton.addEventListener('click', async () => {
    saveButton.disabled = true;
    cancelButton.disabled = true;
    try {
      const registration = await request(endpoint + '/cancel' + capabilityQuery, { method: 'POST', body: '{}' });
      showResult(registration.status);
    } catch (error) {
      showError(error.message);
      saveButton.disabled = false;
      cancelButton.disabled = false;
    }
  });

  initialize();
})();
