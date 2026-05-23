/**
 * 🌌 Revive Cosmic Dashboard Client-Side Logic
 */

// ─── 1. State Management ───
const state = {
    activeWorkspace: null,
    registeredWorkspaces: [],
    manifest: null,
    activeProfile: "base",
    driftReport: null,
    diagnosticsReport: null,
    isRestoring: false,
};

// ─── 2. DOM Elements ───
const el = {
    // Workspaces
    activeWsName: document.getElementById("active-ws-name"),
    activeWsPath: document.getElementById("active-ws-path"),
    workspacesList: document.getElementById("workspaces-list"),
    wsRegisterForm: document.getElementById("ws-register-form"),
    newWsPath: document.getElementById("new-ws-path"),
    newWsName: document.getElementById("new-ws-name"),
    
    // Profiles & Navigation
    profilesGrid: document.getElementById("profiles-grid"),
    currentProfileBadge: document.getElementById("current-profile-badge"),
    btnCreateProfileModal: document.getElementById("btn-create-profile-modal"),
    
    // Diagnostics Panel
    diagSummary: document.getElementById("diag-summary"),
    
    // Controls
    btnCheckDrift: document.getElementById("btn-check-drift"),
    btnOpenRestore: document.getElementById("btn-open-restore"),
    inheritanceContainer: document.getElementById("inheritance-flow-container"),
    
    // Assets & Packages
    assetsCountBadge: document.getElementById("assets-count-badge"),
    assetsTableBody: document.getElementById("assets-table-body"),
    importAssetForm: document.getElementById("import-asset-form"),
    importSrc: document.getElementById("import-src"),
    importId: document.getElementById("import-id"),
    importType: document.getElementById("import-type"),
    importTarget: document.getElementById("import-target"),
    secretKeyGroup: document.getElementById("secret-key-group"),
    importRecipient: document.getElementById("import-recipient"),
    packagesSummaryPanel: document.getElementById("packages-summary-panel"),
    
    // Modals
    restoreModal: document.getElementById("restore-modal"),
    btnCloseRestoreModal: document.getElementById("btn-close-restore-modal"),
    restoreConfigForm: document.getElementById("restore-config-form"),
    restoreDryRun: document.getElementById("restore-dry-run"),
    restoreIdentity: document.getElementById("restore-identity"),
    btnTriggerRestore: document.getElementById("btn-trigger-restore"),
    restoreTerminalContainer: document.getElementById("restore-terminal-container"),
    restoreLogsBody: document.getElementById("restore-logs-body"),
    terminalPulse: document.getElementById("terminal-pulse"),
    restoreSuccessBanner: document.getElementById("restore-success-banner"),
    successTxId: document.getElementById("success-tx-id"),
    
    // Diff Modal
    diffModal: document.getElementById("diff-modal"),
    btnCloseDiffModal: document.getElementById("btn-close-diff-modal"),
    diffAssetId: document.getElementById("diff-asset-id"),
    diffAssetTarget: document.getElementById("diff-asset-target"),
    diffContentBody: document.getElementById("diff-content-body"),
    
    // Create Profile Modal
    createProfileModal: document.getElementById("create-profile-modal"),
    btnCloseProfileModal: document.getElementById("btn-close-profile-modal"),
    createProfileForm: document.getElementById("create-profile-form"),
    newProfileName: document.getElementById("new-profile-name"),
    profileExtendsCheckboxes: document.getElementById("profile-extends-checkboxes"),
    profileAssetsCheckboxes: document.getElementById("profile-assets-checkboxes"),
    profileSecretsCheckboxes: document.getElementById("profile-secrets-checkboxes"),
    profilePackagesCheckboxes: document.getElementById("profile-packages-checkboxes"),

    // Confetti
    confettiCanvas: document.getElementById("confetti-canvas"),

    // Tabs Navigation Hub
    navTabs: null,
    tabContents: null,

    // Cryptographic Key Gen elements
    btnGenerateKeys: null,
    keypairDisplay: null,
    displayPubkey: null,
    displayPrivkey: null,
    btnCopyPubkey: null,
    btnCopyPrivkey: null,

    // Disaster Recovery elements
    journalsCountBadge: null,
    journalsTableBody: null,
    recoveryTerminalPulse: null,
    recoveryLogsBody: null,
};

const API_BASE = ""; // Relative to server root

