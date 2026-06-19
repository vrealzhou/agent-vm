const API = {
    get: (path) => fetch(path).then(r => r.json()),
    post: (path, body) => fetch(path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
    }).then(r => r.json()),
    delete: (path) => fetch(path, { method: 'DELETE' }).then(r => r.json())
};

let currentStatus = null;
let busy = false;
let refreshInterval = null;

const els = {
    btnBootstrap: document.getElementById('btn-bootstrap'),
    btnStart: document.getElementById('btn-start'),
    btnStop: document.getElementById('btn-stop'),
    btnDestroy: document.getElementById('btn-destroy'),
    btnUpgradeKernel: document.getElementById('btn-upgrade-kernel'),
    btnAddSync: document.getElementById('btn-add-sync'),
    btnAddTunnel: document.getElementById('btn-add-tunnel'),
    actionText: document.getElementById('action-text'),
    refreshText: document.getElementById('refresh-text'),
    progress: document.getElementById('progress'),
    overviewText: document.getElementById('overview-text'),
    resourceText: document.getElementById('resource-text'),
    syncList: document.getElementById('sync-list'),
    tunnelList: document.getElementById('tunnel-list'),
    modalOverlay: document.getElementById('modal-overlay'),
    modalTitle: document.getElementById('modal-title'),
    modalBody: document.getElementById('modal-body'),
    modalConfirm: document.getElementById('modal-confirm'),
    modalCancel: document.getElementById('modal-cancel'),
    filepickerOverlay: document.getElementById('filepicker-overlay'),
    filepickerTitle: document.getElementById('filepicker-title'),
    filepickerBreadcrumb: document.getElementById('filepicker-breadcrumb'),
    filepickerList: document.getElementById('filepicker-list'),
    filepickerSelect: document.getElementById('filepicker-select'),
    filepickerCancel: document.getElementById('filepicker-cancel'),
};

function showToast(message, type = 'success') {
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    toast.textContent = message;
    document.body.appendChild(toast);
    setTimeout(() => toast.remove(), 3000);
}

function setBusy(isBusy) {
    busy = isBusy;
    els.progress.classList.toggle('hidden', !isBusy);
    [els.btnBootstrap, els.btnStart, els.btnStop, els.btnDestroy, els.btnUpgradeKernel].forEach(btn => {
        btn.disabled = isBusy;
    });
}

function setAction(msg) { els.actionText.textContent = msg; }

function updateButtons() {
    if (!currentStatus) return;
    const { status } = currentStatus;
    const bootstrapDone = status.BootstrapDone;
    const running = status.Running;
    els.btnStart.disabled = !bootstrapDone || running || busy;
    els.btnStop.disabled = !bootstrapDone || !running || busy;
    els.btnBootstrap.disabled = busy;
    els.btnDestroy.disabled = busy;
    els.btnUpgradeKernel.disabled = !bootstrapDone || !running || busy;
}

async function refreshStatus() {
    try {
        const data = await API.get('/api/status');
        currentStatus = data;
        const { status, metrics } = data;
        els.overviewText.textContent = formatOverview(status);
        els.resourceText.textContent = formatResources(status, metrics);
        els.refreshText.textContent = 'Last refresh: ' + new Date().toISOString();
        updateButtons();
    } catch (err) {
        els.refreshText.textContent = 'Refresh failed: ' + err.message;
    }
}

function formatOverview(status) {
    const lines = [
        `Name: ${status.Name}`,
        `State: ${status.State}`,
        `Running: ${status.Running}`,
        `Bootstrap complete: ${status.BootstrapDone}`,
        `Disk: ${status.DiskPath}`,
        `IP: ${status.StaticIP}`,
        `SSH: ${status.SSHTarget}`,
    ];
    if (status.KernelVersion) lines.push(`Kernel: ${status.KernelVersion}`);
    if (status.Running) lines.push(`PID: ${status.PID}`);
    return lines.join('\n');
}

