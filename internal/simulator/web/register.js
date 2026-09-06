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
  const generateButton = document.querySelector('#generate-card-button');
  const cardholderInput = document.querySelector('#cardholder');
  const numberInput = document.querySelector('#card-number');
  const monthSelect = document.querySelector('#expiry-month');
  const yearSelect = document.querySelector('#expiry-year');
  const securityCodeInput = document.querySelector('#security-code');

  let activeProvider = 'stripe';
  let registrationPoll = null;
  let resultNotification = null;

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
    if (registrationPoll !== null) window.clearTimeout(registrationPoll);
    registrationPoll = null;
    if (resultNotification !== null) window.clearTimeout(resultNotification);
    loading.classList.add('hidden');
    formPanel.classList.add('hidden');
    resultPanel.classList.remove('hidden');
    const resultIcon = document.querySelector('#result-icon');
    resultIcon.className = 'result-icon';
    const results = {
      completed: ['✓', 'Card saved', 'The provider token is ready. Returning to MyScoutee…'],
      expired: ['⌛', 'Registration timed out', 'The 3DS confirmation was not completed in time.'],
      failed: ['×', 'Registration declined', 'The card was not saved.'],
      cancelled: ['×', 'Registration closed', 'No card details were saved.']
    };
    const result = results[status] || results.failed;
    resultIcon.textContent = result[0];
    if (status !== 'completed') resultIcon.classList.add(status === 'expired' ? 'expired' : 'cancelled');
    document.querySelector('#result-title').textContent = result[1];
    document.querySelector('#result-message').textContent = result[2];
    resultNotification = window.setTimeout(() => notifyParent(status), 1600);
  }

  function remainingTime(registration) {
    const expiresAt = Number(registration.threeDsExpiresAt) * 1000;
    const remainingSeconds = Number.isFinite(expiresAt)
      ? Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000))
      : 180;
    const minutes = Math.floor(remainingSeconds / 60);
    return `${minutes}:${String(remainingSeconds % 60).padStart(2, '0')}`;
  }

  function showAuthorizationWait(registration) {
    loading.classList.add('hidden');
    formPanel.classList.add('hidden');
    resultPanel.classList.remove('hidden');
    const resultIcon = document.querySelector('#result-icon');
    resultIcon.className = 'result-icon waiting';
    resultIcon.textContent = '';
    document.querySelector('#result-title').textContent = 'Waiting for 3DS approval';
    document.querySelector('#result-message').textContent =
      `Approve this card registration in the admin 3DS simulator. Time remaining: ${remainingTime(registration)}.`;
    if (registrationPoll !== null) window.clearTimeout(registrationPoll);
    registrationPoll = window.setTimeout(refreshAuthorization, 1000);
  }

  async function refreshAuthorization() {
    registrationPoll = null;
    try {
      const registration = await request(endpoint + capabilityQuery);
      if (registration.status === 'pending' && registration.awaiting3ds === true) {
        showAuthorizationWait(registration);
        return;
      }
      showResult(registration.status);
    } catch (error) {
      showResult('failed');
    }
  }

  function renderProvider(registration) {
    const provider = registration.provider === 'barion' ? 'barion' : 'stripe';
    activeProvider = provider;
    document.body.classList.toggle('provider-barion', provider === 'barion');
    const providerLogo = document.querySelector('#provider-logo');
    providerLogo.src = `/simulator-ui/${provider}.svg`;
    providerLogo.alt = provider === 'barion' ? 'Barion' : 'Stripe';
  }

  async function generateTestCard() {
    generateButton.disabled = true;
    errorBox.classList.add('hidden');
    try {
      const card = await request(endpoint + '/generate-test-card' + capabilityQuery, {
        method: 'POST',
        body: '{}'
      });
      cardholderInput.value = card.cardholderName;
      numberInput.value = formatNumber(card.cardNumber);
      monthSelect.value = String(card.expiryMonth);
      yearSelect.value = String(card.expiryYear);
      securityCodeInput.value = card.securityCode;
      cardholderInput.focus();
    } catch (error) {
      showError(error.message);
    } finally {
      generateButton.disabled = false;
    }
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
      renderProvider(registration);
      if (registration.status !== 'pending') {
        showResult(registration.status);
        return;
      }
      if (registration.awaiting3ds === true) {
        showAuthorizationWait(registration);
        return;
      }
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
  generateButton.addEventListener('click', () => void generateTestCard());

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
          cardholderName: cardholderInput.value,
          securityCode: securityCodeInput.value
        })
      });
      numberInput.value = '';
      securityCodeInput.value = '';
      if (registration.status === 'pending' && registration.awaiting3ds === true) {
        showAuthorizationWait(registration);
      } else {
        showResult(registration.status);
      }
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