// ─── 3. Initialization & Event Routing ───
document.addEventListener("DOMContentLoaded", () => {
    // API State Loading
    loadWorkspaceStatus();

    // Query active tab navigation elements
    el.navTabs = document.querySelectorAll(".nav-tab");
    el.tabContents = document.querySelectorAll(".tab-content");
    el.navTabs.forEach(tab => {
        tab.addEventListener("click", () => {
            const targetTab = tab.getAttribute("data-tab");
            switchTab(targetTab);
        });
    });

    // Query cryptographic Key Generator elements
    el.btnGenerateKeys = document.getElementById("btn-generate-keys");
    el.keypairDisplay = document.getElementById("keypair-display");
    el.displayPubkey = document.getElementById("display-pubkey");
    el.displayPrivkey = document.getElementById("display-privkey");
    el.btnCopyPubkey = document.getElementById("btn-copy-pubkey");
    el.btnCopyPrivkey = document.getElementById("btn-copy-privkey");

    if (el.btnGenerateKeys) {
        el.btnGenerateKeys.addEventListener("click", handleKeyGeneration);
    }
    if (el.btnCopyPubkey) {
        el.btnCopyPubkey.addEventListener("click", () => copyToClipboard(el.displayPubkey.value, el.btnCopyPubkey));
    }
    if (el.btnCopyPrivkey) {
        el.btnCopyPrivkey.addEventListener("click", () => copyToClipboard(el.displayPrivkey.value, el.btnCopyPrivkey));
    }

    // Query Disaster Recovery elements
    el.journalsCountBadge = document.getElementById("journals-count-badge");
    el.journalsTableBody = document.getElementById("journals-table-body");
    el.recoveryTerminalPulse = document.getElementById("recovery-terminal-pulse");
    el.recoveryLogsBody = document.getElementById("recovery-logs-body");

    // Event Registration
    el.wsRegisterForm.addEventListener("submit", handleWorkspaceRegister);
    el.btnCheckDrift.addEventListener("click", runDriftStatusAnalysis);
    
    // Modals Open/Close
    el.btnOpenRestore.addEventListener("click", () => openModal(el.restoreModal));
    el.btnCloseRestoreModal.addEventListener("click", () => closeModal(el.restoreModal));
    el.btnCloseDiffModal.addEventListener("click", () => closeModal(el.diffModal));
    el.btnCreateProfileModal.addEventListener("click", openCreateProfileModal);
    el.btnCloseProfileModal.addEventListener("click", () => closeModal(el.createProfileModal));

    // Global Modal Escape Key
    document.addEventListener("keydown", (e) => {
        if (e.key === "Escape") {
            document.querySelectorAll(".modal-overlay.visible").forEach(closeModal);
        }
    });

    // Global Modal Backdrop Click
    document.querySelectorAll(".modal-overlay").forEach(modal => {
        modal.addEventListener("click", (e) => {
            if (e.target === modal) {
                closeModal(modal);
            }
        });
    });

    // Secret Encryption key visual visibility
    el.importType.addEventListener("change", () => {
        if (el.importType.value === "secret") {
            el.secretKeyGroup.classList.remove("hidden");
        } else {
            el.secretKeyGroup.classList.add("hidden");
        }
    });

    // Auto-predict Asset ID and Target Destination on source entry
    el.importSrc.addEventListener("input", () => {
        const path = el.importSrc.value.trim();
        if (path) {
            const basename = path.split(/[/\\]/).pop();
            if (basename) {
                if (!el.importId.value) el.importId.value = basename.replace(/\./g, "_");
                if (!el.importTarget.value) el.importTarget.value = `~/.config/revive_imported/${basename}`;
            }
        }
    });

    el.importAssetForm.addEventListener("submit", handleAssetImport);
    el.restoreConfigForm.addEventListener("submit", handleRestoreExecution);
    el.createProfileForm.addEventListener("submit", handleProfileCreation);

    // Initial confetti canvas sizing
    resizeConfettiCanvas();
    window.addEventListener("resize", resizeConfettiCanvas);
});

// ─── 4. API Service Calls ───

async function apiRequest(endpoint, options = {}) {
    const url = `${API_BASE}${endpoint}`;
    try {
        const response = await fetch(url, {
            ...options,
            headers: {
                "Content-Type": "application/json",
                ...(options.headers || {}),
            },
        });
        const data = await response.json();
        if (!response.ok) {
            throw new Error(data.error || `HTTP error ${response.status}`);
        }
        return data;
    } catch (error) {
        console.error(`API Request to ${endpoint} failed:`, error);
        alert(`API Error: ${error.message}`);
        throw error;
    }
}

async function loadWorkspaceStatus() {
    try {
        const wsData = await apiRequest("/api/workspace");
        state.activeWorkspace = wsData.active_workspace;
        state.registeredWorkspaces = wsData.registered_workspaces;
        
        updateWorkspaceUI();
        
        if (state.activeWorkspace) {
            await loadManifest();
            runDoctorDiagnostics();
        } else {
            showNoWorkspaceState();
        }
    } catch (err) {
        console.error("Failed loading initial workspace status:", err);
    }
}

async function loadManifest() {
    try {
        const manifest = await apiRequest("/api/manifest");
        state.manifest = manifest;
        
        // Default to first profile if current active is not present
        const profiles = Object.keys(manifest.profiles || {});
        if (profiles.length > 0 && !profiles.includes(state.activeProfile)) {
            state.activeProfile = profiles.includes("base") ? "base" : profiles[0];
        }
        
        updateManifestUI();
    } catch (err) {
        console.error("Failed loading manifest:", err);
    }
}

async function runDoctorDiagnostics() {
    if (!state.activeWorkspace) return;
    el.diagSummary.innerHTML = `<span class="diag-spinner">Running diagnostic check...</span>`;
    try {
        const report = await apiRequest("/api/action/doctor", {
            method: "POST",
            body: JSON.stringify({ profile: state.activeProfile }),
        });
        state.diagnosticsReport = report;
        renderDoctorDiagnostics();
    } catch (err) {
        el.diagSummary.innerHTML = `<span class="text-dim">Diagnostics failed. Check console.</span>`;
    }
}

// ─── 5. UI Rendering & Updates ───

function updateWorkspaceUI() {
    if (state.activeWorkspace) {
        el.activeWsName.textContent = state.activeWorkspace.name;
        el.activeWsPath.textContent = state.activeWorkspace.path;
    } else {
        el.activeWsName.textContent = "No active workspace";
        el.activeWsPath.textContent = "Please register or select a repository directory";
    }

    // Render other workspaces
    el.workspacesList.innerHTML = "";
    state.registeredWorkspaces.forEach(ws => {
        if (state.activeWorkspace && ws.path === state.activeWorkspace.path) return;
        
        const wsCard = document.createElement("div");
        wsCard.className = "workspace-card";
        wsCard.innerHTML = `
            <div class="ws-info">
                <span class="ws-icon">📁</span>
                <div class="ws-details">
                    <span class="ws-name">${escapeHtml(ws.name)}</span>
                    <span class="ws-path">${escapeHtml(ws.path)}</span>
                </div>
            </div>
        `;
        wsCard.addEventListener("click", () => handleWorkspaceSwitch(ws.name));
        el.workspacesList.appendChild(wsCard);
    });
}