function formatResources(status, metrics) {
    if (!status.Running) return 'The VM is stopped.';
    if (!metrics || !metrics.Available) return 'Guest metrics are not available yet. SSH may still be starting.';
    const cpuText = metrics.HasCPUPercent
        ? `CPU usage: ${metrics.CPUPercent.toFixed(1)}%`
        : 'CPU usage: waiting for a second sample';
    return [
        cpuText,
        `Memory: ${metrics.MemUsedMiB} MiB / ${metrics.MemTotalMiB} MiB (${metrics.MemUsedPercent.toFixed(1)}%)`,
        'Sampling source: /proc/stat and /proc/meminfo over SSH.',
    ].join('\n');
}

function showModal(title, bodyHtml, onConfirm) {
    els.modalTitle.textContent = title;
    els.modalBody.innerHTML = bodyHtml;
    els.modalOverlay.classList.remove('hidden');
    els.modalConfirm.onclick = () => { onConfirm(); hideModal(); };
    els.modalCancel.onclick = hideModal;
    els.modalOverlay.querySelector('.modal-close').onclick = hideModal;
}

function hideModal() { els.modalOverlay.classList.add('hidden'); }

let filePickerCallback = null;
let filePickerCurrentPath = '';
let filePickerRoot = '';
let filePickerMode = 'vm';

async function openFilePicker(root, callback, mode) {
    filePickerMode = mode || 'vm';
    filePickerRoot = root || (filePickerMode === 'host' ? '' : '/home/vm');
    filePickerCurrentPath = filePickerRoot;
    filePickerCallback = callback;
    els.filepickerTitle.textContent = filePickerMode === 'host' ? 'Select Host Path' : 'Select VM Path';
    els.filepickerOverlay.classList.remove('hidden');
    await loadFilePickerPath(filePickerCurrentPath);
}

async function loadFilePickerPath(path) {
    try {
        const endpoint = filePickerMode === 'host' ? '/api/host-files' : '/api/vm-files';
        const data = await API.get(`${endpoint}?path=${encodeURIComponent(path)}&root=${encodeURIComponent(filePickerRoot)}`);
        if (data.error) { showToast(data.error, 'error'); return; }
        filePickerCurrentPath = data.path || path;
        renderFilePicker(data.entries || []);
    } catch (err) {
        showToast('Failed to load files: ' + err.message, 'error');
    }
}

function renderFilePicker(entries) {
    const parts = filePickerCurrentPath.split('/').filter(Boolean);
    let crumbHtml = '<span data-path="/">/</span>';
    let buildPath = '';
    parts.forEach(part => {
        buildPath += '/' + part;
        crumbHtml += ` / <span data-path="${buildPath}">${part}</span>`;
    });
    els.filepickerBreadcrumb.innerHTML = crumbHtml;
    els.filepickerBreadcrumb.querySelectorAll('span').forEach(span => {
        span.onclick = () => loadFilePickerPath(span.dataset.path);
    });

    els.filepickerList.innerHTML = entries.map(e => `
        <div class="filepicker-item" data-name="${e.name}" data-isdir="${e.isDir}">
            <span class="icon">${e.isDir ? '📁' : '📄'}</span>
            <span>${e.name}</span>
        </div>
    `).join('');

    els.filepickerList.querySelectorAll('.filepicker-item').forEach(item => {
        item.onclick = () => {
            const name = item.dataset.name;
            const isDir = item.dataset.isdir === 'true';
            if (isDir) loadFilePickerPath(filePickerCurrentPath + '/' + name);
        };
    });
}

els.filepickerSelect.onclick = () => {
    if (filePickerCallback) filePickerCallback(filePickerCurrentPath);
    els.filepickerOverlay.classList.add('hidden');
};
els.filepickerCancel.onclick = () => els.filepickerOverlay.classList.add('hidden');
els.filepickerOverlay.querySelector('.modal-close').onclick = () => els.filepickerOverlay.classList.add('hidden');

