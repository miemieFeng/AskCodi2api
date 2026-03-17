function showTab(tabId, element) {
    document.querySelectorAll('.tab-content').forEach(el => el.style.display = 'none');
    document.getElementById(tabId).style.display = 'block';
    if(element) {
        document.querySelectorAll('.nav-btn').forEach(el => el.classList.remove('active'));
        element.classList.add('active');
    }

    if(tabId === 'dashboard') loadStats();
    if(tabId === 'accounts') loadAccounts();
    if(tabId === 'proxies') loadProxies();
    if(tabId === 'settings') loadConfig();
}

// Proxy toggle UI logic
function initProxyToggle() {
    const checkbox = document.getElementById('config-proxy-enabled');
    const slider = checkbox.nextElementSibling;
    const statusText = document.getElementById('proxy-status-text');

    function updateSlider() {
        if (checkbox.checked) {
            slider.style.backgroundColor = '#6b4cff';
            slider.innerHTML = '<span style="position:absolute;content:\'\';height:20px;width:20px;left:27px;bottom:3px;background-color:white;border-radius:50%;transition:0.3s;"></span>';
            statusText.textContent = '已启用';
            statusText.style.color = '#4caf50';
        } else {
            slider.style.backgroundColor = '#555';
            slider.innerHTML = '<span style="position:absolute;content:\'\';height:20px;width:20px;left:3px;bottom:3px;background-color:white;border-radius:50%;transition:0.3s;"></span>';
            statusText.textContent = '已关闭';
            statusText.style.color = '#a0a0a0';
        }
    }
    checkbox.addEventListener('change', updateSlider);
    updateSlider();
}

async function loadConfig() {
    try {
        const res = await fetch('/api/config');
        const data = await res.json();
        document.getElementById('config-batch-size').value = data.batch_size;
        document.getElementById('config-concurrency').value = data.concurrency;
        document.getElementById('config-interval').value = data.auto_register_interval_minutes;
        document.getElementById('config-threshold').value = data.min_account_threshold;
        document.getElementById('config-gptmail-key').value = data.gptmail_api_key;
        document.getElementById('config-proxy-enabled').checked = data.proxy_enabled;
        document.getElementById('config-initial-referral-code').value = data.initial_referral_code || '';
        document.getElementById('config-zenproxy-url').value = data.zenproxy_url || 'http://cn.azt.cc:13000';
        document.getElementById('config-zenproxy-api-key').value = data.zenproxy_api_key || '';
        initProxyToggle();
    } catch(e) {
        console.error("加载配置数据失败", e);
    }
}

async function saveConfig() {
    const payload = {
        batch_size: parseInt(document.getElementById('config-batch-size').value) || 5,
        concurrency: parseInt(document.getElementById('config-concurrency').value) || 2,
        auto_register_interval_minutes: parseInt(document.getElementById('config-interval').value) || 60,
        min_account_threshold: parseInt(document.getElementById('config-threshold').value) || 10,
        gptmail_api_key: document.getElementById('config-gptmail-key').value || 'gpt-test',
        proxy_enabled: document.getElementById('config-proxy-enabled').checked,
        initial_referral_code: document.getElementById('config-initial-referral-code').value || '',
        zenproxy_url: document.getElementById('config-zenproxy-url').value || 'http://cn.azt.cc:13000',
        zenproxy_api_key: document.getElementById('config-zenproxy-api-key').value || ''
    };

    try {
        const res = await fetch('/api/config', {
            method: 'PUT',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(payload)
        });
        if(res.ok) {
            alert('保存成功');
        } else {
            alert('保存失败');
        }
    } catch(e) {
        alert('保存请求发送失败');
    }
}

function translateStatus(status) {
    if (status === 'Active') return '正常';
    if (status === 'Exhausted') return '已耗尽';
    if (status === 'Banned') return '被封禁';
    if (status === 'Disabled (Manual)') return '已停用';
    return status;
}