function showNoWorkspaceState() {
    el.profilesGrid.innerHTML = `<p class="text-dim center-text col-span-2">Register workspace to load profiles.</p>`;
    el.inheritanceContainer.innerHTML = `<div class="flow-placeholder">No active workspace configured.</div>`;
    el.assetsTableBody.innerHTML = `<tr><td colspan="6" class="center-text text-dim">Please register or configure an active workspace first.</td></tr>`;
    el.packagesSummaryPanel.innerHTML = `<p class="text-dim">No workspace detected.</p>`;
    el.diagSummary.innerHTML = `<span class="text-dim">No workspace diagnostic.</span>`;
    el.assetsCountBadge.textContent = "0 Assets";
}

function updateManifestUI() {
    if (!state.manifest) return;
    
    el.currentProfileBadge.textContent = state.activeProfile;

    // Render Profiles selection grid
    el.profilesGrid.innerHTML = "";
    const profiles = Object.keys(state.manifest.profiles || {});
    profiles.forEach(pname => {
        const card = document.createElement("div");
        card.className = `profile-card ${pname === state.activeProfile ? "active" : ""}`;
        card.textContent = pname;
        card.addEventListener("click", () => {
            state.activeProfile = pname;
            document.querySelectorAll(".profile-card").forEach(c => c.classList.remove("active"));
            card.classList.add("active");
            
            // Switch profile details
            updateManifestUI();
            runDoctorDiagnostics();
            runDriftStatusAnalysis();
        });
        el.profilesGrid.appendChild(card);
    });

    // Render Inheritance Map flow
    renderInheritanceFlow();

    // Render configured Assets pooled locally
    renderAssetsPool();

    // Render Packages Checklist
    renderPackagesChecklist();
}

function renderInheritanceFlow() {
    if (!state.manifest || !state.activeProfile) return;
    const profile = state.manifest.profiles[state.activeProfile];
    if (!profile) return;

    el.inheritanceContainer.innerHTML = "";
    const nodes = [];

    // Traverse extensions
    const collectInheritance = (name) => {
        nodes.unshift(name);
        const p = state.manifest.profiles[name];
        if (p && p.extends && p.extends.length > 0) {
            // Take first extension branch for simple high-end flow display
            collectInheritance(p.extends[0]);
        }
    };
    collectInheritance(state.activeProfile);

    const flowWrapper = document.createElement("div");
    flowWrapper.className = "flow-nodes";

    nodes.forEach((nodeName, idx) => {
        const node = document.createElement("div");
        node.className = `flow-node ${nodeName === state.activeProfile ? "active" : ""}`;
        node.textContent = nodeName;
        flowWrapper.appendChild(node);

        if (idx < nodes.length - 1) {
            const arrow = document.createElement("span");
            arrow.className = "flow-arrow";
            arrow.innerHTML = "➔";
            flowWrapper.appendChild(arrow);
        }
    });

    el.inheritanceContainer.appendChild(flowWrapper);
}

function renderAssetsPool() {
    if (!state.manifest) return;

    const assets = state.manifest.assets || [];
    const secrets = state.manifest.secrets || [];
    const totalAssets = assets.length + secrets.length;
    el.assetsCountBadge.textContent = `${totalAssets} Registered`;

    // Empty list state
    if (totalAssets === 0) {
        el.assetsTableBody.innerHTML = `<tr><td colspan="6" class="center-text text-dim">No assets or secrets registered in this workspace yet. Use the Import Station!</td></tr>`;
        return;
    }

    el.assetsTableBody.innerHTML = "";

    // Helper to add rows
    const appendRow = (item, isSecret) => {
        const tr = document.createElement("tr");
        tr.id = `asset-row-${item.id}`;
        
        let statusBadge = `<span class="badge">Auditing...</span>`;
        let actionButton = `<button class="cyber-btn sm borderless disabled" disabled>Diff</button>`;

        if (state.driftReport && state.driftReport.assets) {
            const drift = state.driftReport.assets[item.id];
            if (drift) {
                if (drift.status === "in_sync") {
                    statusBadge = `<span class="status-badge in_sync">✓ In Sync</span>`;
                } else if (drift.status === "missing") {
                    statusBadge = `<span class="status-badge missing">⚠ Missing</span>`;
                } else if (drift.status === "modified") {
                    statusBadge = `<span class="status-badge drifted">Drifted</span>`;
                    actionButton = `
                        <button class="cyber-btn sm cyan border" onclick="viewAssetDiff('${item.id}')">
                            <span>Diff Compare</span>
                        </button>
                    `;
                } else {
                    statusBadge = `<span class="status-badge error">⚠ Audit Error</span>`;
                }
            }
        }

        tr.innerHTML = `
            <td><strong>${escapeHtml(item.id)}</strong></td>
            <td><span class="badge">${isSecret ? "encrypted secret" : escapeHtml(item.type)}</span></td>
            <td><code class="text-dim">${escapeHtml(item.source)}</code></td>
            <td><code>${escapeHtml(item.target)}</code></td>
            <td id="asset-status-${item.id}">${statusBadge}</td>
            <td>${actionButton}</td>
        `;
        el.assetsTableBody.appendChild(tr);
    };

    // Render Assets then Secrets
    assets.forEach(a => appendRow(a, false));
    secrets.forEach(s => appendRow(s, true));
}

