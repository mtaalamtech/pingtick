let activeTab = 'dashboard-tab';
let targetsData = [];
let chartInstance = null;

document.addEventListener('DOMContentLoaded', () => {
    // Navigation Tabs Setup
    const navLinks = document.querySelectorAll('.nav-link');
    navLinks.forEach(link => {
        link.addEventListener('click', (e) => {
            e.preventDefault();
            const target = link.getAttribute('data-target');
            window.location.hash = target;
            switchTab(target);
        });
    });

    // Check hash on load
    if (window.location.hash) {
        let hash = window.location.hash.substring(1);
        if (document.getElementById(hash)) {
            activeTab = hash;
        }
    }
    switchTab(activeTab);

    // Row buttons carry only an id, so target names never reach inline handlers
    const targetActions = { insights: showTargetHistory, clone: cloneTarget, edit: editTarget, delete: deleteTarget };
    document.getElementById('dashboard-targets-list').addEventListener('click', (e) => {
        const button = e.target.closest('button[data-action]');
        if (button) targetActions[button.dataset.action](button.dataset.id);
    });

    // Initial load generic setup
    fetchTargets();
    fetchSettings();
    fetchSpeedTest();

    // Auto-update dashboard metrics
    setInterval(() => {
        if (activeTab === 'dashboard-tab' && !document.hidden) {
            fetchTargets();
            fetchSpeedTest();
        }
    }, 2000);

    // Mouse exit intent
    document.addEventListener('mouseleave', (e) => {
        if (e.clientY <= 0) {
            const nag = document.getElementById('tray-nag');
            if (nag && nag.style.opacity === "0" || nag.style.opacity === "") {
                nag.style.opacity = "1";
                setTimeout(() => nag.style.opacity = "0", 6000);
            }
        }
    });
});

function switchTab(tabId) {
    activeTab = tabId;

    // Update active class on nav links
    const navLinks = document.querySelectorAll('.nav-link');
    navLinks.forEach(link => {
        if (link.getAttribute('data-target') === tabId) {
            link.classList.add('active');
        } else {
            link.classList.remove('active');
        }
    });

    // Update active class on content sections
    const tabs = document.querySelectorAll('.tab-content');
    tabs.forEach(tab => {
        if (tab.id === tabId) {
            tab.classList.add('active');
        } else {
            tab.classList.remove('active');
        }
    });

    // Refresh data depending on new Tab
    if (tabId === 'dashboard-tab') {
        fetchTargets();
    } else if (tabId === 'scanner-tab') {
        fetchScan();
    } else if (tabId === 'history-tab') {
        fetchGlobalHistory();
    } else if (tabId === 'alerts-tab') {
        fetchSettings();
    }
}

// Fetch list of targets
function fetchTargets() {
    fetch('/api/targets')
        .then(res => res.json())
        .then(data => {
            targetsData = data || [];
            updateStatsBar();
            renderDashboardList();
        })
        .catch(err => console.error('Error fetching targets:', err));
}

// Update upper dashboard status metrics
function updateStatsBar() {
    const total = targetsData.length;
    const online = targetsData.filter(t => t.status === 'online').length;
    const offline = targetsData.filter(t => t.status === 'offline').length;

    let totalLatency = 0;
    let latencyCount = 0;
    let totalSent = 0;
    let totalSucceed = 0;

    targetsData.forEach(t => {
        if (t.type !== 'ssl' && t.status === 'online' && t.latency > 0) {
            totalLatency += t.latency;
            latencyCount++;
        }
        totalSent += t.total_sent;
        totalSucceed += t.succeed_count;
    });

    const avgLatency = latencyCount > 0 ? Math.round(totalLatency / latencyCount) : 0;
    const globalUptime = totalSent > 0 ? ((totalSucceed / totalSent) * 100).toFixed(2) : 0;

    document.getElementById('stat-total').innerText = total;
    document.getElementById('stat-uptime').innerText = `${globalUptime}%`;
    document.getElementById('stat-offline').innerText = offline;
    document.getElementById('stat-latency').innerText = avgLatency + 'ms';
}