els.btnBootstrap.onclick = () => {
    const c = (currentStatus && currentStatus.config) || {};
    const sel = (id, val, opts) => opts.map(o => `<option${o === val ? ' selected' : ''}>${o}</option>`).join('');
    const backend = c.backend || 'utm';
    const isContainer = backend === 'sbx' || backend === 'podman';

    let fields = `
        <div class="form-group"><label>Backend</label><select id="bootstrap-backend">${sel('bootstrap-backend', backend, ['utm', 'sbx', 'podman'])}</select></div>
        <div class="form-group"><label>Default shell</label><select id="bootstrap-shell">${sel('bootstrap-shell', c.shell || 'fish', ['fish', 'zsh'])}</select></div>
        <div class="form-group"><label>Default editor</label><select id="bootstrap-editor">${sel('bootstrap-editor', c.editor || 'neovim', ['neovim', 'helix'])}</select></div>`;

    if (!isContainer) {
        fields += `<div class="form-group"><label>Window manager</label><select id="bootstrap-wm">${sel('bootstrap-wm', c.windowManager || 'xfce', ['lxqt', 'xfce'])}</select></div>`;
    }

    fields += `<div class="form-group"><label>Memory (MiB)</label><input id="bootstrap-memory" type="number" placeholder="6144" value="${c.memoryMiB || 6144}"></div>`;

    if (!isContainer) {
        fields += `
        <div class="form-group"><label>Disk size</label><input id="bootstrap-disk" placeholder="100G" value="${c.diskSize || '100G'}"></div>
        <div class="form-group"><label>Guest IP address</label><input id="bootstrap-ip" placeholder="192.168.64.10" value="${c.staticIP || '192.168.64.10'}"></div>`;
    }

    if (isContainer) {
        fields += `
        <div class="form-group"><label>Environment / Image</label><input id="bootstrap-environment" placeholder="e.g. go, node, php-symfony" value="${c.environment || ''}"></div>
        <div class="form-group"><label>Port mappings (host:guest, one per line)</label><textarea id="bootstrap-ports" rows="3" placeholder="3000:3000&#10;8080:80">${Array.isArray(c.ports) ? c.ports.map(p => p.host + ':' + p.guest).join('\\n') : ''}</textarea></div>
        <div class="form-group"><label>Volume mounts (host:container, one per line)</label><textarea id="bootstrap-volumes" rows="3" placeholder="/Users/me/projects:/home/vm/projects">${Array.isArray(c.volumes) ? c.volumes.filter(v => v.host_path).map(v => v.host_path + ':' + v.mount).join('\\n') : ''}</textarea></div>`;
    }

    fields += `
        <div class="form-group"><label>Hook scripts (one path per line)</label><textarea id="bootstrap-hook-scripts" rows="5" placeholder="/path/to/hooks.sh">${Array.isArray(c.hookScripts) ? c.hookScripts.join('\\n') : (c.hookScripts || '')}</textarea></div>
        <div class="form-group"><label>Git user name</label><input id="bootstrap-git-user" placeholder="Your Name" value="${c.userName || ''}"></div>
        <div class="form-group"><label>Git user email</label><input id="bootstrap-git-email" placeholder="you@example.com" value="${c.userEmail || ''}"></div>`;

    showModal('Bootstrap Preferences', fields, async () => {
        setBusy(true); setAction('Running bootstrap...');
        try {
            const body = {
                shell: document.getElementById('bootstrap-shell').value,
                editor: document.getElementById('bootstrap-editor').value,
                backend: document.getElementById('bootstrap-backend').value,
            };
            const newBackend = body.backend;
            const newIsContainer = newBackend === 'sbx' || newBackend === 'podman';

            if (!newIsContainer) {
                body.windowManager = document.getElementById('bootstrap-wm')?.value || 'xfce';
            }
            const mem = parseInt(document.getElementById('bootstrap-memory').value);
            if (mem > 0) body.memoryMiB = mem;
            if (!newIsContainer) {
                const disk = document.getElementById('bootstrap-disk')?.value.trim();
                const ip = document.getElementById('bootstrap-ip')?.value.trim();
                if (disk) body.diskSize = disk;
                if (ip) body.staticIP = ip;
            }
            if (newIsContainer) {
                const env = document.getElementById('bootstrap-environment')?.value.trim();
                if (env) body.environment = env;
                const portsText = document.getElementById('bootstrap-ports')?.value.trim();
                if (portsText) {
                    body.ports = portsText.split('\n').map(l => l.trim()).filter(l => l).map(l => {
                        const [h, g] = l.split(':');
                        return { host: parseInt(h), guest: parseInt(g) };
                    });
                }
                const volsText = document.getElementById('bootstrap-volumes')?.value.trim();
                if (volsText) {
                    body.volumes = volsText.split('\n').map(l => l.trim()).filter(l => l).map(l => {
                        const [hp, mp] = l.split(':');
                        return { host_path: hp, mount: mp };
                    });
                }
            }
            const hookScriptsText = document.getElementById('bootstrap-hook-scripts').value.trim();
            body.hookScripts = hookScriptsText ? hookScriptsText.split('\n').map(s => s.trim()).filter(s => s) : [];
            body.userName = document.getElementById('bootstrap-git-user').value.trim();
            body.userEmail = document.getElementById('bootstrap-git-email').value.trim();
            await API.post('/api/bootstrap', body);
            showToast('Bootstrap started — see progress log below');
        } catch (err) {
            showToast('Bootstrap failed: ' + err.message, 'error');
            setAction('Bootstrap failed: ' + err.message);
        }
        setBusy(false);
        els.progress.classList.remove('hidden');
    });
};