function renderPackagesChecklist() {
    if (!state.manifest || !state.activeProfile) return;
    const profile = state.manifest.profiles[state.activeProfile];
    if (!profile) return;

    const listContainer = el.packagesSummaryPanel.querySelector(".pkg-list-container");
    listContainer.innerHTML = "";

    const packages = state.manifest.packages || {};
    let renderedCount = 0;

    const renderProvider = (title, items) => {
        if (!items || items.length === 0) return;
        renderedCount++;
        
        const group = document.createElement("div");
        group.className = "pkg-provider-group";
        group.innerHTML = `
            <div class="pkg-provider-title">${title}</div>
            <div class="pkg-items-list">
                ${items.map(item => `<span class="pkg-badge">${escapeHtml(item)}</span>`).join("")}
            </div>
        `;
        listContainer.appendChild(group);
    };

    // Render registered global packages matching profile subscriptions
    const subPackages = profile.packages || [];
    if (subPackages.includes("brew")) renderProvider("Homebrew Packages", packages.brew);
    if (subPackages.includes("apt")) renderProvider("Apt Packages", packages.apt);
    if (subPackages.includes("flatpak")) renderProvider("Flatpak Packages", packages.flatpak);
    if (subPackages.includes("snap")) renderProvider("Snap Packages", packages.snap);
    
    if (subPackages.includes("docker") && packages.docker && packages.docker.images && packages.docker.images.length > 0) {
        renderProvider("Docker Container Images", packages.docker.images);
    }
    if (subPackages.includes("node") && packages.node) {
        const nodeVer = packages.node.version || (packages.node.version_file ? `from ${packages.node.version_file}` : "latest");
        renderProvider("Node Environment Provisioning", [nodeVer]);
    }

    if (renderedCount === 0) {
        listContainer.innerHTML = `<p class="text-dim">No native package dependencies listed in this profile.</p>`;
    }
}

function renderDoctorDiagnostics() {
    if (!state.diagnosticsReport) return;
    const report = state.diagnosticsReport;
    
    el.diagSummary.innerHTML = "";
    
    // Tools checks
    const systemTools = ["age", "git", "brew", "apt", "docker"];
    const issues = report.issues || [];
    
    // Render health status details
    const totalIssues = issues.length;
    
    const countCard = document.createElement("div");
    countCard.className = `diag-item ${totalIssues > 0 ? "issue" : "healthy"}`;
    countCard.innerHTML = `
        <span>Audit Status</span>
        <strong>${totalIssues > 0 ? `${totalIssues} Issues Found` : "100% Healthy"}</strong>
    `;
    el.diagSummary.appendChild(countCard);

    if (totalIssues > 0) {
        // Show first 2 critical issues
        issues.slice(0, 2).forEach(issue => {
            const item = document.createElement("div");
            item.className = "diag-item issue";
            item.innerHTML = `
                <span class="text-dim" style="font-size: 0.75rem;">${escapeHtml(issue.category)}</span>
                <span style="font-size: 0.72rem; text-align: right;">${escapeHtml(issue.message)}</span>
            `;
            el.diagSummary.appendChild(item);
        });
    } else {
        const verifyCard = document.createElement("div");
        verifyCard.className = "diag-item healthy";
        verifyCard.innerHTML = `
            <span>Age Engine Capabilities</span>
            <span>Active & Verified</span>
        `;
        el.diagSummary.appendChild(verifyCard);
    }
}

// ─── 6. User Event Actions ───

async function handleWorkspaceSwitch(name) {
    try {
        await apiRequest("/api/workspace/switch", {
            method: "POST",
            body: JSON.stringify({ name }),
        });
        
        state.driftReport = null; // Clear previous drift
        await loadWorkspaceStatus();
    } catch (err) {
        console.error("Workspace switch failed:", err);
    }
}

async function handleWorkspaceRegister(e) {
    e.preventDefault();
    const path = el.newWsPath.value.trim();
    const name = el.newWsName.value.trim() || null;

    if (!path) return;

    try {
        await apiRequest("/api/workspace/register", {
            method: "POST",
            body: JSON.stringify({ path, name }),
        });

        el.newWsPath.value = "";
        el.newWsName.value = "";
        
        state.driftReport = null;
        await loadWorkspaceStatus();
    } catch (err) {
        console.error("Workspace registration failed:", err);
    }
}

async function runDriftStatusAnalysis() {
    if (!state.activeWorkspace || !state.activeProfile) return;
    
    el.btnCheckDrift.disabled = true;
    el.btnCheckDrift.innerHTML = `<span>Auditing Environment...</span>`;
    
    try {
        const report = await apiRequest("/api/action/status", {
            method: "POST",
            body: JSON.stringify({ profile: state.activeProfile }),
        });
        state.driftReport = report;
        
        // Re-render asset rows with active statuses
        renderAssetsPool();
    } catch (err) {
        console.error("Drift check failed:", err);
    } finally {
        el.btnCheckDrift.disabled = false;
        el.btnCheckDrift.innerHTML = `<span>Check Environment Drift</span>`;
    }
}

async function handleAssetImport(e) {
    e.preventDefault();
    const src = el.importSrc.value.trim();
    const aid = el.importId.value.trim();
    const type = el.importType.value;
    const target = el.importTarget.value.trim();
    const recipient = el.importRecipient.value.trim();

    if (!src || !aid || !target) return;

    const payload = {
        source_path: src,
        is_secret: type === "secret",
        asset_id: aid,
        target_path: target,
        profile: state.activeProfile,
        recipient: type === "secret" ? recipient : null,
    };

    try {
        await apiRequest("/api/asset/import", {
            method: "POST",
            body: JSON.stringify(payload),
        });

        // Reset imports fields
        el.importSrc.value = "";
        el.importId.value = "";
        el.importTarget.value = "";
        el.importRecipient.value = "";

        // Reload Manifest UI and Drift status
        await loadManifest();
        runDoctorDiagnostics();
        runDriftStatusAnalysis();
    } catch (err) {
        console.error("Asset import failed:", err);
    }
}

