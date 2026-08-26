(() => {
  'use strict';

  const $ = (selector, root = document) => root.querySelector(selector);
  const state = {
    query: '',
    page: 1,
    pageSize: 20,
    total: 0,
    totalPages: 0,
    facets: null,
    config: null,
    currentKey: null,
    currentDoc: null,
    currentStaging: false,
    aiResultKey: null,
    aiRunning: false,
    aiController: null,
    aiTimerHandle: null,
    aiStartedAt: 0,
    aiRunToken: 0,
  };

  const els = {
    brandTitle: $('#brandTitle'), brandSubtitle: $('#brandSubtitle'), countBadge: $('#countBadge'), modeBadge: $('#modeBadge'),
    hero: $('#hero'), heroSearchForm: $('#heroSearchForm'), heroSearch: $('#heroSearch'), quickLinks: $('#quickLinks'),
    resultsView: $('#resultsView'), topSearchForm: $('#topSearchForm'), topSearch: $('#topSearch'),
    resultCount: $('#resultCount'), resultHint: $('#resultHint'), clearSearch: $('#clearSearch'), sideFacets: $('#sideFacets'),
    loading: $('#loading'), noResults: $('#noResults'), noResultsHint: $('#noResultsHint'), resultList: $('#resultList'), pagination: $('#pagination'),
    aiFallbackPanel: $('#aiFallbackPanel'), aiTitle: $('#aiTitle'), aiStatus: $('#aiStatus'), aiProgress: $('#aiProgress'),
    aiTimer: $('#aiTimer'), aiNote: $('#aiNote'), openAIResult: $('#openAIResult'), retryAI: $('#retryAI'),
    articleDialog: $('#articleDialog'), closeArticle: $('#closeArticle'), articleEyebrow: $('#articleEyebrow'), stagingBadge: $('#stagingBadge'),
    articleTitle: $('#articleTitle'), articleMeta: $('#articleMeta'), problemSection: $('#problemSection'),
    articleProblem: $('#articleProblem'), answerSection: $('#answerSection'), articleAnswer: $('#articleAnswer'),
    tagsSection: $('#tagsSection'), articleTags: $('#articleTags'), sourceSection: $('#sourceSection'),
    articleSource: $('#articleSource'), articleSourceUri: $('#articleSourceUri'), sourceLink: $('#sourceLink'),
    articlePath: $('#articlePath'), copyAnswer: $('#copyAnswer'), copyLink: $('#copyLink'), toastHost: $('#toastHost'),
  };

  async function api(url, options = {}) {
    const headers = {'Accept': 'application/json', ...(options.headers || {})};
    const response = await fetch(url, {...options, headers});
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(body.error || `${response.status} ${response.statusText}`);
      error.status = response.status;
      error.body = body;
      throw error;
    }
    return body;
  }

  async function postJSON(url, data, options = {}) {
    return api(url, {
      method: 'POST',
      body: JSON.stringify(data),
      ...options,
      headers: {'Content-Type': 'application/json', ...(options.headers || {})},
    });
  }

  function escapeHTML(value) {
    return String(value ?? '').replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
  }

  function escapeRegex(value) {
    return String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  function highlighted(value, query = state.query) {
    let safe = escapeHTML(value);
    const terms = [...new Set(String(query).trim().split(/\s+/).filter(Boolean))]
      .sort((a, b) => b.length - a.length);
    if (!terms.length) return safe;
    const regex = new RegExp(`(${terms.map(escapeRegex).join('|')})`, 'gi');
    return safe.replace(regex, '<mark>$1</mark>');
  }

  function qs(params) {
    const out = new URLSearchParams();
    Object.entries(params).forEach(([key, value]) => {
      if (value !== '' && value !== null && value !== undefined) out.set(key, String(value));
    });
    return out.toString();
  }

  function updateURL({replace = false} = {}) {
    const params = new URLSearchParams();
    if (state.query) params.set('q', state.query);
    if (state.page > 1) params.set('page', String(state.page));
    if (state.currentKey) params.set(state.currentStaging ? 'staging' : 'doc', state.currentKey);
    const url = `${location.pathname}${params.toString() ? `?${params}` : ''}`;
    history[replace ? 'replaceState' : 'pushState']({}, '', url);
  }

  async function loadBootstrap() {
    try {
      const [config, health, facets] = await Promise.all([
        api('/api/config'), api('/api/health'), api('/api/facets?limit=10')
      ]);
      state.config = config;
      state.facets = facets;
      document.title = config.title || 'Helpdesk Search';
      els.brandTitle.textContent = config.title || 'Helpdesk Search';
      els.brandSubtitle.textContent = config.subtitle || 'Interne Wissenssuche für den Helpdesk';
      els.countBadge.textContent = `${Number(health.count || 0).toLocaleString('de-DE')} Wissenseinträge`;
      els.modeBadge.textContent = config.ai_fallback_enabled ? 'Nur lesen · KI-Fallback' : 'Nur lesen';
      renderFacets();
    } catch (error) {
      els.countBadge.textContent = 'Wissensbasis nicht erreichbar';
      toast(error.message, 'error');
    }
  }

  function renderFacets() {
    const categories = (state.facets?.categories?.length ? state.facets.categories : state.facets?.keywords) || [];
    els.quickLinks.innerHTML = '';
    els.sideFacets.innerHTML = '';
    categories.slice(0, 7).forEach((facet) => {
      const heroButton = document.createElement('button');
      heroButton.type = 'button';
      heroButton.className = 'quick-chip';
      heroButton.innerHTML = `<span>${escapeHTML(facet.name)}</span><small>${facet.count.toLocaleString('de-DE')}</small>`;
      heroButton.addEventListener('click', () => submitSearch(facet.name));
      els.quickLinks.appendChild(heroButton);

      const sideButton = document.createElement('button');
      sideButton.type = 'button';
      sideButton.className = 'facet-button';
      sideButton.innerHTML = `<span>${escapeHTML(facet.name)}</span><small>${facet.count.toLocaleString('de-DE')}</small>`;
      sideButton.addEventListener('click', () => submitSearch(facet.name));
      els.sideFacets.appendChild(sideButton);
    });
  }

  async function runSearch({allowAI = true} = {}) {
    const query = state.query.trim();
    if (!query) {
      showHome();
      return;
    }

    showResults();
    resetAIPanel({cancel: false});
    els.loading.classList.remove('hidden');
    els.noResults.classList.add('hidden');
    els.resultList.innerHTML = '';
    els.pagination.classList.add('hidden');

    try {
      const data = await api(`/api/search?${qs({q: query, page: state.page, page_size: state.pageSize})}`);
      if (query !== state.query.trim()) return;
      state.page = data.page || 1;
      state.total = data.total || 0;
      state.totalPages = data.total_pages || 0;
      renderResults(data.items || []);
      renderPagination();
      els.resultCount.textContent = `${state.total.toLocaleString('de-DE')} Treffer`;
      els.resultHint.textContent = `für „${query}“`;

      if (!state.total) {
        if (allowAI && state.config?.ai_fallback_enabled) {
          await runAIFallback(query);
        } else {
          els.noResults.classList.remove('hidden');
          els.noResultsHint.textContent = state.config?.ai_fallback_enabled
            ? 'Für diese URL wurde kein neuer KI-Entwurf gestartet. Ein vorhandener Staging-Entwurf kann direkt geöffnet werden.'
            : 'Versuche einen kürzeren Fehlercode, einen Produktnamen oder einzelne Wörter aus der Fehlermeldung.';
        }
      }
    } catch (error) {
      els.resultCount.textContent = 'Suche fehlgeschlagen';
      els.resultHint.textContent = '';
      toast(error.message, 'error');
    } finally {
      els.loading.classList.add('hidden');
    }
  }

  async function runAIFallback(query) {
    if (!state.config?.ai_fallback_enabled || state.aiRunning) return;

    const runToken = ++state.aiRunToken;
    state.aiRunning = true;
    state.aiResultKey = null;
    state.aiController?.abort();
    state.aiController = new AbortController();
    showAIPending();
    startAITimer();

    try {
      const generated = await postJSON('/api/ai/fallback', {query}, {signal: state.aiController.signal});
      if (runToken !== state.aiRunToken || query !== state.query.trim()) return;
      state.aiResultKey = generated.key;
      showAISuccess(generated);
      await openStaging(generated.key);
    } catch (error) {
      if (error.name === 'AbortError') return;
      if (runToken !== state.aiRunToken || query !== state.query.trim()) return;
      if (error.status === 409) {
        toast('Während der KI-Anfrage ist ein KB-Treffer verfügbar geworden. Die Suche wird aktualisiert.', 'success');
        await runSearch({allowAI: false});
        return;
      }
      showAIError(error.message);
    } finally {
      if (runToken === state.aiRunToken) {
        state.aiRunning = false;
        stopAITimer();
      }
    }
  }

  function showAIPending() {
    els.noResults.classList.add('hidden');
    els.aiFallbackPanel.classList.remove('hidden', 'ai-success', 'ai-error');
    els.aiFallbackPanel.classList.add('ai-pending');
    els.aiTitle.textContent = 'KI erstellt einen Helpdesk-Entwurf';
    const model = state.config?.ai_fallback_model ? ` (${state.config.ai_fallback_model})` : '';
    els.aiStatus.textContent = `Die interne Wissensbasis hat keinen Treffer. Ollama${model} erzeugt jetzt einen strukturierten Entwurf.`;
    els.aiNote.textContent = 'Der Entwurf wird getrennt von der produktiven KB gespeichert und muss geprüft werden.';
    els.aiProgress.classList.remove('hidden');
    els.openAIResult.classList.add('hidden');
    els.retryAI.classList.add('hidden');
  }

  function showAISuccess(generated) {
    els.aiFallbackPanel.classList.remove('ai-pending', 'ai-error');
    els.aiFallbackPanel.classList.add('ai-success');
    els.aiTitle.textContent = 'KI-Entwurf im Staging gespeichert';
    const seconds = Math.max(0, Number(generated.duration_ms || 0) / 1000);
    els.aiStatus.textContent = `Der Entwurf wurde nach ${seconds.toLocaleString('de-DE', {maximumFractionDigits: 1})} Sekunden erzeugt und als ${generated.key} abgelegt.`;
    els.aiNote.textContent = 'AI-Staging ist ungeprüft und bleibt von der produktiven Wissensbasis getrennt.';
    els.aiProgress.classList.add('hidden');
    els.openAIResult.classList.remove('hidden');
    els.retryAI.classList.add('hidden');
  }

  function showAIError(message) {
    els.aiFallbackPanel.classList.remove('ai-pending', 'ai-success');
    els.aiFallbackPanel.classList.add('ai-error');
    els.aiTitle.textContent = 'KI-Fallback konnte keinen Entwurf liefern';
    els.aiStatus.textContent = message || 'Unbekannter Fehler bei der Ollama-Anfrage.';
    els.aiNote.textContent = 'Die normale Wissensbasis wurde nicht verändert.';
    els.aiProgress.classList.add('hidden');
    els.openAIResult.classList.add('hidden');
    els.retryAI.classList.remove('hidden');
    els.noResults.classList.remove('hidden');
  }

  function startAITimer() {
    stopAITimer();
    state.aiStartedAt = Date.now();
    updateAITimer();
    state.aiTimerHandle = setInterval(updateAITimer, 1000);
  }

  function updateAITimer() {
    const elapsed = Math.floor((Date.now() - state.aiStartedAt) / 1000);
    const minutes = Math.floor(elapsed / 60);
    const seconds = elapsed % 60;
    const max = Number(state.config?.ai_fallback_timeout_seconds || 600);
    els.aiTimer.textContent = `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')} / ${formatDuration(max)}`;
  }

  function formatDuration(seconds) {
    const minutes = Math.floor(seconds / 60);
    const rest = Math.floor(seconds % 60);
    return `${String(minutes).padStart(2, '0')}:${String(rest).padStart(2, '0')}`;
  }

  function stopAITimer() {
    if (state.aiTimerHandle) clearInterval(state.aiTimerHandle);
    state.aiTimerHandle = null;
  }

  function resetAIPanel({cancel = true} = {}) {
    if (cancel && state.aiController) state.aiController.abort();
    if (cancel) state.aiRunToken++;
    state.aiRunning = false;
    state.aiController = null;
    stopAITimer();
    els.aiFallbackPanel.classList.add('hidden');
    els.aiFallbackPanel.classList.remove('ai-pending', 'ai-success', 'ai-error');
    els.aiProgress.classList.remove('hidden');
    els.openAIResult.classList.add('hidden');
    els.retryAI.classList.add('hidden');
    els.aiTimer.textContent = '00:00';
  }

  function renderResults(items) {
    els.resultList.innerHTML = '';
    for (const item of items) {
      const card = document.createElement('article');
      card.className = 'result-card';
      const tags = [...(item.categories || []), ...(item.keywords || [])].slice(0, 4);
      card.innerHTML = `
        <button class="result-main" type="button">
          <div class="result-overline">
            <span class="result-id">${highlighted(item.id || item.rel_path)}</span>
            ${item.source ? `<span class="result-source">${escapeHTML(item.source)}</span>` : ''}
          </div>
          <h2>${highlighted(item.title || '(ohne Titel)')}</h2>
          ${item.excerpt ? `<p>${highlighted(item.excerpt)}</p>` : '<p class="muted">Kein Beschreibungstext hinterlegt.</p>'}
          <div class="result-tags">${tags.map(tag => `<span>${highlighted(tag)}</span>`).join('')}</div>
        </button>
        <div class="result-arrow" aria-hidden="true">→</div>`;
      $('.result-main', card).addEventListener('click', () => openArticle(item.key));
      card.addEventListener('dblclick', () => openArticle(item.key));
      els.resultList.appendChild(card);
    }
  }

  function renderPagination() {
    els.pagination.innerHTML = '';
    if (state.totalPages <= 1) {
      els.pagination.classList.add('hidden');
      return;
    }
    els.pagination.classList.remove('hidden');

    const add = (label, page, {active = false, disabled = false, aria = ''} = {}) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = label;
      button.className = `page-btn${active ? ' active' : ''}`;
      button.disabled = disabled;
      if (aria) button.setAttribute('aria-label', aria);
      button.addEventListener('click', () => goPage(page));
      els.pagination.appendChild(button);
    };

    add('‹', state.page - 1, {disabled: state.page <= 1, aria: 'Vorherige Seite'});
    const pages = pageWindow(state.page, state.totalPages);
    let previous = 0;
    pages.forEach(page => {
      if (previous && page - previous > 1) {
        const gap = document.createElement('span');
        gap.className = 'page-gap';
        gap.textContent = '…';
        els.pagination.appendChild(gap);
      }
      add(String(page), page, {active: page === state.page, aria: `Seite ${page}`});
      previous = page;
    });
    add('›', state.page + 1, {disabled: state.page >= state.totalPages, aria: 'Nächste Seite'});
  }

  function pageWindow(current, total) {
    const candidates = new Set([1, total]);
    for (let page = current - 2; page <= current + 2; page++) {
      if (page >= 1 && page <= total) candidates.add(page);
    }
    return [...candidates].sort((a, b) => a - b);
  }

  function goPage(page) {
    if (page < 1 || page > state.totalPages || page === state.page) return;
    state.page = page;
    state.currentKey = null;
    state.currentStaging = false;
    updateURL();
    runSearch();
    window.scrollTo({top: 0, behavior: 'smooth'});
  }

  function submitSearch(value) {
    const query = String(value ?? '').trim();
    if (!query) return;
    resetAIPanel({cancel: true});
    state.query = query;
    state.page = 1;
    state.currentKey = null;
    state.currentStaging = false;
    state.aiResultKey = null;
    els.heroSearch.value = query;
    els.topSearch.value = query;
    updateURL();
    runSearch();
  }

  function showHome() {
    resetAIPanel({cancel: true});
    state.query = '';
    state.page = 1;
    state.currentKey = null;
    state.currentStaging = false;
    state.aiResultKey = null;
    els.hero.classList.remove('hidden');
    els.resultsView.classList.add('hidden');
    els.heroSearch.value = '';
    updateURL({replace: true});
    setTimeout(() => els.heroSearch.focus(), 0);
  }

  function showResults() {
    els.hero.classList.add('hidden');
    els.resultsView.classList.remove('hidden');
    els.topSearch.value = state.query;
  }

  async function openArticle(key, {updateHistory = true} = {}) {
    try {
      const data = await api(`/api/items/${encodeURIComponent(key)}`);
      state.currentKey = key;
      state.currentStaging = false;
      state.currentDoc = data.document || {};
      renderArticle(state.currentDoc, data.meta || {});
      if (updateHistory) updateURL();
      if (!els.articleDialog.open) els.articleDialog.showModal();
    } catch (error) {
      toast(error.message, 'error');
    }
  }

  async function openStaging(key, {updateHistory = true} = {}) {
    try {
      const data = await api(`/api/staging/${encodeURIComponent(key)}`);
      state.aiResultKey = key;
      state.currentKey = key;
      state.currentStaging = true;
      state.currentDoc = data.document || {};
      renderArticle(state.currentDoc, data.meta || {staging: true});
      if (updateHistory) updateURL();
      if (!els.articleDialog.open) els.articleDialog.showModal();
    } catch (error) {
      toast(error.message, 'error');
    }
  }

  function renderArticle(doc, meta) {
    const isStaging = Boolean(meta.staging);
    els.stagingBadge.classList.toggle('hidden', !isStaging);
    els.articleEyebrow.textContent = doc.id || meta.rel_path || 'Wissensartikel';
    els.articleTitle.textContent = doc.title || '(ohne Titel)';
    els.articleProblem.textContent = doc.text || '';
    els.articleAnswer.textContent = doc.answer || '';
    els.problemSection.classList.toggle('hidden', !doc.text);
    els.answerSection.classList.toggle('hidden', !doc.answer);

    const metaParts = [];
    if (isStaging) metaParts.push('AI-STAGING / ungeprüft');
    if (doc.language) metaParts.push(doc.language);
    if (doc.communication_style) metaParts.push(doc.communication_style);
    if (typeof doc.auto_reply === 'boolean') metaParts.push(`auto_reply: ${doc.auto_reply}`);
    if (doc.min_score !== undefined && doc.min_score !== null) metaParts.push(`min_score: ${doc.min_score}`);
    els.articleMeta.innerHTML = metaParts.map(value => `<span>${escapeHTML(value)}</span>`).join('');

    const tags = [...new Set([...(Array.isArray(doc.categories) ? doc.categories : []), ...(Array.isArray(doc.keywords) ? doc.keywords : [])])];
    els.articleTags.innerHTML = tags.map(tag => `<span>${escapeHTML(tag)}</span>`).join('');
    els.tagsSection.classList.toggle('hidden', tags.length === 0);

    const source = String(doc.source || '').trim();
    const sourceURI = safeURL(doc.source_uri);
    els.articleSource.textContent = source || 'Quelle';
    els.articleSourceUri.textContent = sourceURI || '';
    els.sourceSection.classList.toggle('hidden', !source && !sourceURI);
    els.sourceLink.classList.toggle('hidden', !sourceURI);
    if (sourceURI) els.sourceLink.href = sourceURI;
    else els.sourceLink.removeAttribute('href');

    els.articlePath.textContent = meta.rel_path || '';
  }

  function safeURL(value) {
    const raw = String(value || '').trim();
    if (!raw) return '';
    try {
      const parsed = new URL(raw);
      return ['http:', 'https:'].includes(parsed.protocol) ? parsed.href : '';
    } catch (_) {
      return '';
    }
  }

  function closeArticle({updateHistory = true} = {}) {
    if (els.articleDialog.open) els.articleDialog.close();
    state.currentKey = null;
    state.currentDoc = null;
    state.currentStaging = false;
    if (updateHistory) updateURL({replace: true});
  }

  async function copyText(text, message) {
    try {
      await navigator.clipboard.writeText(text);
      toast(message, 'success');
    } catch (_) {
      toast('Kopieren wurde vom Browser blockiert.', 'error');
    }
  }

  function toast(message, type = '') {
    const el = document.createElement('div');
    el.className = `toast ${type}`;
    el.textContent = message;
    els.toastHost.appendChild(el);
    setTimeout(() => el.remove(), 4200);
  }

  function bindEvents() {
    els.heroSearchForm.addEventListener('submit', event => {
      event.preventDefault();
      submitSearch(els.heroSearch.value);
    });
    els.topSearchForm.addEventListener('submit', event => {
      event.preventDefault();
      submitSearch(els.topSearch.value);
    });
    els.clearSearch.addEventListener('click', showHome);
    els.closeArticle.addEventListener('click', () => closeArticle());
    els.articleDialog.addEventListener('click', event => {
      if (event.target === els.articleDialog) closeArticle();
    });
    els.articleDialog.addEventListener('cancel', event => {
      event.preventDefault();
      closeArticle();
    });
    els.copyAnswer.addEventListener('click', () => copyText(String(state.currentDoc?.answer || ''), 'Antwort kopiert.'));
    els.copyLink.addEventListener('click', () => copyText(location.href, 'Artikellink kopiert.'));
    els.openAIResult.addEventListener('click', () => {
      if (state.aiResultKey) openStaging(state.aiResultKey);
    });
    els.retryAI.addEventListener('click', () => runAIFallback(state.query.trim()));

    document.addEventListener('keydown', event => {
      if (event.key === '/' && !['INPUT', 'TEXTAREA'].includes(document.activeElement?.tagName)) {
        event.preventDefault();
        (state.query ? els.topSearch : els.heroSearch).focus();
      }
    });

    window.addEventListener('popstate', () => hydrateFromURL({historyNavigation: true}));
  }

  async function hydrateFromURL({historyNavigation = false} = {}) {
    resetAIPanel({cancel: true});
    const params = new URLSearchParams(location.search);
    state.query = (params.get('q') || '').trim();
    state.page = Math.max(1, Number.parseInt(params.get('page') || '1', 10) || 1);
    const docKey = params.get('doc') || '';
    const stagingKey = params.get('staging') || '';

    if (state.query) {
      els.heroSearch.value = state.query;
      els.topSearch.value = state.query;
      await runSearch({allowAI: !stagingKey});
    } else {
      showHome();
    }

    if (stagingKey) await openStaging(stagingKey, {updateHistory: false});
    else if (docKey) await openArticle(docKey, {updateHistory: false});
    else if (historyNavigation && els.articleDialog.open) closeArticle({updateHistory: false});
  }

  async function init() {
    bindEvents();
    await loadBootstrap();
    await hydrateFromURL();
  }

  init();
})();
