(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  const state = {
    scope: 'production',
    page: 1,
    pageSize: 60,
    total: 0,
    totalPages: 0,
    items: [],
    selected: new Set(),
    currentKey: null,
    currentDoc: null,
    currentMeta: null,
    dirty: false,
    activeTab: 'form',
    lastBulkPreviewSignature: '',
  };

  const els = {
    brandTitle: $('#brandTitle'), brandSubtitle: $('#brandSubtitle'), healthPill: $('#healthPill'), reloadBtn: $('#reloadBtn'), bulkBtn: $('#bulkBtn'),
    scopeProduction: $('#scopeProduction'), scopeStaging: $('#scopeStaging'), stagingCountBadge: $('#stagingCountBadge'),
    searchInput: $('#searchInput'), autoReplyFilter: $('#autoReplyFilter'), languageFilter: $('#languageFilter'),
    sourceFilter: $('#sourceFilter'), styleFilter: $('#styleFilter'), selectPage: $('#selectPage'),
    selectionCount: $('#selectionCount'), resultList: $('#resultList'), prevPage: $('#prevPage'), nextPage: $('#nextPage'),
    pageLabel: $('#pageLabel'), totalLabel: $('#totalLabel'), emptyState: $('#emptyState'), editor: $('#editor'),
    filePath: $('#filePath'), dirtyBadge: $('#dirtyBadge'), stagingBadge: $('#stagingBadge'), saveBtn: $('#saveBtn'), formatJsonBtn: $('#formatJsonBtn'),
    deleteStagingBtn: $('#deleteStagingBtn'), promoteStagingBtn: $('#promoteStagingBtn'),
    formTab: $('#formTab'), rawTab: $('#rawTab'), rawEditor: $('#rawEditor'), rawError: $('#rawError'),
    bulkDialog: $('#bulkDialog'), bulkTargetText: $('#bulkTargetText'), bulkAllMatching: $('#bulkAllMatching'),
    allMatchingHint: $('#allMatchingHint'), bulkPreview: $('#bulkPreview'), previewBulkBtn: $('#previewBulkBtn'),
    applyBulkBtn: $('#applyBulkBtn'), stagingBulkDialog: $('#stagingBulkDialog'), stagingBulkTargetText: $('#stagingBulkTargetText'),
    stagingBulkResult: $('#stagingBulkResult'), bulkDeleteStagingBtn: $('#bulkDeleteStagingBtn'), bulkPromoteStagingBtn: $('#bulkPromoteStagingBtn'),
    toastHost: $('#toastHost')
  };

  let searchTimer;

  async function api(url, options = {}) {
    const res = await fetch(url, options);
    const contentType = res.headers.get('content-type') || '';
    const body = contentType.includes('application/json') ? await res.json() : await res.text();
    if (!res.ok) {
      const msg = typeof body === 'object' && body?.error ? body.error : `${res.status} ${res.statusText}`;
      throw new Error(msg);
    }
    return body;
  }

  function currentQuery(page = state.page) {
    return {
      q: els.searchInput.value.trim(),
      auto_reply: els.autoReplyFilter.value,
      language: els.languageFilter.value.trim(),
      communication_style: els.styleFilter.value.trim(),
      source: els.sourceFilter.value.trim(),
      page,
      page_size: state.pageSize,
    };
  }

  function queryString(q) {
    const p = new URLSearchParams();
    Object.entries(q).forEach(([k, v]) => {
      if (v !== '' && v !== null && v !== undefined && !(k === 'auto_reply' && v === 'any')) p.set(k, v);
    });
    return p.toString();
  }

  async function loadHealth() {
    try {
      const [h, config] = await Promise.all([api('/api/health'), api('/api/config')]);
      if (config.title) {
        els.brandTitle.textContent = config.title;
        document.title = config.title;
      }
      if (config.subtitle) els.brandSubtitle.textContent = config.subtitle;
      const stagingCount = Number(h.staging_count || 0);
      els.stagingCountBadge.textContent = stagingCount.toLocaleString('de-DE');
      els.healthPill.textContent = `${h.count.toLocaleString('de-DE')} produktiv · ${stagingCount.toLocaleString('de-DE')} Staging`;
      els.healthPill.className = 'pill ok';
      els.healthPill.title = `Daten: ${h.data_dir}\nStaging: ${h.staging_dir || '–'}\nBackups: ${h.backup_dir || '–'}`;
    } catch (err) {
      els.healthPill.textContent = 'Offline';
      els.healthPill.className = 'pill';
      toast(err.message, 'error');
    }
  }

  async function loadList(resetPage = false) {
    if (resetPage) state.page = 1;
    els.resultList.innerHTML = '<div class="muted-text" style="padding:16px">Lade …</div>';
    try {
      const endpoint = state.scope === 'staging' ? '/api/staging' : '/api/items';
      const data = await api(`${endpoint}?${queryString(currentQuery())}`);
      state.page = data.page || 1;
      state.total = data.total;
      state.totalPages = data.total_pages;
      state.items = data.items || [];
      renderList();
    } catch (err) {
      els.resultList.innerHTML = `<div class="inline-error" style="margin:12px">${escapeHTML(err.message)}</div>`;
    }
  }

  function renderList() {
    els.resultList.innerHTML = '';
    if (state.items.length === 0) {
      els.resultList.innerHTML = '<div class="muted-text" style="padding:20px;text-align:center">Keine Treffer.</div>';
    }
    for (const item of state.items) {
      const row = document.createElement('div');
      row.className = `result-item${item.key === state.currentKey ? ' active' : ''}${state.scope === 'staging' ? ' staging-item' : ''}`;
      row.dataset.key = item.key;
      const checked = state.selected.has(item.key) ? 'checked' : '';
      const auto = item.auto_reply === true;
      row.innerHTML = `
        <input class="result-check" type="checkbox" ${checked} aria-label="Auswählen">
        <div>
          <div class="result-id">${state.scope === 'staging' ? '<span class="mini-staging">STAGING</span> ' : ''}${escapeHTML(item.id || item.rel_path)}</div>
          <div class="result-title">${escapeHTML(item.title || '(ohne Titel)')}</div>
          <div class="result-meta">
            <span><i class="bool-dot ${auto ? 'true' : ''}"></i>auto ${String(item.auto_reply ?? '–')}</span>
            <span>score ${item.min_score ?? '–'}</span>
            <span>${escapeHTML(item.language || '–')}</span>
            <span>${escapeHTML(item.source || '')}</span>
          </div>
        </div>`;
      const cb = $('.result-check', row);
      cb.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleSelection(item.key, cb.checked);
      });
      row.addEventListener('click', () => openItem(item.key));
      els.resultList.appendChild(row);
    }
    els.pageLabel.textContent = state.totalPages ? `Seite ${state.page} / ${state.totalPages}` : 'Seite 0 / 0';
    els.totalLabel.textContent = `${state.total.toLocaleString('de-DE')} Treffer`;
    els.prevPage.disabled = state.page <= 1;
    els.nextPage.disabled = state.totalPages === 0 || state.page >= state.totalPages;
    els.selectPage.checked = state.items.length > 0 && state.items.every(i => state.selected.has(i.key));
    els.selectPage.indeterminate = !els.selectPage.checked && state.items.some(i => state.selected.has(i.key));
    updateSelectionUI();
  }

  function toggleSelection(key, checked) {
    if (checked) state.selected.add(key); else state.selected.delete(key);
    renderSelectionOnly();
  }

  function renderSelectionOnly() {
    els.selectionCount.textContent = `${state.selected.size.toLocaleString('de-DE')} ausgewählt`;
    els.bulkBtn.disabled = state.scope === 'staging' ? state.selected.size === 0 : (state.selected.size === 0 && state.total === 0);
    els.bulkBtn.textContent = state.scope === 'staging' ? '✦ Staging-Aktionen' : '✦ Massenbearbeitung';
    els.selectPage.checked = state.items.length > 0 && state.items.every(i => state.selected.has(i.key));
    els.selectPage.indeterminate = !els.selectPage.checked && state.items.some(i => state.selected.has(i.key));
  }

  function updateSelectionUI() { renderSelectionOnly(); }

  async function openItem(key) {
    if (key === state.currentKey) return;
    if (state.dirty && !confirm('Es gibt ungespeicherte Änderungen. Wirklich einen anderen Eintrag öffnen?')) return;
    try {
      const endpoint = state.scope === 'staging' ? '/api/staging' : '/api/items';
      const data = await api(`${endpoint}/${encodeURIComponent(key)}`);
      state.currentKey = key;
      state.currentDoc = data.document;
      state.currentMeta = data.meta;
      state.dirty = false;
      showEditor();
      fillForm();
      setTab('form');
      renderList();
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  function showEditor() {
    els.emptyState.classList.add('hidden');
    els.editor.classList.remove('hidden');
    els.filePath.textContent = state.currentMeta?.rel_path || '–';
    const isStaging = state.scope === 'staging';
    els.stagingBadge.classList.toggle('hidden', !isStaging);
    els.promoteStagingBtn.classList.toggle('hidden', !isStaging);
    els.deleteStagingBtn.classList.toggle('hidden', !isStaging);
    setDirty(false);
  }

  function fillForm() {
    $$('[data-field]').forEach(input => {
      const name = input.dataset.field;
      const val = state.currentDoc?.[name];
      if (input.type === 'checkbox') input.checked = Boolean(val);
      else input.value = val ?? '';
    });
    $$('[data-list-field]').forEach(input => {
      const val = state.currentDoc?.[input.dataset.listField];
      input.value = Array.isArray(val) ? val.join('\n') : '';
    });
    els.rawEditor.value = JSON.stringify(state.currentDoc, null, 2);
    clearRawError();
  }

  function syncFormToDoc() {
    if (!state.currentDoc) return;
    $$('[data-field]').forEach(input => {
      const name = input.dataset.field;
      if (input.type === 'checkbox') state.currentDoc[name] = input.checked;
      else if (input.type === 'number') {
        if (input.value === '') delete state.currentDoc[name];
        else state.currentDoc[name] = Number(input.value);
      } else state.currentDoc[name] = input.value;
    });
    $$('[data-list-field]').forEach(input => {
      state.currentDoc[input.dataset.listField] = lines(input.value);
    });
  }

  function syncRawToDoc() {
    try {
      const parsed = JSON.parse(els.rawEditor.value);
      if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('Die JSON-Wurzel muss ein Objekt sein.');
      state.currentDoc = parsed;
      clearRawError();
      return true;
    } catch (err) {
      els.rawError.textContent = `JSON-Fehler: ${err.message}`;
      els.rawError.classList.remove('hidden');
      return false;
    }
  }

  function clearRawError() {
    els.rawError.textContent = '';
    els.rawError.classList.add('hidden');
  }

  function setDirty(v = true) {
    state.dirty = v;
    els.dirtyBadge.classList.toggle('hidden', !v);
  }

  function setTab(tab) {
    if (tab === state.activeTab) return;
    if (state.activeTab === 'raw' && tab === 'form') {
      if (!syncRawToDoc()) return;
      fillForm();
    } else if (state.activeTab === 'form' && tab === 'raw') {
      syncFormToDoc();
      els.rawEditor.value = JSON.stringify(state.currentDoc, null, 2);
    }
    state.activeTab = tab;
    $$('.tab').forEach(b => b.classList.toggle('active', b.dataset.tab === tab));
    els.formTab.classList.toggle('active', tab === 'form');
    els.rawTab.classList.toggle('active', tab === 'raw');
    els.formatJsonBtn.classList.toggle('hidden', tab !== 'raw');
  }

  async function saveCurrent() {
    if (!state.currentKey || !state.currentDoc) return false;
    if (state.activeTab === 'raw') {
      if (!syncRawToDoc()) return false;
    } else syncFormToDoc();

    els.saveBtn.disabled = true;
    try {
      const endpoint = state.scope === 'staging' ? '/api/staging' : '/api/items';
      const result = await api(`${endpoint}/${encodeURIComponent(state.currentKey)}`, {
        method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(state.currentDoc)
      });
      state.currentMeta = result.meta;
      if (result.document) state.currentDoc = result.document;
      setDirty(false);
      if (state.scope === 'staging') toast('Staging-Entwurf gespeichert.', 'success');
      else toast(`Gespeichert. Backup: ${shortPath(result.backup)}`, 'success');
      await loadList(false);
      return true;
    } catch (err) {
      toast(err.message, 'error');
      return false;
    } finally {
      els.saveBtn.disabled = false;
    }
  }

  function openBulk() {
    if (state.scope === 'staging') {
      if (state.selected.size === 0) return;
      els.stagingBulkTargetText.textContent = `${state.selected.size.toLocaleString('de-DE')} ausgewählte Staging-Entwürfe`;
      els.stagingBulkResult.className = 'preview-box hidden';
      els.stagingBulkResult.textContent = '';
      els.stagingBulkDialog.showModal();
      return;
    }
    if (state.selected.size === 0 && state.total === 0) return;
    resetBulkPreview();
    els.bulkAllMatching.checked = state.selected.size === 0;
    updateBulkTargetText();
    els.bulkDialog.showModal();
  }

  function updateBulkTargetText() {
    const all = els.bulkAllMatching.checked;
    els.bulkTargetText.textContent = all
      ? `${state.total.toLocaleString('de-DE')} aktuelle Treffer als Ziel`
      : `${state.selected.size.toLocaleString('de-DE')} explizit ausgewählte Dateien als Ziel`;
    els.allMatchingHint.textContent = `Aktueller Filter: ${state.total.toLocaleString('de-DE')} Treffer`;
  }

  function buildPatch() {
    const patch = {};
    const enabled = id => $(`[data-enable="${id}"]`)?.checked;
    if (enabled('bulkAutoReply')) patch.set_auto_reply = $('#bulkAutoReply').value === 'true';
    if (enabled('bulkMinScore')) patch.set_min_score = Number($('#bulkMinScore').value);
    if (enabled('bulkLanguage')) patch.set_language = $('#bulkLanguage').value;
    if (enabled('bulkStyle')) patch.set_communication_style = $('#bulkStyle').value;
    if (enabled('bulkSource')) patch.set_source = $('#bulkSource').value;
    if (enabled('bulkSourceUri')) patch.set_source_uri = $('#bulkSourceUri').value;

    const addK = lines($('#addKeywords').value), rmK = lines($('#removeKeywords').value);
    const addC = lines($('#addCategories').value), rmC = lines($('#removeCategories').value);
    if (addK.length) patch.add_keywords = addK;
    if (rmK.length) patch.remove_keywords = rmK;
    if (addC.length) patch.add_categories = addC;
    if (rmC.length) patch.remove_categories = rmC;

    const find = $('#replaceFind').value;
    if (find) {
      patch.find_replace = {
        fields: $$('input[name="replaceField"]:checked').map(x => x.value),
        find,
        replace: $('#replaceWith').value,
        regex: $('#replaceRegex').checked,
        case_sensitive: $('#replaceCase').checked,
      };
    }
    return patch;
  }

  function buildBulkRequest(dryRun) {
    const q = currentQuery(1);
    q.page = 0; q.page_size = 0;
    return {
      keys: Array.from(state.selected),
      all_matching: els.bulkAllMatching.checked,
      query: q,
      patch: buildPatch(),
      dry_run: dryRun,
    };
  }

  async function previewBulk() {
    const req = buildBulkRequest(true);
    if (!Object.keys(req.patch).length) {
      toast('Bitte mindestens eine Änderung festlegen.', 'error');
      return;
    }
    els.previewBulkBtn.disabled = true;
    try {
      const result = await api('/api/bulk', {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(req)
      });
      state.lastBulkPreviewSignature = signatureFor(req);
      renderBulkPreview(result);
      els.applyBulkBtn.disabled = result.changed === 0;
    } catch (err) {
      els.bulkPreview.textContent = err.message;
      els.bulkPreview.className = 'preview-box warn';
      els.applyBulkBtn.disabled = true;
    } finally {
      els.previewBulkBtn.disabled = false;
    }
  }

  function renderBulkPreview(result) {
    els.bulkPreview.className = `preview-box ${result.changed > 0 ? 'ok' : 'warn'}`;
    els.bulkPreview.innerHTML = `
      <strong>Vorschau:</strong> ${result.changed.toLocaleString('de-DE')} von ${result.targeted.toLocaleString('de-DE')} Dateien würden geändert,
      ${result.skipped.toLocaleString('de-DE')} bleiben unverändert.
      ${result.sample?.length ? `<div class="preview-samples">${result.sample.map(x => `<code>${escapeHTML(x.id || x.rel_path)} · ${escapeHTML(x.title || '')}</code>`).join('')}</div>` : ''}`;
  }

  async function applyBulk() {
    const req = buildBulkRequest(false);
    const sig = signatureFor({...req, dry_run: true});
    if (sig !== state.lastBulkPreviewSignature) {
      toast('Die Massenänderung wurde seit der Vorschau verändert. Bitte erneut Vorschau ausführen.', 'error');
      els.applyBulkBtn.disabled = true;
      return;
    }
    if (!confirm('Massenänderung jetzt wirklich auf die Zieldateien anwenden?')) return;
    els.applyBulkBtn.disabled = true;
    try {
      const result = await api('/api/bulk', {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(req)
      });
      toast(`${result.changed.toLocaleString('de-DE')} Dateien geändert. Backup: ${shortPath(result.backup)}`, 'success');
      els.bulkDialog.close();
      state.selected.clear();
      state.currentKey = null; state.currentDoc = null; state.currentMeta = null; state.dirty = false;
      els.editor.classList.add('hidden'); els.emptyState.classList.remove('hidden');
      await Promise.all([loadList(true), loadHealth()]);
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  async function promoteCurrentStaging() {
    if (state.scope !== 'staging' || !state.currentKey) return;
    if (state.dirty) {
      if (!confirm('Der Entwurf enthält ungespeicherte Änderungen. Vor der Freigabe speichern?')) return;
      if (!await saveCurrent()) return;
    }
    if (!confirm('Diesen Staging-Entwurf jetzt unverändert in die produktive Wissensbasis freigeben?')) return;
    els.promoteStagingBtn.disabled = true;
    try {
      const result = await api(`/api/staging/${encodeURIComponent(state.currentKey)}/promote`, {
        method: 'POST', headers: {'Content-Type':'application/json'}, body: '{}'
      });
      const productionKey = result.production?.key;
      toast(`Freigegeben: ${result.production?.rel_path || result.production?.id || 'Produktivartikel'}`, 'success');
      state.currentKey = null; state.currentDoc = null; state.currentMeta = null; state.dirty = false; state.selected.clear();
      await loadHealth();
      await setScope('production');
      if (productionKey) await openItem(productionKey);
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      els.promoteStagingBtn.disabled = false;
    }
  }

  async function deleteCurrentStaging() {
    if (state.scope !== 'staging' || !state.currentKey) return;
    if (!confirm('Diesen Staging-Entwurf löschen? Er wird zur Sicherheit nach staging/.trash verschoben.')) return;
    els.deleteStagingBtn.disabled = true;
    try {
      await api(`/api/staging/${encodeURIComponent(state.currentKey)}`, {method: 'DELETE'});
      toast('Staging-Entwurf gelöscht und in .trash archiviert.', 'success');
      state.selected.delete(state.currentKey);
      state.currentKey = null; state.currentDoc = null; state.currentMeta = null; state.dirty = false;
      els.editor.classList.add('hidden'); els.emptyState.classList.remove('hidden');
      await Promise.all([loadList(true), loadHealth()]);
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      els.deleteStagingBtn.disabled = false;
    }
  }

  async function stagingBulkAction(action) {
    if (state.scope !== 'staging' || state.selected.size === 0) return;
    const verb = action === 'promote' ? 'freigeben' : 'löschen';
    if (!confirm(`${state.selected.size.toLocaleString('de-DE')} Staging-Entwürfe wirklich ${verb}?`)) return;
    els.bulkPromoteStagingBtn.disabled = true;
    els.bulkDeleteStagingBtn.disabled = true;
    try {
      const result = await api('/api/staging/bulk', {
        method: 'POST', headers: {'Content-Type':'application/json'},
        body: JSON.stringify({keys: Array.from(state.selected), action})
      });
      els.stagingBulkResult.className = `preview-box ${result.failed ? 'warn' : 'ok'}`;
      els.stagingBulkResult.innerHTML = `<strong>${result.succeeded.toLocaleString('de-DE')} erfolgreich</strong> · ${result.failed.toLocaleString('de-DE')} fehlgeschlagen` +
        (result.failed ? `<div class="preview-samples">${result.items.filter(x => !x.ok).slice(0,20).map(x => `<code>${escapeHTML(x.key)} · ${escapeHTML(x.error)}</code>`).join('')}</div>` : '');
      state.selected.clear();
      state.currentKey = null; state.currentDoc = null; state.currentMeta = null; state.dirty = false;
      els.editor.classList.add('hidden'); els.emptyState.classList.remove('hidden');
      await Promise.all([loadList(true), loadHealth()]);
      if (!result.failed) setTimeout(() => els.stagingBulkDialog.close(), 650);
    } catch (err) {
      els.stagingBulkResult.className = 'preview-box warn';
      els.stagingBulkResult.textContent = err.message;
    } finally {
      els.bulkPromoteStagingBtn.disabled = false;
      els.bulkDeleteStagingBtn.disabled = false;
    }
  }

  async function setScope(scope) {
    if (scope !== 'production' && scope !== 'staging') return;
    if (scope === state.scope) return;
    if (state.dirty && !confirm('Ungespeicherte Änderungen verwerfen und Bereich wechseln?')) return;
    state.scope = scope;
    state.page = 1;
    state.selected.clear();
    state.currentKey = null; state.currentDoc = null; state.currentMeta = null; state.dirty = false;
    els.scopeProduction.classList.toggle('active', scope === 'production');
    els.scopeStaging.classList.toggle('active', scope === 'staging');
    els.editor.classList.add('hidden'); els.emptyState.classList.remove('hidden');
    els.emptyState.querySelector('h1').textContent = scope === 'staging' ? 'Staging-Entwürfe prüfen' : 'JSON-Wissensbasis bearbeiten';
    els.emptyState.querySelector('p').textContent = scope === 'staging'
      ? 'KI-generierte Entwürfe prüfen, bearbeiten und anschließend gezielt freigeben oder verwerfen.'
      : 'Wähle links einen Eintrag aus oder markiere mehrere Dateien für eine Massenänderung.';
    renderSelectionOnly();
    await loadList(true);
  }

  function resetBulkPreview() {
    state.lastBulkPreviewSignature = '';
    els.bulkPreview.className = 'preview-box hidden';
    els.bulkPreview.textContent = '';
    els.applyBulkBtn.disabled = true;
  }

  function signatureFor(obj) { return JSON.stringify(obj); }
  function lines(s) { return s.split(/\r?\n/).map(x => x.trim()).filter(Boolean); }
  function shortPath(p) { if (!p) return '–'; const parts = p.split('/'); return parts.slice(-2).join('/'); }
  function escapeHTML(s) { return String(s ?? '').replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c])); }

  function toast(message, type = '') {
    const el = document.createElement('div');
    el.className = `toast ${type}`;
    el.textContent = message;
    els.toastHost.appendChild(el);
    setTimeout(() => el.remove(), 5000);
  }

  function debounceReload() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => loadList(true), 240);
  }

  els.scopeProduction.addEventListener('click', () => setScope('production'));
  els.scopeStaging.addEventListener('click', () => setScope('staging'));
  els.promoteStagingBtn.addEventListener('click', promoteCurrentStaging);
  els.deleteStagingBtn.addEventListener('click', deleteCurrentStaging);
  els.bulkPromoteStagingBtn.addEventListener('click', () => stagingBulkAction('promote'));
  els.bulkDeleteStagingBtn.addEventListener('click', () => stagingBulkAction('delete'));

  // Filters and navigation.
  [els.searchInput, els.languageFilter, els.sourceFilter, els.styleFilter].forEach(el => el.addEventListener('input', debounceReload));
  els.autoReplyFilter.addEventListener('change', () => loadList(true));
  els.prevPage.addEventListener('click', () => { if (state.page > 1) { state.page--; loadList(); } });
  els.nextPage.addEventListener('click', () => { if (state.page < state.totalPages) { state.page++; loadList(); } });
  els.selectPage.addEventListener('change', () => {
    for (const item of state.items) {
      if (els.selectPage.checked) state.selected.add(item.key); else state.selected.delete(item.key);
    }
    renderList();
  });

  // Editor.
  $$('[data-field], [data-list-field]').forEach(el => el.addEventListener('input', () => setDirty(true)));
  els.rawEditor.addEventListener('input', () => { setDirty(true); clearRawError(); });
  $$('.tab').forEach(btn => btn.addEventListener('click', () => setTab(btn.dataset.tab)));
  els.saveBtn.addEventListener('click', saveCurrent);
  els.formatJsonBtn.addEventListener('click', () => {
    if (syncRawToDoc()) {
      els.rawEditor.value = JSON.stringify(state.currentDoc, null, 2);
      setDirty(true);
    }
  });

  // Bulk modal.
  els.bulkBtn.addEventListener('click', openBulk);
  els.bulkAllMatching.addEventListener('change', () => { updateBulkTargetText(); resetBulkPreview(); });
  $$('[data-enable]').forEach(toggle => toggle.addEventListener('change', () => {
    const target = document.getElementById(toggle.dataset.enable);
    if (target) target.disabled = !toggle.checked;
    resetBulkPreview();
  }));
  $$('#bulkDialog input, #bulkDialog textarea, #bulkDialog select').forEach(el => {
    if (el !== els.bulkAllMatching && !el.hasAttribute('data-enable')) el.addEventListener('input', resetBulkPreview);
  });
  els.previewBulkBtn.addEventListener('click', previewBulk);
  els.applyBulkBtn.addEventListener('click', applyBulk);

  els.reloadBtn.addEventListener('click', async () => {
    if (state.dirty && !confirm('Ungespeicherte Änderungen verwerfen und Dateien neu einlesen?')) return;
    try {
      const r = await api('/api/reload', {method: 'POST', headers: {'Content-Type':'application/json'}, body:'{}'});
      state.currentKey = null; state.currentDoc = null; state.currentMeta = null; state.dirty = false; state.selected.clear();
      els.editor.classList.add('hidden'); els.emptyState.classList.remove('hidden');
      toast(`${r.count.toLocaleString('de-DE')} Dateien neu eingelesen.`, 'success');
      await Promise.all([loadList(true), loadHealth()]);
    } catch (err) { toast(err.message, 'error'); }
  });

  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); saveCurrent(); }
    if (e.key === '/' && !['INPUT','TEXTAREA','SELECT'].includes(document.activeElement?.tagName)) { e.preventDefault(); els.searchInput.focus(); }
  });
  window.addEventListener('beforeunload', e => { if (state.dirty) { e.preventDefault(); e.returnValue = ''; } });

  loadHealth();
  loadList(true);
})();