async function handleRestoreExecution(e) {
    e.preventDefault();
    if (state.isRestoring) return;

    state.isRestoring = true;
    el.btnTriggerRestore.disabled = true;
    el.btnTriggerRestore.innerHTML = `<span>Awaiting Transaction Lock...</span>`;
    el.restoreTerminalContainer.classList.remove("hidden");
    el.restoreLogsBody.innerHTML = `<div class="terminal-line warning">[system] Acquiring flock process lock at ~/.config/rv/rv.lock ...</div>`;
    el.terminalPulse.classList.add("active");
    el.restoreSuccessBanner.classList.add("hidden");

    const payload = {
        profile: state.activeProfile,
        identity: el.restoreIdentity.value.trim() || null,
        dry_run: el.restoreDryRun.checked,
    };

    try {
        const res = await apiRequest("/api/action/restore", {
            method: "POST",
            body: JSON.stringify(payload),
        });

        // Parse logs line by line
        const logs = res.logs || "";
        renderTerminalLogs(logs);

        if (res.success) {
            el.successTxId.textContent = res.tx_id || "DRY-RUN-PLAN";
            el.restoreSuccessBanner.classList.remove("hidden");
            // Trigger glorious interactive confetti
            triggerCelebrationConfetti();
        } else {
            const line = document.createElement("div");
            line.className = "terminal-line error";
            line.textContent = `[restore error] ${res.error}`;
            el.restoreLogsBody.appendChild(line);
        }
    } catch (err) {
        const line = document.createElement("div");
        line.className = "terminal-line error";
        line.textContent = `[connection crash] Restoration process encountered exception: ${err.message}`;
        el.restoreLogsBody.appendChild(line);
    } finally {
        state.isRestoring = false;
        el.btnTriggerRestore.disabled = false;
        el.btnTriggerRestore.innerHTML = `<span>Acquire Process Lock & Synchronize</span>`;
        el.terminalPulse.classList.remove("active");
        
        // Reload State
        loadWorkspaceStatus();
    }
}

function renderTerminalLogs(logText) {
    const lines = logText.split("\n");
    el.restoreLogsBody.innerHTML = "";
    lines.forEach(rawLine => {
        if (!rawLine.trim()) return;
        const line = document.createElement("div");
        line.className = "terminal-line";
        
        if (rawLine.includes("Error:") || rawLine.includes("CRITICAL") || rawLine.includes("ERROR")) {
            line.className += " error";
        } else if (rawLine.includes("Warning:") || rawLine.includes("WARNING")) {
            line.className += " warning";
        } else if (rawLine.includes("Step") || rawLine.includes("✓")) {
            line.className += " success";
        }
        
        line.textContent = rawLine;
        el.restoreLogsBody.appendChild(line);
    });
    // Auto Scroll
    el.restoreLogsBody.scrollTop = el.restoreLogsBody.scrollHeight;
}

// ─── 7. Interactive Diffs Viewer ───

async function viewAssetDiff(assetId) {
    if (!state.driftReport || !state.activeWorkspace) return;
    
    const activeWs = state.activeWorkspace;
    el.diffAssetId.textContent = assetId;

    const assetStatus = state.driftReport.assets[assetId];
    if (!assetStatus) return;
    el.diffAssetTarget.textContent = assetStatus.target;

    el.diffContentBody.innerHTML = `<span class="text-dim">Computing local file comparison differences...</span>`;
    openModal(el.diffModal);

    try {
        // Fetch manifest and profile keys to perform a precise unified difflib check or request diff service
        // Since http.server uses simple API, we can fetch drift details or run `rv diff`
        // Instead of calling a new process, we will perform a lightweight GET on differences using difflib or simple line compares
        // We will mock/calculate diff from details since StatusService contains details or we can run custom compare
        // Let's create an elegant visual rendering of the diff. StatusService _check_asset_drift returns comparison
        // Let's call /api/action/status again or read details if included in status.
        // Wait, StatusService get_status does not return full unified diff string but we can request unified diff from StatusService.
        // Let's check status return in status.py: it compares hashes. Let's see if we can get diff.
        // Wait, status.py has status details. We can calculate differences by calling an API or returning details.
        // Since we want this to be extremely high-end, let's implement a clean diff parser in JavaScript.
        // But wait! We need the actual diff text. Let's send a request or compute a diff by sending file contents.
        // Wait! We can generate a simple visual representation of diff, or read details.
        // Let's check: does StatusService have a diff method? Yes, StatusService handles it or main.py does diff.
        // Wait! Can we perform a post to read files? Yes! We can fetch the raw source file content and target file content, and run a standard LCS diff in JavaScript! That's incredibly elegant, self-contained, and works offline without adding any extra python modules!
        // Let's do that! That's extremely robust and high-end.
        // Wait, is there a simple API to read file content? No, but wait, the Web Server has endpoints.
        // Let's make sure the client can fetch difference lines. We can add a simple API endpoint `/api/action/diff` to our server!
        // Ah! Let's check if we already registered `/api/action/diff`. We registered `/api/action/status` and `/api/action/doctor`. We can easily extend our server to handle `/api/action/diff` as well!
        // Wait, let's see how our `server.py` handles API requests. It's extremely clean, we can edit `/api/action/diff` inside it or fetch both and compare! We can just return standard difflib lines.
        // Let's check if we can compute the diff inside Javascript or if we can run a simple API command to compute diff.
        // Let's check if we can add a `/api/action/diff` endpoint in `server.py` later if needed, or if we can read the source file relative to the repo and the target file and compare them.
        // Let's see. Let's implement a simple file compare or request the server. Let's just create a highly elegant visual layout.
        // Let's write the endpoint `/api/action/diff` to fetch the colored unified diff of the asset!
        // To do that, we will implement `/api/action/diff` in `server.py` which uses Python's standard `difflib.unified_diff` to compare the resolved asset/secret and the system target, returning the diff lines as a list of strings! This is incredibly robust, completely standard python, and returns perfect unified diffs!
        
        const diffReport = await apiRequest("/api/action/diff", {
            method: "POST",
            body: JSON.stringify({
                profile: state.activeProfile,
                asset_id: assetId,
            }),
        });

        renderDiffLines(diffReport.diff_lines || []);
    } catch (err) {
        el.diffContentBody.innerHTML = `<span class="terminal-line error">Failed to compute differences: ${escapeHtml(err.message)}</span>`;
    }
}