// Render dynamic table rows in Dashboard
function renderDashboardList() {
    const tbody = document.getElementById('dashboard-targets-list');
    if (!tbody) return;

    if (targetsData.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" class="empty-state text-secondary">No systems configured. Go to "Manage Targets" to add one.</td></tr>`;
        return;
    }

    tbody.innerHTML = '';
    targetsData.forEach(t => {
        const timeAgo = (Date.now() - new Date(t.last_check).getTime()) / 1000;
        let lastCheckStr = timeAgo < 60 ? 'Just now' : Math.floor(timeAgo / 60) + 'm ago';
        if (!t.last_check || new Date(t.last_check).getFullYear() < 2000) lastCheckStr = 'Pending...';

        let latencyStr = '-';
        if (t.status === 'online') {
            if (t.type === 'ssl') {
                latencyStr = `${t.latency} days left`;
            } else {
                latencyStr = `${t.latency}ms`;
            }
        } else if (t.status === 'offline') {
            latencyStr = '<span class="text-danger">Failed</span>';
        }

        const typeIndicator = {
            'ping': 'ICMP Ping',
            'ssl': 'SSL Certificate',
            'port': 'TCP Port'
        }[t.type] || t.type;

        let statsStr = '-';
        if (t.type !== 'ssl') {
            statsStr = `${t.min_latency}/${t.max_latency}/${t.latency}ms`;
        } else {
            if (t.status === 'online') {
                if (t.latency <= 15) {
                    statsStr = `<span class="blink-danger">Expires in ${t.latency} Days</span>`;
                } else {
                    statsStr = `<span>Expires in ${t.latency} Days</span>`;
                }
            } else if (t.status === 'offline') {
                statsStr = '<span class="text-danger">Cert Invalid</span>';
            }
        }

        let percentFail = 0;
        if (t.total_sent > 0) {
            percentFail = Math.round((t.failed_count / t.total_sent) * 100);
        }
        const healthStr = `<span class="${percentFail > 0 ? 'text-danger' : 'text-success'}">${percentFail}% Failed</span> <br><small class="text-secondary">Sent: ${t.total_sent}</small>`;

        const replyIp = t.reply_ip || '-';
        const groupTag = t.tag ? `<span class="badge" style="background: rgba(255,255,255,0.1); border: 1px solid var(--glass-border);">${escapeHtml(t.tag)}</span>` : '<span class="text-secondary">-</span>';

        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${groupTag}</td>
            <td><span class="status-indicator ${t.status}"></span> ${t.status.toUpperCase()}</td>
            <td><strong>${escapeHtml(t.name)}</strong></td>
            <td><span class="pill">${escapeHtml(t.host)}</span></td>
            <td>${escapeHtml(replyIp)}</td>
            <td>${healthStr}</td>
            <td>${statsStr}</td>
            <td><span class="text-secondary">${escapeHtml(typeIndicator)}</span></td>
            <td class="text-secondary">${lastCheckStr}</td>
            <td style="white-space: nowrap;">
                <button class="btn secondary btn-sm" data-action="insights" data-id="${escapeHtml(t.id)}">Insights</button>
                <button class="btn secondary btn-sm" data-action="clone" data-id="${escapeHtml(t.id)}">Clone</button>
                <button class="btn primary btn-sm" data-action="edit" data-id="${escapeHtml(t.id)}">Edit</button>
                <button class="btn danger btn-sm" data-action="delete" data-id="${escapeHtml(t.id)}">Delete</button>
            </td>
        `;
        tbody.appendChild(tr);
    });
}

// Modal Form handling
function openAddModal() {
    document.getElementById('add-modal').classList.add('active');
    document.getElementById('target_edit_id').value = '';
    document.getElementById('btn_submit_target').innerText = 'Create Target';

    let cats = document.getElementById('target_categories_setting').value.split(',').map(s => s.trim()).filter(s => s);
    let tags = document.getElementById('target_tags_setting').value.split(',').map(s => s.trim()).filter(s => s);

    if (cats.length === 0) cats = ['General'];

    fillSelect('target_category', cats);
    fillSelect('target_tag', tags, 'None');
}

// Values are set through the DOM so settings text is never parsed as HTML.
function fillSelect(id, values, emptyLabel) {
    const options = values.map(value => new Option(value, value));
    if (emptyLabel) options.unshift(new Option(emptyLabel, ''));
    document.getElementById(id).replaceChildren(...options);
}

function editTarget(id) {
    const target = targetsData.find(t => t.id === id);
    if (!target) return;

    openAddModal();
    document.getElementById('target_edit_id').value = id;
    document.getElementById('btn_submit_target').innerText = 'Save Changes';

    document.getElementById('target_name').value = target.name;

    let host = target.host;
    if (target.type === 'port' && host.includes(':')) {
        const parts = host.split(':');
        document.getElementById('target_host').value = parts[0];
        document.getElementById('target_port').value = parts[1];
    } else {
        document.getElementById('target_host').value = host;
        document.getElementById('target_port').value = '';
    }

    document.getElementById('target_type').value = target.type;
    togglePortInput();

    document.getElementById('target_interval').value = target.interval;

    if (document.getElementById('target_snmp_community')) {
        document.getElementById('target_snmp_community').value = target.snmp_community || '';
        document.getElementById('target_snmp_oid').value = target.snmp_oid || '';
    }

    if (document.getElementById('target_wmi_user')) {
        document.getElementById('target_wmi_user').value = target.wmi_user || '';
        document.getElementById('target_wmi_pass').value = target.wmi_pass || '';
        document.getElementById('target_wmi_namespace').value = target.wmi_namespace || '';
        document.getElementById('target_wmi_query').value = target.wmi_query || '';
    }

    setTimeout(() => {
        if (target.category) document.getElementById('target_category').value = target.category;
        if (target.tag) document.getElementById('target_tag').value = target.tag;
    }, 10);
}

function cloneTarget(id) {
    const target = targetsData.find(t => t.id === id);
    if (!target) return;

    openAddModal();

    // Explicitly make sure it acts as a new submission
    document.getElementById('target_edit_id').value = '';
    document.getElementById('btn_submit_target').innerText = 'Create Target';

    document.getElementById('target_name').value = 'Clone ' + target.name;

    let host = target.host;
    if (target.type === 'port' && host.includes(':')) {
        const parts = host.split(':');
        document.getElementById('target_host').value = parts[0];
        document.getElementById('target_port').value = parts[1];
    } else {
        document.getElementById('target_host').value = host;
        document.getElementById('target_port').value = '';
    }

    document.getElementById('target_type').value = target.type;
    togglePortInput();

    document.getElementById('target_interval').value = target.interval;

    if (document.getElementById('target_snmp_community')) {
        document.getElementById('target_snmp_community').value = target.snmp_community || '';
        document.getElementById('target_snmp_oid').value = target.snmp_oid || '';
    }

    if (document.getElementById('target_wmi_user')) {
        document.getElementById('target_wmi_user').value = target.wmi_user || '';
        document.getElementById('target_wmi_pass').value = target.wmi_pass || '';
        document.getElementById('target_wmi_namespace').value = target.wmi_namespace || '';
        document.getElementById('target_wmi_query').value = target.wmi_query || '';
    }

    setTimeout(() => {
        if (target.category) document.getElementById('target_category').value = target.category;
        if (target.tag) document.getElementById('target_tag').value = target.tag;
    }, 10);
}

function closeAddModal() {
    document.getElementById('add-modal').classList.remove('active');
    document.getElementById('target_edit_id').value = '';
    document.getElementById('target_name').value = '';
    document.getElementById('target_host').value = '';
    document.getElementById('target_port').value = '';
    document.getElementById('target_interval').value = '10';
}

function openBatchModal() {
    document.getElementById('batch-modal').classList.add('active');
    document.getElementById('batch_addresses').value = '';

    let cats = document.getElementById('target_categories_setting').value.split(',').map(s => s.trim()).filter(s => s);
    let tags = document.getElementById('target_tags_setting').value.split(',').map(s => s.trim()).filter(s => s);

    if (cats.length === 0) cats = ['General'];

    fillSelect('batch_category', cats);
    fillSelect('batch_tag', tags, 'None');
}

function closeBatchModal() {
    document.getElementById('batch-modal').classList.remove('active');
}

async function submitBatchImport() {
    const type = document.getElementById('batch_type').value;
    const interval = parseInt(document.getElementById('batch_interval').value);
    const category = document.getElementById('batch_category').value;
    const tag = document.getElementById('batch_tag').value;

    const rawLines = document.getElementById('batch_addresses').value.split('\n');
    let importedCount = 0;

    const btn = document.getElementById('btn_submit_batch');
    btn.disabled = true;
    btn.innerText = 'Importing...';

    for (let line of rawLines) {
        let addr = line.trim();
        if (!addr) continue;

        let host = addr;
        if (type === 'port') {
            if (!host.includes(':')) {
                console.warn("Skipping bad port mapping address:", host);
                continue;
            }
        }

        // Prevent duplication check
        let isDup = targetsData.find(t => t.host === host && t.type === type);
        if (isDup) {
            console.log("Skipping duplicate:", host);
            continue;
        }

        // Using host directly as friendly name fallback map
        const payload = { name: host, host: host, type, interval, tag, category };

        try {
            const r = await fetch('/api/targets', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });
            if (r.ok) importedCount++;
        } catch (e) {
            console.error(e);
        }
    }

    alert(`Batch Operation complete. Inserted ${importedCount} new monitoring targets.`);
    btn.disabled = false;
    btn.innerText = 'Import Endpoints';
    closeBatchModal();
    fetchTargets();
}

function togglePortInput() {
    const type = document.getElementById('target_type').value;
    const portGroup = document.getElementById('target_port_group');
    const hostGroup = document.getElementById('host_group');
    const snmpGroup = document.getElementById('snmp_config_grid');
    const wmiGroup = document.getElementById('wmi_config_grid');

    if (type === 'port') {
        portGroup.style.display = 'block';
        hostGroup.style.gridColumn = 'span 1';
        if (snmpGroup) snmpGroup.style.display = 'none';
        if (wmiGroup) wmiGroup.style.display = 'none';
        document.getElementById('target_host').placeholder = "Domain/IP without port";
    } else if (type === 'snmp' || type === 'bandwidth') {
        portGroup.style.display = 'none';
        hostGroup.style.gridColumn = 'span 2';
        if (snmpGroup) snmpGroup.style.display = 'grid';
        if (wmiGroup) wmiGroup.style.display = 'none';
        document.getElementById('target_host').placeholder = "Endpoint Address";
    } else if (type === 'wmi') {
        portGroup.style.display = 'none';
        hostGroup.style.gridColumn = 'span 2';
        if (snmpGroup) snmpGroup.style.display = 'none';
        if (wmiGroup) wmiGroup.style.display = 'grid';
        document.getElementById('target_host').placeholder = "Windows IP Address";
    } else {
        portGroup.style.display = 'none';
        hostGroup.style.gridColumn = 'span 2';
        if (snmpGroup) snmpGroup.style.display = 'none';
        if (wmiGroup) wmiGroup.style.display = 'none';
        document.getElementById('target_host').placeholder = "Endpoint Address";
    }
}

function submitAddTarget() {
    const editId = document.getElementById('target_edit_id').value;
    const name = document.getElementById('target_name').value.trim();
    let host = document.getElementById('target_host').value.trim();
    const type = document.getElementById('target_type').value;
    const tag = document.getElementById('target_tag').value.trim();
    const category = document.getElementById('target_category').value.trim();
    const port = document.getElementById('target_port').value.trim();
    const interval = parseInt(document.getElementById('target_interval').value);

    let snmp_community = '';
    let snmp_oid = '';
    if (document.getElementById('target_snmp_community')) {
        snmp_community = document.getElementById('target_snmp_community').value.trim();
        snmp_oid = document.getElementById('target_snmp_oid').value.trim();
    }

    let wmi_user = '';
    let wmi_pass = '';
    let wmi_namespace = '';
    let wmi_query = '';
    if (document.getElementById('target_wmi_user')) {
        wmi_user = document.getElementById('target_wmi_user').value.trim();
        wmi_pass = document.getElementById('target_wmi_pass').value.trim();
        wmi_namespace = document.getElementById('target_wmi_namespace').value.trim();
        wmi_query = document.getElementById('target_wmi_query').value.trim();
    }

    if (type === 'port' && port) {
        host = `${host}:${port}`;
    }

    if (!name || !host || isNaN(interval)) {
        alert('Please fill out all input fields correctly.');
        return;
    }

    const payload = { name, host, type, interval, tag, category, snmp_community, snmp_oid, wmi_user, wmi_pass, wmi_namespace, wmi_query };
    if (editId) payload.id = editId;

    fetch('/api/targets', {
        method: editId ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(res => {
            if (!res.ok) throw new Error('Bad request profile validation');
            return res.json();
        })
        .then(() => {
            closeAddModal();
            // Clear fields
            document.getElementById('target_name').value = '';
            document.getElementById('target_host').value = '';
            document.getElementById('target_port').value = '';
            document.getElementById('target_tag').value = '';
            if (document.getElementById('target_snmp_community')) {
                document.getElementById('target_snmp_community').value = '';
                document.getElementById('target_snmp_oid').value = '';
            }
            if (document.getElementById('target_wmi_user')) {
                document.getElementById('target_wmi_user').value = '';
                document.getElementById('target_wmi_pass').value = '';
                document.getElementById('target_wmi_namespace').value = '';
                document.getElementById('target_wmi_query').value = '';
            }
            document.getElementById('target_interval').value = '10';
            togglePortInput();
            fetchTargets();
        })
        .catch(err => {
            alert('Failed to save target. Make sure format is correct.');
            console.error(err);
        });
}

// Delete target
function deleteTarget(id) {
    if (confirm('Are you sure you want to stop monitoring this target?')) {
        fetch(`/api/targets?id=${id}`, {
            method: 'DELETE'
        })
            .then(() => {
                fetchTargets();
            })
            .catch(err => console.error('Delete error:', err));
    }
}

function fetchSettings() {
    fetch('/api/settings')
        .then(res => res.json())
        .then(data => {
            document.getElementById('alert_threshold').value = data.alert_threshold || '3';
            document.getElementById('data_retention').value = data.data_retention || '3 months';
            document.getElementById('show_public_ip').checked = (data.show_public_ip === 'true');
            document.getElementById('auto_speed_test').checked = (data.auto_speed_test === 'true');
            document.getElementById('telegram_enabled').checked = (data.telegram_enabled === 'true');
            document.getElementById('telegram_bot_token').value = data.telegram_bot_token || '';
            document.getElementById('telegram_chat_id').value = data.telegram_chat_id || '';
            document.getElementById('email_enabled').checked = (data.email_enabled === 'true');

            if (document.getElementById('daily_report_enabled')) {
                document.getElementById('daily_report_enabled').checked = (data.daily_report_enabled === 'true');
                document.getElementById('daily_report_time').value = data.daily_report_time || '08:00';
            }
            document.getElementById('smtp_host').value = data.smtp_host || '';
            document.getElementById('smtp_port').value = data.smtp_port || '';
            document.getElementById('smtp_protocol').value = data.smtp_protocol || 'starttls';
            document.getElementById('smtp_user').value = data.smtp_user || '';
            document.getElementById('smtp_pass').value = data.smtp_pass || '';
            document.getElementById('smtp_from').value = data.smtp_from || '';
            document.getElementById('alert_email_to').value = data.alert_email_to || '';
            document.getElementById('target_categories_setting').value = data.target_categories || 'Infrastructure, External, Database, Services';
            document.getElementById('target_tags_setting').value = data.target_tags || 'Production, Staging, QA, Development';
        })
        .catch(err => console.error('Error fetching settings:', err));
}

function saveSettings() {
    const payload = {
        alert_threshold: document.getElementById('alert_threshold').value,
        data_retention: document.getElementById('data_retention').value,
        show_public_ip: document.getElementById('show_public_ip').checked ? 'true' : 'false',
        auto_speed_test: document.getElementById('auto_speed_test').checked ? 'true' : 'false',
        telegram_enabled: document.getElementById('telegram_enabled').checked ? 'true' : 'false',
        telegram_bot_token: document.getElementById('telegram_bot_token').value,
        telegram_chat_id: document.getElementById('telegram_chat_id').value,
        email_enabled: document.getElementById('email_enabled').checked ? 'true' : 'false',
        smtp_host: document.getElementById('smtp_host').value,
        smtp_port: document.getElementById('smtp_port').value,
        smtp_protocol: document.getElementById('smtp_protocol').value,
        smtp_user: document.getElementById('smtp_user').value,
        smtp_pass: document.getElementById('smtp_pass').value,
        smtp_from: document.getElementById('smtp_from').value,
        alert_email_to: document.getElementById('alert_email_to').value,
        target_categories: document.getElementById('target_categories_setting').value,
        target_tags: document.getElementById('target_tags_setting').value
    };

    if (document.getElementById('daily_report_enabled')) {
        payload.daily_report_enabled = document.getElementById('daily_report_enabled').checked ? 'true' : 'false';
        payload.daily_report_time = document.getElementById('daily_report_time').value;
    }

    fetch('/api/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(res => res.json())
        .then(data => {
            if (data.success) {
                alert('Settings saved successfully.');
            } else {
                alert('Could not save settings.');
            }
        })
        .catch(err => {
            alert('Server connection error.');
            console.error(err);
        });
}

let activeExportId = '';

function showTargetHistory(id) {
    const target = targetsData.find(t => t.id === id);
    document.getElementById('chart-modal-title').innerText = `Latency History - ${target ? target.name : ''}`;
    activeExportId = id;
    document.getElementById('chart-modal').classList.add('active');

    fetch(`/api/history?id=${id}`)
        .then(res => res.json())
        .then(history => {
            renderChart(history);
        })
        .catch(err => console.error(err));
}

function renderChart(history) {
    const ctx = document.getElementById('latencyChart').getContext('2d');

    // Populate history table
    const tbody = document.getElementById('history-log-list');
    tbody.innerHTML = '';

    // Reverse array to show newest first
    const reversedHistory = [...history].reverse();
    reversedHistory.forEach(h => {
        const d = new Date(h.timestamp);
        const timeStr = d.toLocaleDateString() + ' ' + d.toLocaleTimeString();

        const tr = document.createElement('tr');
        const statusBadge = h.status === 'online' ? '<span class="text-success">Succeeded</span>' : '<span class="text-danger">Failed</span>';

        tr.innerHTML = `
            <td>${timeStr}</td>
            <td>${escapeHtml(h.reply_ip || '-')}</td>
            <td>${h.latency}</td>
            <td>${h.ttl ? h.ttl : '-'}</td>
            <td>${statusBadge}</td>
        `;
        tbody.appendChild(tr);
    });

    // Destroy previous Chart instance
    if (chartInstance) {
        chartInstance.destroy();
    }

    const labels = history.map(h => {
        const d = new Date(h.timestamp);
        return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    });

    const latencyData = history.map(h => h.latency);
    const color = '#3b82f6';

    chartInstance = new Chart(ctx, {
        type: 'line',
        data: {
            labels: labels,
            datasets: [{
                label: 'Latency (ms)',
                data: latencyData,
                borderColor: color,
                backgroundColor: 'rgba(59, 130, 246, 0.1)',
                borderWidth: 2,
                pointRadius: 3,
                tension: 0.2,
                fill: true
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
                y: {
                    grid: { color: 'rgba(255,255,255,0.05)' },
                    ticks: { color: '#94a3b8' }
                },
                x: {
                    grid: { color: 'rgba(255,255,255,0.05)' },
                    ticks: { color: '#94a3b8' }
                }
            },
            plugins: {
                legend: { display: false }
            }
        }
    });
}

function closeChartModal() {
    document.getElementById('chart-modal').classList.remove('active');
    document.getElementById('history-log-list').innerHTML = '';
    if (chartInstance) {
        chartInstance.destroy();
        chartInstance = null;
    }
}

function toggleReportPeriod() {
    const period = document.getElementById('report-period').value;
    const datePickers = document.getElementById('custom-date-pickers');
    if (period === 'custom') {
        datePickers.style.display = 'flex';
    } else {
        datePickers.style.display = 'none';
        document.getElementById('report-start-date').value = '';
        document.getElementById('report-end-date').value = '';
    }
}

function fetchGlobalHistory() {
    let period = document.getElementById('report-period')?.value || 'today';
    let url = `/api/history_report?period=${period}`;

    if (period === 'custom') {
        const start = document.getElementById('report-start-date').value;
        const end = document.getElementById('report-end-date').value;
        if (!start || !end) return; // Wait for both dates
        url += `&start=${start}&end=${end}`;
    }

    fetch(url)
        .then(res => res.json())
        .then(data => {
            const tbody = document.getElementById('global-history-list');
            if (!tbody) return;
            tbody.innerHTML = '';

            let total = 0, upCount = 0, downCount = 0, uptimeSum = 0;

            if (!data || data.length === 0) {
                tbody.innerHTML = `<tr><td colspan="10" class="text-secondary" style="padding: 2rem; text-align: center;">No history logs generated yet for this period.</td></tr>`;

                document.getElementById('report-total').innerText = '0';
                document.getElementById('report-up').innerText = '0';
                document.getElementById('report-down').innerText = '0';
                document.getElementById('report-uptime').innerText = '0%';
                return;
            }

            data.forEach(h => {
                total++;
                if (h.current === 'online') upCount++;
                else downCount++;
                uptimeSum += h.uptime_pct || 0;

                const tr = document.createElement('tr');
                const statusBadge = h.current === 'online' ? '<span class="status-indicator online"></span>up' : '<span class="status-indicator offline"></span>down';

                tr.innerHTML = `
                    <td>${h.date}</td>
                    <td><strong>${escapeHtml(h.name)}</strong></td>
                    <td><span class="tag-badge" style="background: rgba(255,255,255,0.1); margin: 0;">${escapeHtml(h.type)}</span></td>
                    <td>${escapeHtml(h.category)}</td>
                    <td>${escapeHtml(h.host)}</td>
                    <td style="text-transform: uppercase;">${statusBadge}</td>
                    <td>${h.checks}</td>
                    <td><span class="text-success">${h.up}</span> / <span class="text-danger">${h.down}</span></td>
                    <td>${h.uptime_pct.toFixed(1)}%</td>
                    <td>${h.avg_latency.toFixed(1)} ms</td>
                `;
                tbody.appendChild(tr);
            });

            document.getElementById('report-total').innerText = total;
            document.getElementById('report-up').innerText = upCount;
            document.getElementById('report-down').innerText = downCount;
            document.getElementById('report-uptime').innerText = total > 0 ? (uptimeSum / total).toFixed(1) + '%' : '0%';

            const curSpeed = document.getElementById('speed-download')?.innerText || '--';
            document.getElementById('report-speed').innerText = period === 'today' ? curSpeed.replace('Mbps', '').trim() : '--';
        })
        .catch(err => console.error('Error fetching global history:', err));
}

// Utility
function escapeHtml(value) {
    const entities = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
    return String(value ?? '').replace(/[&<>"']/g, c => entities[c]);
}

// Speed Test Handlers
function fetchSpeedTest() {
    fetch('/api/speedtest')
        .then(res => res.json())
        .then(data => {
            const btn = document.getElementById('btn-run-speedtest');
            if (data.is_running) {
                btn.innerHTML = `<span class="btn-icon">⏳</span> Testing...`;
                btn.disabled = true;
            } else {
                btn.innerHTML = `<span class="btn-icon">⚡</span> Run Speed Test`;
                btn.disabled = false;
            }

            if (data.download_speed > 0) {
                document.getElementById('speed-download').innerHTML = `${data.download_speed.toFixed(2)} <span class="unit">Mbps</span>`;
            }
            if (data.upload_speed > 0) {
                document.getElementById('speed-upload').innerHTML = `${data.upload_speed.toFixed(2)} <span class="unit">Mbps</span>`;
            }
            if (data.latency > 0) {
                document.getElementById('speed-latency').innerHTML = `${data.latency} <span class="unit">ms</span>`;
            }
            if (data.timestamp && new Date(data.timestamp).getFullYear() > 2000) {
                const date = new Date(data.timestamp);
                document.getElementById('speed-time').innerText = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
            }
            document.getElementById('speed-isp-info').innerText = data.isp ? `— ${data.isp} (${data.ip_address})` : '';
        })
        .catch(err => console.error('Speedtest fetch error:', err));
}

function triggerSpeedTest() {
    fetch('/api/speedtest', { method: 'POST' })
        .then(() => {
            fetchSpeedTest();
        })
        .catch(err => console.error('Speedtest trigger error:', err));
}

// IP Scanner
let scanPollTimer = null;

function fetchScan() {
    fetch('/api/scan')
        .then(res => res.json())
        .then(renderScan)
        .catch(err => console.error('Scan fetch error:', err));
}

function setScanStatus(text, isError) {
    const status = document.getElementById('scan-status');
    status.innerText = text;
    status.className = isError ? 'text-danger' : 'text-secondary';
}

function renderScan(state) {
    const ranges = state.local_ranges || [];
    const input = document.getElementById('scan_range');
    if (!input.value && !state.running && ranges.length > 0) {
        input.value = state.range || ranges[0];
    }

    const list = document.getElementById('scan_local_ranges');
    list.replaceChildren(...ranges.map(range => {
        const option = document.createElement('option');
        option.value = range;
        return option;
    }));

    document.getElementById('btn-scan-start').style.display = state.running ? 'none' : '';
    document.getElementById('btn-scan-stop').style.display = state.running ? '' : 'none';

    const hosts = state.hosts || [];
    if (state.running) {
        setScanStatus(`Scanning ${state.range}: ${state.done} of ${state.total} checked, ${hosts.length} found`);
    } else if (state.total > 0) {
        const finished = state.done < state.total ? 'Scan stopped' : 'Scan complete';
        setScanStatus(`${finished}: ${state.done} of ${state.total} checked, ${hosts.length} found`);
    } else {
        setScanStatus('Enter a range and start a scan.');
    }

    renderScanResults(hosts, state.running);

    clearTimeout(scanPollTimer);
    if (state.running && activeTab === 'scanner-tab') {
        scanPollTimer = setTimeout(fetchScan, 1000);
    }
}

function renderScanResults(hosts, running) {
    const tbody = document.getElementById('scan-results-list');
    if (hosts.length === 0) {
        tbody.innerHTML = `<tr><td colspan="6" class="empty-state text-secondary">${running ? 'Looking for devices...' : 'No devices to show.'}</td></tr>`;
        return;
    }

    // Hostnames come from the network, so results are only ever set as text.
    tbody.replaceChildren(...hosts.map(host => {
        const tr = document.createElement('tr');
        [host.ip, host.hostname || '-', host.mac || '-', `${host.latency}ms`, host.detected_by].forEach(text => {
            const td = document.createElement('td');
            td.textContent = text;
            tr.appendChild(td);
        });

        const action = document.createElement('td');
        const button = document.createElement('button');
        const monitored = targetsData.some(t => t.host === host.ip && t.type === 'ping');
        button.className = 'btn secondary btn-sm';
        button.textContent = monitored ? 'Monitoring' : 'Monitor';
        button.disabled = monitored;
        button.addEventListener('click', () => monitorScannedHost(host, button));
        action.appendChild(button);
        tr.appendChild(action);
        return tr;
    }));
}

function monitorScannedHost(host, button) {
    button.disabled = true;
    fetch('/api/targets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: host.hostname || host.ip, host: host.ip, type: 'ping', interval: 10, tag: '', category: 'General' })
    })
        .then(res => {
            if (!res.ok) throw new Error('Target rejected');
            targetsData.push({ host: host.ip, type: 'ping' });
            button.textContent = 'Monitoring';
            fetchTargets();
        })
        .catch(err => {
            button.disabled = false;
            setScanStatus(`Could not monitor ${host.ip}.`, true);
            console.error(err);
        });
}

function startScan() {
    const range = document.getElementById('scan_range').value.trim();
    if (!range) {
        setScanStatus('Enter an address range first.', true);
        return;
    }

    fetch('/api/scan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ range })
    })
        .then(async res => {
            if (!res.ok) throw new Error((await res.text()).trim());
            fetchScan();
        })
        .catch(err => setScanStatus(err.message, true));
}

function stopScan() {
    fetch('/api/scan', { method: 'DELETE' })
        .then(fetchScan)
        .catch(err => console.error('Scan stop error:', err));
}

function exportCsv() {
    if (activeExportId) {
        window.location.href = `/api/export?id=${activeExportId}`;
    }
}

function exportGlobalCsv() {
    let period = document.getElementById('report-period')?.value || 'today';
    let url = `/api/export_global?format=csv&period=${period}`;

    if (period === 'custom') {
        const start = document.getElementById('report-start-date').value;
        const end = document.getElementById('report-end-date').value;
        if (!start || !end) {
            alert("Please select both start and end dates before exporting.");
            return;
        }
        url += `&start=${start}&end=${end}`;
    }

    window.location.href = url;
}

function exportGlobalPdf() {
    let period = document.getElementById('report-period')?.value || 'today';
    let url = `/api/export_global?format=pdf&period=${period}`;

    if (period === 'custom') {
        const start = document.getElementById('report-start-date').value;
        const end = document.getElementById('report-end-date').value;
        if (!start || !end) {
            alert("Please select both start and end dates before exporting.");
            return;
        }
        url += `&start=${start}&end=${end}`;
    }

    window.location.href = url;
}

function testAlerts(type) {
    const btn = event.target;
    const oldText = btn.innerHTML;
    btn.innerHTML = 'Testing...';
    btn.disabled = true;

    fetch(`/api/test_alerts?type=${type}`, { method: 'POST' })
        .then(async res => {
            if (!res.ok) throw new Error((await res.text()).trim());
            alert(`${type} test alert sent. Check your inbox/app.`);
            btn.innerHTML = oldText;
            btn.disabled = false;
        })
        .catch(err => {
            alert(`The ${type} test failed: ${err.message}`);
            console.error(err);
            btn.innerHTML = oldText;
            btn.disabled = false;
        });
}

function shutdownApp() {
    if (confirm("Are you sure you want to stop the PingTick server daemon? You will need to manually restart the application to resume monitoring.")) {
        fetch('/api/shutdown', { method: 'POST' })
            .then(() => {
                document.body.innerHTML = '<div style="display:flex; justify-content:center; align-items:center; height:100vh; font-family: Inter, sans-serif; color: #fff; background: #0f172a;"><h1>PingTick Engine Offline. Safe to close.</h1></div>';
                setTimeout(() => {
                    window.open('', '_parent', '');
                    window.close();
                }, 500);
            })
            .catch(err => console.error(err));
    }
}