els.btnStart.onclick = async () => {
    setBusy(true); setAction('Starting...');
    try { await API.post('/api/start'); showToast('Start initiated'); }
    catch (err) { showToast('Start failed: ' + err.message, 'error'); setAction('Start failed: ' + err.message); }
    setBusy(false);
    els.progress.classList.remove('hidden');
};

els.btnStop.onclick = async () => {
    setBusy(true); setAction('Stopping...');
    try { await API.post('/api/stop'); showToast('Stop initiated'); }
    catch (err) { showToast('Stop failed: ' + err.message, 'error'); setAction('Stop failed: ' + err.message); }
    setBusy(false);
    els.progress.classList.remove('hidden');
};

els.btnDestroy.onclick = () => {
    if (!confirm('Stop and delete all generated files?')) return;
    setBusy(true); setAction('Destroying...');
    API.post('/api/destroy')
        .then(() => showToast('Destroy initiated'))
        .catch(err => { showToast('Destroy failed: ' + err.message, 'error'); setAction('Destroy failed: ' + err.message); })
        .finally(() => { setBusy(false); els.progress.classList.remove('hidden'); });
};

async function loadTunnels() {
    try {
        const data = await API.get('/api/tunnels');
        const tunnels = data.tunnels || [];
        els.tunnelList.innerHTML = tunnels.length === 0
            ? '<div class="list-item">No tunnels configured.</div>'
            : tunnels.map(t => {
                const statusIcon = t.running ? '🟢' : '🔴';
                const mapping = t.type === 'local'
                    ? `host:${t.local_port} → ${t.remote_host || 'localhost'}:${t.remote_port}`
                    : `vm:${t.remote_port} → host:${t.local_port}`;
                return `
                    <div class="list-item">
                        <div class="list-item-info">${statusIcon} ${t.name} — ${mapping}</div>
                        <div class="list-item-actions">
                            <button class="btn btn-small" onclick="toggleTunnel('${t.id}', ${t.running})">${t.running ? 'Stop' : 'Start'}</button>
                            <button class="btn btn-small btn-danger" onclick="removeTunnel('${t.id}')">Remove</button>
                        </div>
                    </div>
                `;
            }).join('');
    } catch (err) {
        els.tunnelList.innerHTML = '<div class="list-item">Error loading tunnels</div>';
    }
}

window.toggleTunnel = async (id, isRunning) => {
    try {
        await API.post(`/api/tunnels/${id}/${isRunning ? 'stop' : 'start'}`);
        showToast(`Tunnel ${isRunning ? 'stopped' : 'started'}`);
        loadTunnels();
    } catch (err) { showToast(err.message, 'error'); }
};

window.removeTunnel = async (id) => {
    if (!confirm('Remove this tunnel?')) return;
    try {
        await API.delete(`/api/tunnels/${id}`);
        showToast('Tunnel removed'); loadTunnels();
    } catch (err) { showToast(err.message, 'error'); }
};