function renderDiffLines(lines) {
    if (!lines || lines.length === 0) {
        el.diffContentBody.innerHTML = `<span class="status-badge in_sync">✓ Files are structurally in sync. No drift detected.</span>`;
        return;
    }

    el.diffContentBody.innerHTML = "";
    lines.forEach(line => {
        const span = document.createElement("span");
        if (line.startsWith("+") && !line.startsWith("+++")) {
            span.className = "diff-add";
        } else if (line.startsWith("-") && !line.startsWith("---")) {
            span.className = "diff-del";
        } else if (line.startsWith("@@")) {
            span.className = "diff-chunk";
        }
        span.textContent = line + "\n";
        el.diffContentBody.appendChild(span);
    });
}

// ─── 8. Profile Creation & Manifest Updates ───

function openCreateProfileModal() {
    if (!state.manifest) return;

    el.profileExtendsCheckboxes.innerHTML = "";
    el.profileAssetsCheckboxes.innerHTML = "";
    el.profileSecretsCheckboxes.innerHTML = "";
    el.profilePackagesCheckboxes.innerHTML = "";

    const profiles = Object.keys(state.manifest.profiles || {});
    const assets = state.manifest.assets || [];
    const secrets = state.manifest.secrets || [];

    // Extends
    profiles.forEach(p => {
        el.profileExtendsCheckboxes.appendChild(createCheckboxItem("extends", p, p));
    });

    // Assets
    assets.forEach(a => {
        el.profileAssetsCheckboxes.appendChild(createCheckboxItem("assets", a.id, `${a.id} (${a.type} -> ${a.target})`));
    });

    // Secrets
    secrets.forEach(s => {
        el.profileSecretsCheckboxes.appendChild(createCheckboxItem("secrets", s.id, `${s.id} (secret -> ${s.target})`));
    });

    // Packages
    ["brew", "apt", "flatpak", "snap", "docker", "node"].forEach(pkg => {
        el.profilePackagesCheckboxes.appendChild(createCheckboxItem("packages", pkg, pkg));
    });

    openModal(el.createProfileModal);
}

function createCheckboxItem(name, value, labelText) {
    const div = document.createElement("div");
    div.className = "form-group flex-row";
    div.innerHTML = `
        <input type="checkbox" name="${name}" value="${value}" id="chk-${name}-${value}">
        <label for="chk-${name}-${value}">
            <strong>${escapeHtml(value)}</strong>
            <span class="help-text">${escapeHtml(labelText)}</span>
        </label>
    `;
    return div;
}

async function handleProfileCreation(e) {
    e.preventDefault();
    const name = el.newProfileName.value.trim().replace(/\s+/g, "_");
    if (!name) return;

    // Read checkbox arrays
    const getCheckedValues = (name) => {
        const chks = document.querySelectorAll(`input[name="${name}"]:checked`);
        return Array.from(chks).map(c => c.value);
    };

    const newProfile = {
        extends: getCheckedValues("extends"),
        assets: getCheckedValues("assets"),
        secrets: getCheckedValues("secrets"),
        packages: getCheckedValues("packages"),
    };

    // Deep merge/update manifest
    const updatedManifest = { ...state.manifest };
    updatedManifest.profiles[name] = newProfile;

    try {
        await apiRequest("/api/manifest", {
            method: "POST",
            body: JSON.stringify(updatedManifest),
        });

        closeModal(el.createProfileModal);
        el.newProfileName.value = "";
        
        state.activeProfile = name;
        await loadManifest();
        runDoctorDiagnostics();
        runDriftStatusAnalysis();
    } catch (err) {
        console.error("Profile creation failed:", err);
    }
}

// ─── 9. Custom Confetti Celebration Animation ───

let confettiInterval = null;
let confettiActive = false;
const confettiParticles = [];
const confettiColors = ["#a855f7", "#06b6d4", "#10b981", "#f59e0b", "#ef4444", "#3b82f6"];

function resizeConfettiCanvas() {
    el.confettiCanvas.width = window.innerWidth;
    el.confettiCanvas.height = window.innerHeight;
}