async function loadLogs() {
    try {
        const res = await fetch('/api/registration/logs');
        const logs = await res.json();
        const logsDiv = document.getElementById('registration-logs');
        if (logsDiv) {
            if (!logs || logs.length === 0) {
                logsDiv.innerHTML = "暂无注册日志...";
            } else {
                logsDiv.innerHTML = logs.map(l => `[${l.timestamp}] ${l.message}`).join('<br>');
                logsDiv.scrollTop = logsDiv.scrollHeight;
            }
        }
    } catch(e) {
        console.error("加载日志失败", e);
    }
}

setInterval(() => {
    if (document.getElementById('accounts').style.display === 'block') {
        loadLogs();
        loadAccounts();
    }
}, 3000);

async function loadStats() {
    try {
        const res = await fetch('/api/dashboard/stats');
        const data = await res.json();
        document.getElementById('stat-total-accounts').innerText = data.total_accounts;
        document.getElementById('stat-active-accounts').innerText = data.active_accounts;
        document.getElementById('stat-total-proxies').innerText = data.total_proxies;
        document.getElementById('stat-total-tokens').innerText = data.total_tokens || 0;
    } catch(e) {
        console.error("加载面板数据失败", e);
    }
}

// Pagination state
let currentPage = 1;
let currentPageSize = 20;
let currentStatusFilter = '';

async function loadAccounts() {
    try {
        let url = `/api/accounts?page=${currentPage}&page_size=${currentPageSize}`;
        if (currentStatusFilter) url += `&status=${currentStatusFilter}`;
        const res = await fetch(url);
        const data = await res.json();
        const tbody = document.querySelector('#accounts-table tbody');
        tbody.innerHTML = '';
        (data.items || []).forEach(acc => {
            const dateStr = new Date(acc.created_at).toLocaleString();
            const translatedStatus = translateStatus(acc.status);
            tbody.innerHTML += `
                <tr>
                    <td>${acc.id}</td>
                    <td>${acc.email}</td>
                    <td><span style="color: ${acc.status === 'Active' ? '#4caf50' : '#cf6679'}">${translatedStatus}</span></td>
                    <td>${acc.tokens_remaining}</td>
                    <td>
                        <button class="primary-btn" onclick="refreshAccount(${acc.id})" style="margin-right: 5px;">刷新额度</button>
                        <button class="danger-btn" onclick="disableAccount(${acc.id})" style="margin-right: 5px;">停用</button>
                        <button class="danger-btn" onclick="deleteAccount(${acc.id})">删除</button>
                    </td>
                </tr>
            `;
        });

        // Render pagination
        renderPagination(data.page, data.total_pages, data.total);
    } catch(e) {
        console.error("加载账号列表失败", e);
    }
}

function renderPagination(page, totalPages, total) {
    const container = document.getElementById('pagination');
    if (!container) return;
    if (totalPages <= 1) {
        container.innerHTML = `<span style="color: var(--text-muted); font-size: 13px;">共 ${total} 条</span>`;
        return;
    }

    let html = `<span style="color: var(--text-muted); font-size: 13px; margin-right: 15px;">共 ${total} 条</span>`;
    html += `<button class="page-btn${page <= 1 ? ' disabled' : ''}" onclick="goToPage(${page - 1})" ${page <= 1 ? 'disabled' : ''}>&laquo; 上一页</button>`;

    const start = Math.max(1, page - 2);
    const end = Math.min(totalPages, page + 2);
    if (start > 1) {
        html += `<button class="page-btn" onclick="goToPage(1)">1</button>`;
        if (start > 2) html += `<span style="color: var(--text-muted); padding: 0 5px;">...</span>`;
    }
    for (let i = start; i <= end; i++) {
        html += `<button class="page-btn${i === page ? ' active' : ''}" onclick="goToPage(${i})">${i}</button>`;
    }
    if (end < totalPages) {
        if (end < totalPages - 1) html += `<span style="color: var(--text-muted); padding: 0 5px;">...</span>`;
        html += `<button class="page-btn" onclick="goToPage(${totalPages})">${totalPages}</button>`;
    }

    html += `<button class="page-btn${page >= totalPages ? ' disabled' : ''}" onclick="goToPage(${page + 1})" ${page >= totalPages ? 'disabled' : ''}>下一页 &raquo;</button>`;
    container.innerHTML = html;
}

