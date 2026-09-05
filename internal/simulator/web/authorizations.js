(() => {
  const endpoint = '/authorization-session';
  const pending = document.querySelector('#pending');
  const message = document.querySelector('#message');
  const providerBadge = document.querySelector('#provider-badge');
  const modeBadge = document.querySelector('#mode-badge');
  let loading = false;
  let renderedState = '';

  function showMessage(text, error = false) {
    message.textContent = text;
    message.classList.toggle('error', error);
  }

  function formatAmount(item) {
    const amount = item.amountMinorUnit ? Number(item.amount) / 100 : Number(item.amount);
    try {
      return new Intl.NumberFormat(undefined, { style: 'currency', currency: item.currency }).format(amount);
    } catch {
      return `${amount.toLocaleString()} ${item.currency}`;
    }
  }

  function text(tag, value) {
    const node = document.createElement(tag);
    node.textContent = value;
    return node;
  }

  function row(label, value, list) {
    list.append(text('dt', label), text('dd', value));
  }

  function renderItem(item) {
    const article = document.createElement('article');
    article.className = 'authorization';
    const details = document.createElement('div');
    details.append(text('h2', `${item.provider.toUpperCase()} · ${formatAmount(item)}`));
    const list = document.createElement('dl');
    if (item.userReference) row('Member', item.userReference, list);
    row('Reference', item.reference || item.id, list);
    row('Created', new Date(Number(item.created) * 1000).toLocaleString(), list);
    details.append(list);
    const actions = document.createElement('div');
    actions.className = 'actions';
    const open = text('button', 'Open confirmation');
    open.addEventListener('click', () => {
      if (!item.reviewUrl) return;
      window.open(item.reviewUrl, '_blank', 'noopener,noreferrer');
    });
    actions.append(open);
    article.append(details, actions);
    return article;
  }

  function render(data) {
    const items = Array.isArray(data.pending) ? data.pending : [];
    const state = JSON.stringify({
      provider: data.provider,
      requires3ds: data.requires3ds,
      pending: items
    });
    if (state === renderedState) return;
    renderedState = state;
    providerBadge.textContent = data.provider === 'none' ? 'Cash only' : data.provider.toUpperCase();
    modeBadge.textContent = data.requires3ds ? '3DS required' : '3DS disabled';
    pending.replaceChildren();
    if (!items.length) {
      const empty = text('div', 'No payment is waiting for 3DS confirmation.');
      empty.className = 'empty';
      pending.append(empty);
      return;
    }
    pending.append(...items.map(renderItem));
  }

  async function request(url, options) {
    const response = await fetch(url, options);
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || 'The simulator rejected this request.');
    return body;
  }

  async function refresh() {
    if (loading) return;
    loading = true;
    try {
      render(await request(endpoint));
      showMessage('');
    } catch (error) {
      showMessage(error.message || 'Could not load pending confirmations.', true);
    } finally {
      loading = false;
    }
  }

  void refresh();
  window.setInterval(refresh, 1000);
})();