function triggerCelebrationConfetti() {
    if (confettiActive) return;
    confettiActive = true;
    confettiParticles.length = 0;
    
    // Create 120 particles
    for (let i = 0; i < 150; i++) {
        confettiParticles.push({
            x: Math.random() * el.confettiCanvas.width,
            y: Math.random() * -el.confettiCanvas.height - 20,
            size: Math.random() * 8 + 6,
            color: confettiColors[Math.floor(Math.random() * confettiColors.length)],
            speed: Math.random() * 4 + 3,
            angle: Math.random() * 360,
            spin: Math.random() * 4 - 2,
        });
    }

    const ctx = el.confettiCanvas.getContext("2d");
    
    function animate() {
        if (!confettiActive) return;
        ctx.clearRect(0, 0, el.confettiCanvas.width, el.confettiCanvas.height);
        
        let activeCount = 0;

        confettiParticles.forEach(p => {
            p.y += p.speed;
            p.angle += p.spin;
            
            // Check boundaries
            if (p.y < el.confettiCanvas.height) {
                activeCount++;
            }

            ctx.save();
            ctx.translate(p.x, p.y);
            ctx.rotate((p.angle * Math.PI) / 180);
            ctx.fillStyle = p.color;
            ctx.fillRect(-p.size / 2, -p.size / 2, p.size, p.size);
            ctx.restore();
        });

        if (activeCount > 0) {
            requestAnimationFrame(animate);
        } else {
            confettiActive = false;
            ctx.clearRect(0, 0, el.confettiCanvas.width, el.confettiCanvas.height);
        }
    }

    animate();

    // Auto stop after 5 seconds
    setTimeout(() => {
        confettiActive = false;
    }, 6000);
}

// ─── 10. Helper Utilities ───

// ARIA Live Announcer
function announceMessage(msg) {
    let announcer = document.getElementById("a11y-announcer");
    if (!announcer) {
        announcer = document.createElement("div");
        announcer.id = "a11y-announcer";
        announcer.setAttribute("aria-live", "polite");
        announcer.setAttribute("aria-atomic", "true");
        announcer.style.cssText = "position:absolute; width:1px; height:1px; padding:0; margin:-1px; overflow:hidden; clip:rect(0,0,0,0); border:0;";
        document.body.appendChild(announcer);
    }
    announcer.textContent = msg;
}

let lastFocusedElement = null;

function trapFocus(e) {
    if (e.key !== 'Tab') return;
    const focusableElements = e.currentTarget.querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])');
    if (focusableElements.length === 0) return;
    
    const firstElement = focusableElements[0];
    const lastElement = focusableElements[focusableElements.length - 1];
    
    if (e.shiftKey) {
        if (document.activeElement === firstElement) {
            lastElement.focus();
            e.preventDefault();
        }
    } else {
        if (document.activeElement === lastElement) {
            firstElement.focus();
            e.preventDefault();
        }
    }
}

function openModal(modal) {
    lastFocusedElement = document.activeElement;
    modal.classList.add("visible");
    modal.setAttribute("aria-hidden", "false");
    
    // Trap focus inside modal
    const focusableElements = modal.querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])');
    if (focusableElements.length > 0) {
        focusableElements[0].focus();
    }
    modal.addEventListener("keydown", trapFocus);
    announceMessage("Dialog opened");
}

function closeModal(modal) {
    modal.classList.remove("visible");
    modal.setAttribute("aria-hidden", "true");
    modal.removeEventListener("keydown", trapFocus);
    if (lastFocusedElement) {
        lastFocusedElement.focus();
    }
    
    if (modal === el.restoreModal) {
        // Clear terminal logging on close
        el.restoreLogsBody.innerHTML = "";
        el.restoreTerminalContainer.classList.add("hidden");
        el.restoreSuccessBanner.classList.add("hidden");
    }
    announceMessage("Dialog closed");
}

function escapeHtml(str) {
    if (!str) return "";
    return str
        .toString()
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#039;");
}

// ─── 11. Upgraded Dynamic Tabs Navigation Controller ───
function switchTab(tabId) {
    if (!el.navTabs || !el.tabContents) return;
    el.navTabs.forEach(tab => {
        if (tab.getAttribute("data-tab") === tabId) {
            tab.classList.add("active");
            tab.setAttribute("aria-selected", "true");
        } else {
            tab.classList.remove("active");
            tab.setAttribute("aria-selected", "false");
        }
    });

    el.tabContents.forEach(content => {
        if (content.id === `tab-${tabId}`) {
            content.classList.remove("hidden");
        } else {
            content.classList.add("hidden");
        }
    });

    // Automatically load data when accessing recovery section
    if (tabId === "recovery") {
        loadIncompleteJournals();
    }
}

// ─── 12. Age Keypair Generator Handler ───
async function handleKeyGeneration() {
    if (!el.btnGenerateKeys) return;
    el.btnGenerateKeys.disabled = true;
    el.btnGenerateKeys.innerHTML = "<span>Generating Cryptographic Keypair...</span>";
    
    try {
        const res = await apiRequest("/api/action/keygen", { method: "POST" });
        if (el.displayPubkey && el.displayPrivkey && el.keypairDisplay) {
            el.displayPubkey.value = res.public_key;
            el.displayPrivkey.value = res.private_key;
            el.keypairDisplay.classList.remove("hidden");
            
            // Auto fill in recipient public key in import forms for best-in-class UX flow!
            const importRecipient = document.getElementById("import-recipient");
            if (importRecipient) {
                importRecipient.value = res.public_key;
            }
        }
    } catch (err) {
        console.error("Failed to generate Age keys:", err);
    } finally {
        el.btnGenerateKeys.disabled = false;
        el.btnGenerateKeys.innerHTML = "<span>Generate Secure Keypair</span>";
    }
}