els.btnAddTunnel.onclick = () => {
    showModal('Add Tunnel', `
        <div class="form-group"><label>Name</label><input id="tunnel-name" placeholder="Tunnel name"></div>
        <div class="form-group"><label>Type</label><select id="tunnel-type"><option value="local">Local Forward</option><option value="remote">Remote Forward</option></select></div>
        <div class="form-group"><label>Local port</label><input id="tunnel-local" type="number" placeholder="Local port"></div>
        <div class="form-group"><label>Remote port</label><input id="tunnel-remote" type="number" placeholder="Remote port"></div>
        <div class="form-group"><label>Remote host</label><input id="tunnel-host" placeholder="localhost"></div>
        <div class="form-group"><label><input id="tunnel-autostart" type="checkbox"> Auto-start when VM runs</label></div>
    `, async () => {
        try {
            await API.post('/api/tunnels', {
                name: document.getElementById('tunnel-name').value,
                type: document.getElementById('tunnel-type').value,
                localPort: parseInt(document.getElementById('tunnel-local').value),
                remotePort: parseInt(document.getElementById('tunnel-remote').value),
                remoteHost: document.getElementById('tunnel-host').value,
                autoStart: document.getElementById('tunnel-autostart').checked,
            });
            showToast('Tunnel added'); loadTunnels();
        } catch (err) { showToast(err.message, 'error'); }
    });
};

async function loadSync() {
    try {
        const data = await API.get('/api/sync');
        const pairs = data.sync_pairs || [];
        els.syncList.innerHTML = pairs.length === 0
            ? '<div class="list-item">No sync pairs configured.</div>'
            : pairs.map(p => `
                <div class="list-item">
                    <div class="list-item-info">${p.id} — ${p.host_path} ↔ ${p.vm_path}</div>
                    <div class="list-item-actions">
                        <button class="btn btn-small" onclick="runSync('${p.id}')">Sync</button>
                        ${p.mode === 'copy' ? `<button class="btn btn-small" onclick="showHistory('${p.id}')">History</button>` : ''}
                        <button class="btn btn-small btn-danger" onclick="removeSync('${p.id}')">Remove</button>
                    </div>
                </div>
            `).join('');
    } catch (err) {
        els.syncList.innerHTML = '<div class="list-item">Error loading sync pairs</div>';
    }
}

window.runSync = async (id) => {
    try {
        const res = await API.post(`/api/sync/${id}/run`);
        showToast(res.message || 'Sync started');
    } catch (err) { showToast(err.message, 'error'); }
};

window.showHistory = async (id) => {
    try {
        const backups = await API.get(`/api/sync/${id}/history`);
        const html = backups.length === 0
            ? '<p>No backups found.</p>'
            : '<ul>' + backups.map(b => `<li>${new Date(b).toISOString()}</li>`).join('') + '</ul>';
        showModal('Backup History', html, () => {});
        els.modalConfirm.classList.add('hidden');
    } catch (err) { showToast(err.message, 'error'); }
};

window.removeSync = async (id) => {
    if (!confirm('Remove this sync pair?')) return;
    try {
        await API.delete(`/api/sync/${id}`);
        showToast('Sync pair removed'); loadSync();
    } catch (err) { showToast(err.message, 'error'); }
};

els.btnAddSync.onclick = () => {
    showModal('Add Sync Pair', `
        <div class="form-group"><label>Mode</label><select id="sync-mode"><option value="git">Git</option><option value="copy">Copy</option></select></div>
        <div class="form-group">
            <label>Host directory</label>
            <div style="display:flex;gap:8px;"><input id="sync-host" placeholder="Host directory path" style="flex:1;"><button class="btn btn-small" onclick="pickHostPath()">Browse</button></div>
        </div>
        <div class="form-group">
            <label>VM directory</label>
            <div style="display:flex;gap:8px;"><input id="sync-vm" placeholder="VM directory path" style="flex:1;"><button class="btn btn-small" onclick="pickVMPath()">Browse</button></div>
        </div>
        <div class="form-group" id="git-fields">
            <label>Bare repo path</label>
            <div style="display:flex;gap:8px;"><input id="sync-bare" placeholder="Bare repo path on VM (optional)" style="flex:1;"><button class="btn btn-small" onclick="pickBareRepoPath()">Browse</button></div>
        </div>
        <div class="form-group hidden" id="copy-fields">
            <label>Direction</label>
            <select id="sync-direction"><option value="host-to-vm">Host → VM</option><option value="vm-to-host">VM → Host</option><option value="bidirectional">Bidirectional</option></select>
        </div>
    `, async () => {
        const mode = document.getElementById('sync-mode').value;
        const req = { mode, hostPath: document.getElementById('sync-host').value, vmPath: document.getElementById('sync-vm').value };
        if (mode === 'git') req.bareRepoPath = document.getElementById('sync-bare').value;
        else req.direction = document.getElementById('sync-direction').value;
        try {
            await API.post('/api/sync', req);
            showToast('Sync pair added'); loadSync();
        } catch (err) { showToast(err.message, 'error'); }
    });

    document.getElementById('sync-mode').onchange = (e) => {
        const isGit = e.target.value === 'git';
        document.getElementById('git-fields').classList.toggle('hidden', !isGit);
        document.getElementById('copy-fields').classList.toggle('hidden', isGit);
    };
};