function goToPage(page) {
    currentPage = page;
    loadAccounts();
}

function filterByStatus(status) {
    currentStatusFilter = status;
    currentPage = 1;
    loadAccounts();
    // Update active filter button
    document.querySelectorAll('.filter-btn').forEach(btn => btn.classList.remove('active'));
    event.target.classList.add('active');
}

async function triggerRegister() {
    await fetch('/api/accounts/register', { method: 'POST' });
    alert('已在后台触发自动注册任务！请稍后刷新页面查看。');
    loadAccounts();
}

async function refreshAccount(id) {
    try {
        const res = await fetch(`/api/accounts/${id}/refresh`, { method: 'POST' });
        const data = await res.json();
        if (res.ok && data.status === 'success') {
            loadAccounts();
            loadStats();
        } else {
            alert('刷新失败: ' + (data.error || '未知错误'));
        }
    } catch(e) {
        alert('刷新请求发送失败');
    }
}

async function refreshAllTokens() {
    await fetch('/api/accounts/refresh_all', { method: 'POST' });
    alert('已在后台启动批量刷新任务！为了避免触发频繁限制，刷新是逐个进行的，请稍后再次刷新页面。');
    loadAccounts();
}

async function disableAccount(id) {
    await fetch(`/api/accounts/${id}/disable`, { method: 'POST' });
    loadAccounts();
}

async function deleteAccount(id) {
    if(confirm('确定要删除这个账号吗？删除后不可恢复。')) {
        await fetch(`/api/accounts/${id}`, { method: 'DELETE' });
        loadAccounts();
    }
}

async function loadProxies() {
    try {
        const res = await fetch('/api/proxies');
        const data = await res.json();
        const tbody = document.querySelector('#proxies-table tbody');
        tbody.innerHTML = '';
        data.forEach(p => {
            const translatedStatus = translateStatus(p.status);
            tbody.innerHTML += `
                <tr>
                    <td>${p.id}</td>
                    <td>${p.url}</td>
                    <td><span style="color: ${p.status === 'Active' ? '#4caf50' : '#cf6679'}">${translatedStatus}</span></td>
                    <td>${p.fail_count}</td>
                    <td>
                        <button class="danger-btn" onclick="deleteProxy(${p.id})">删除</button>
                    </td>
                </tr>
            `;
        });
    } catch(e) {
        console.error("加载代理列表失败", e);
    }
}

async function addProxy() {
    const url = document.getElementById('proxy-url').value;
    if(!url) return;
    try {
        const res = await fetch('/api/proxies', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({url})
        });
        if(res.ok) {
            document.getElementById('proxy-url').value = '';
            loadProxies();
        } else {
            const data = await res.json();
            alert('添加代理失败: ' + (data.detail || '未知错误'));
        }
    } catch(e) {
        alert('发送添加代理请求失败');
    }
}

async function refreshZenProxies() {
    try {
        const res = await fetch('/api/proxies/refresh-zenproxy', {method: 'POST'});
        if(res.ok) {
            alert('已开始从 ZenProxy 获取代理，请稍候刷新列表查看结果');
        } else {
            alert('获取失败');
        }
    } catch(e) {
        alert('请求失败');
    }
}

async function deleteProxy(id) {
    if(confirm('确定要删除这个代理吗？')) {
        await fetch(`/api/proxies/${id}`, { method: 'DELETE' });
        loadProxies();
    }
}

document.addEventListener('DOMContentLoaded', () => {
    loadStats();
});