function copyToClipboard(text, buttonEl) {
    if (!text || !buttonEl) return;
    navigator.clipboard.writeText(text).then(() => {
        const origText = buttonEl.innerHTML;
        buttonEl.innerHTML = "<span>Copied!</span>";
        buttonEl.classList.add("copy-success");
        setTimeout(() => {
            buttonEl.innerHTML = origText;
            buttonEl.classList.remove("copy-success");
        }, 2000);
    }).catch(err => {
        console.error("Clipboard copy failed:", err);
    });
}

// ─── 13. Transaction Journal Disaster Recovery Handlers ───
async function loadIncompleteJournals() {
    if (!el.journalsTableBody) return;
    
    try {
        const res = await apiRequest("/api/action/recovery/list", { method: "POST" });
        const journals = res.journals || [];
        
        if (el.journalsCountBadge) {
            el.journalsCountBadge.textContent = `${journals.length} Interrupted`;
        }

        if (journals.length === 0) {
            el.journalsTableBody.innerHTML = `
                <tr>
                    <td colspan="5" class="center-text text-dim">No incomplete transactions found. Your system state is perfectly atomic!</td>
                </tr>
            `;
            return;
        }

        el.journalsTableBody.innerHTML = "";
        journals.forEach(journal => {
            const tr = document.createElement("tr");
            
            // Format timestamp nicely
            const date = new Date(journal.timestamp * 1000).toLocaleString();
            
            // Mutations details list
            const mutationsSummary = journal.entries.map(entry => {
                const parts = entry.target.split(/[/\\]/);
                const basename = parts[parts.length - 1] || entry.target;
                const opClass = entry.op.toLowerCase();
                return `<span class="journal-op-badge ${opClass}">${escapeHtml(entry.op)}</span> <code>${escapeHtml(basename)}</code>`;
            }).join("<br>");

            tr.innerHTML = `
                <td><code>${escapeHtml(journal.tx_id.substring(0, 8))}...</code></td>
                <td><span class="text-dim" style="font-size: 0.8rem;">${date}</span></td>
                <td><span class="status-badge drifted" style="text-transform: capitalize;">${escapeHtml(journal.status)}</span></td>
                <td><div style="max-height: 80px; overflow-y: auto; text-align: left; padding: 4px 0; line-height: 1.6;">${mutationsSummary}</div></td>
                <td>
                    <div style="display: flex; gap: 8px;">
                        <button class="cyber-btn sm green" onclick="triggerJournalRollback('${journal.tx_id}')">
                            <span>Rollback</span>
                        </button>
                        <button class="cyber-btn sm border" onclick="triggerJournalDiscard('${journal.tx_id}')">
                            <span>Discard</span>
                        </button>
                    </div>
                </td>
            `;
            el.journalsTableBody.appendChild(tr);
        });
    } catch (err) {
        console.error("Failed to load recovery journals:", err);
    }
}

async function triggerJournalRollback(txId) {
    if (!confirm(`Are you absolutely sure you want to perform a rollback on transaction ${txId}? This will restore all mutated files to their original pre-mutation backup state.`)) {
        return;
    }

    if (el.recoveryTerminalPulse) el.recoveryTerminalPulse.classList.add("active");
    logRecoveryLine(`[recovery] Initiating transaction rollback for ${txId}...`, "warning");

    try {
        const res = await apiRequest("/api/action/recovery/rollback", {
            method: "POST",
            body: JSON.stringify({ tx_id: txId })
        });
        
        if (res.success) {
            logRecoveryLine(`✓ Success: Transaction ${txId} has been successfully rolled back.`, "success");
            logRecoveryLine(`[recovery] Pre-mutation filesystem backups restored and locked directories synchronized.`, "success");
            triggerCelebrationConfetti();
        } else {
            logRecoveryLine(`⚠ Error: ${res.error || "Rollback process failed"}`, "error");
        }
    } catch (err) {
        logRecoveryLine(`⚠ Fatal Connection Crash: ${err.message}`, "error");
    } finally {
        if (el.recoveryTerminalPulse) el.recoveryTerminalPulse.classList.remove("active");
        loadIncompleteJournals();
    }
}

async function triggerJournalDiscard(txId) {
    if (!confirm(`Are you sure you want to discard the journal for transaction ${txId}? This will remove the transaction backup files and journal log WITHOUT changing any files on your system. This operation cannot be undone.`)) {
        return;
    }

    if (el.recoveryTerminalPulse) el.recoveryTerminalPulse.classList.add("active");
    logRecoveryLine(`[recovery] Discarding journal metadata for transaction ${txId}...`, "warning");

    try {
        const res = await apiRequest("/api/action/recovery/discard", {
            method: "POST",
            body: JSON.stringify({ tx_id: txId })
        });
        
        if (res.success) {
            logRecoveryLine(`[recovery] Transaction journal and rollback entries for ${txId} successfully discarded.`, "success");
        } else {
            logRecoveryLine(`⚠ Error: ${res.error || "Discard operation failed"}`, "error");
        }
    } catch (err) {
        logRecoveryLine(`⚠ Fatal Connection Crash: ${err.message}`, "error");
    } finally {
        if (el.recoveryTerminalPulse) el.recoveryTerminalPulse.classList.remove("active");
        loadIncompleteJournals();
    }
}

function logRecoveryLine(text, type = "") {
    if (!el.recoveryLogsBody) return;
    const line = document.createElement("div");
    line.className = "terminal-line";
    if (type) line.className += ` ${type}`;
    line.textContent = text;
    el.recoveryLogsBody.appendChild(line);
    el.recoveryLogsBody.scrollTop = el.recoveryLogsBody.scrollHeight;
}

// Bind to window to allow call from dynamically rendered elements
window.viewAssetDiff = viewAssetDiff;
window.triggerJournalRollback = triggerJournalRollback;
window.triggerJournalDiscard = triggerJournalDiscard;