window.pickVMPath = () => openFilePicker('/home/vm', (path) => { document.getElementById('sync-vm').value = path; }, 'vm');
window.pickBareRepoPath = () => openFilePicker('/home/vm/repos', (path) => { document.getElementById('sync-bare').value = path; }, 'vm');
window.pickHostPath = () => openFilePicker('', (path) => { document.getElementById('sync-host').value = path; }, 'host');

els.btnUpgradeKernel.onclick = () => {
    if (!confirm('Upgrade the VM kernel? This will restart the VM.')) return;
    setBusy(true); setAction('Upgrading kernel...');
    API.post('/api/upgrade-kernel')
        .then(() => showToast('Kernel upgrade started'))
        .catch(err => { showToast('Kernel upgrade failed: ' + err.message, 'error'); setAction('Kernel upgrade failed: ' + err.message); })
        .finally(() => setBusy(false));
};

refreshStatus();
loadTunnels();
loadSync();
refreshInterval = setInterval(() => { refreshStatus(); loadTunnels(); loadSync(); }, 5000);

const themeToggle = document.getElementById('theme-toggle');
const themeLabels = { system: 'System', light: 'Light', dark: 'Dark' };
const themeOrder = ['system', 'light', 'dark'];

function applyTheme(theme) {
    localStorage.setItem('theme', theme);
    themeToggle.textContent = themeLabels[theme];
    if (theme === 'system') {
        document.documentElement.removeAttribute('data-theme');
    } else {
        document.documentElement.setAttribute('data-theme', theme);
    }
}

const saved = localStorage.getItem('theme') || 'system';
applyTheme(saved);

themeToggle.onclick = () => {
    const current = localStorage.getItem('theme') || 'system';
    const next = themeOrder[(themeOrder.indexOf(current) + 1) % themeOrder.length];
    applyTheme(next);
};

const progressLog = document.getElementById('progress-log');
let lastProgressTime = 0;

async function loadProgress() {
    try {
        const data = await API.get(`/api/progress?since=${lastProgressTime}`);
        const entries = data.entries || [];
        if (entries.length > 0) {
            entries.forEach(e => {
                const div = document.createElement('div');
                const time = document.createElement('span');
                time.className = 'log-time';
                time.textContent = new Date(e.time).toLocaleTimeString();
                div.appendChild(time);
                div.appendChild(document.createTextNode(e.message));
                progressLog.appendChild(div);
            });
            lastProgressTime = new Date(entries[entries.length - 1].time).getTime() + 1;
            progressLog.scrollTop = progressLog.scrollHeight;
        }
    } catch (_) {}
}

loadProgress();
setInterval(loadProgress, 2000);

const leftPanel = document.getElementById('left-panel');
const divider = document.getElementById('divider');
const rightPanel = document.getElementById('right-panel');
const savedWidth = localStorage.getItem('panel-width');
if (savedWidth) leftPanel.style.width = savedWidth;

let dragging = false;
divider.addEventListener('mousedown', (e) => {
    e.preventDefault();
    dragging = true;
    divider.classList.add('active');
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
});
document.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const newWidth = Math.max(200, Math.min(e.clientX, window.innerWidth - 200));
    leftPanel.style.width = newWidth + 'px';
});
document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    divider.classList.remove('active');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    localStorage.setItem('panel-width', leftPanel.style.width);
});
