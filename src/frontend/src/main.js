const versionElement = document.getElementById("version");
const statusElement = document.getElementById("status");
const bootstrapScreenElement = document.getElementById("bootstrap-screen");
const bootstrapMetaElement = document.getElementById("bootstrap-meta");
const bootstrapNodeListElement = document.getElementById("bootstrap-node-list");
const bootstrapErrorElement = document.getElementById("bootstrap-error");
const bootstrapRefreshButton = document.getElementById("bootstrap-refresh");
const bootstrapSelectionElement = document.getElementById("bootstrap-selection");
const bootstrapCustomHostElement = document.getElementById("bootstrap-custom-host");
const bootstrapCustomPortElement = document.getElementById("bootstrap-custom-port");
const bootstrapCustomConnectButton = document.getElementById("bootstrap-custom-connect");
const bootstrapPasswordMetaElement = document.getElementById("bootstrap-password-meta");
const bootstrapPasswordElement = document.getElementById("bootstrap-password");
const bootstrapPasswordSubmitButton = document.getElementById("bootstrap-password-submit");
const updateDialogElement = document.getElementById("update-dialog");
const updateMessageElement = document.getElementById("update-message");
const updateErrorElement = document.getElementById("update-error");
const updateNowButton = document.getElementById("update-now");
const exitAppButton = document.getElementById("exit-app");
const fieldElements = new Map(
    Array.from(document.querySelectorAll("[data-field]")).map((element) => [element.dataset.field, element]),
);
const appBridge = globalThis.go.desktop.App;
const runtimeBridge = globalThis.runtime;
const updateCountdownSeconds = 15;
const bootstrapRefreshIntervalMs = 5000;
const diskUsageRefreshIntervalMs = 2000;
const defaultPlaceholder = "Pending";
const dashboardEventBindings = [
    ["dashboard:this-node", "node"],
    ["dashboard:your-account", "account"],
    ["dashboard:network", "network"],
];

let updateCountdownTimer = null;
let bootstrapRefreshTimer = null;
let diskUsageTimer = null;
let countdownRemaining = updateCountdownSeconds;
let pendingUpdateStatus = null;
let dashboardEventUnsubscribers = [];
let bootstrapRequired = false;
let selectedBootstrapKey = "";
let connectingBootstrap = false;
let awaitingBootstrapPassword = false;
let pendingBootstrapSessionID = "";
let pendingBootstrapRecovery = false;

function formatVersion(value) {
    if (!value) {
        return "unknown";
    }

    if (value === "unavailable") {
        return value;
    }

    if (value.startsWith("v")) {
        return value;
    }

    if (/^\d+\.\d+\.\d+$/.test(value)) {
        return `v${value}`;
    }

    return value;
}

function setMetric(field, value) {
    const element = fieldElements.get(field);
    if (!element) {
        return;
    }

    const hasValue = value !== null && value !== undefined && String(value).trim() !== "";
    element.textContent = hasValue ? String(value) : element.dataset.placeholder || defaultPlaceholder;
    element.classList.toggle("is-placeholder", !hasValue);
}

function formatBytes(bytes) {
    if (!Number.isFinite(bytes) || bytes <= 0) {
        return "0 B";
    }

    const units = ["B", "KB", "MB", "GB", "TB"];
    let value = bytes;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
        value /= 1024;
        unitIndex += 1;
    }

    const decimals = value >= 100 || unitIndex === 0 ? 0 : value >= 10 ? 1 : 2;
    return `${value.toFixed(decimals)} ${units[unitIndex]}`;
}

function formatPercent(value) {
    if (!Number.isFinite(value) || value <= 0) {
        return "0.00%";
    }

    if (value < 0.01) {
        return `${value.toFixed(4)}%`;
    }

    return `${value.toFixed(2)}%`;
}

function formatDiskUsage(snapshot) {
    if (!snapshot || typeof snapshot !== "object") {
        return defaultPlaceholder;
    }

    return `${formatBytes(snapshot.totalBytes)} total (${formatBytes(snapshot.appBytes)} app + ${formatBytes(snapshot.dataBytes)} data) / ${formatBytes(snapshot.volumeBytes)} (${formatPercent(snapshot.usagePercent)}) • R ${Number(snapshot.readMbps || 0).toFixed(2)} Mb/s • W ${Number(snapshot.writeMbps || 0).toFixed(2)} Mb/s`;
}

function formatBandwidthUsage(snapshot) {
    if (!snapshot || typeof snapshot !== "object") {
        return defaultPlaceholder;
    }

    return `Up ${Number(snapshot.writeMbps || 0).toFixed(2)} Mb/s • Down ${Number(snapshot.readMbps || 0).toFixed(2)} Mb/s`;
}

function formatBootstrapLatency(node) {
    if (!node || typeof node !== "object") {
        return "Unknown";
    }

    if (!node.reachable) {
        return node.error || "Unreachable";
    }

    return `${Number(node.latencyMilliseconds || 0)} ms`;
}

function showBootstrapScreen() {
    bootstrapRequired = true;
    bootstrapScreenElement.classList.remove("hidden");
}

function hideBootstrapScreen() {
    bootstrapRequired = false;
    bootstrapScreenElement.classList.add("hidden");
    bootstrapErrorElement.textContent = "";
    bootstrapErrorElement.classList.add("hidden");
    selectedBootstrapKey = "";
    pendingBootstrapSessionID = "";
    pendingBootstrapRecovery = false;
    awaitingBootstrapPassword = false;
    if (bootstrapPasswordElement) {
        bootstrapPasswordElement.value = "";
        bootstrapPasswordElement.disabled = false;
    }
    if (bootstrapPasswordSubmitButton) {
        bootstrapPasswordSubmitButton.disabled = false;
        bootstrapPasswordSubmitButton.textContent = "Continue Bootstrap";
    }
    if (bootstrapPasswordMetaElement) {
        bootstrapPasswordMetaElement.textContent = "Choose a bootstrap node first.";
    }
    bootstrapSelectionElement.textContent = "No bootstrap node selected yet.";
    bootstrapSelectionElement.classList.add("is-placeholder");
}

function bootstrapNodeKey(node) {
    if (!node || typeof node !== "object") {
        return "";
    }

    return `${node.nodeId || ""}|${node.endpoint || ""}`;
}

function selectBootstrapNode(node) {
    selectedBootstrapKey = bootstrapNodeKey(node);

    if (bootstrapCustomHostElement) {
        bootstrapCustomHostElement.value = node.host || "";
    }
    if (bootstrapCustomPortElement) {
        bootstrapCustomPortElement.value = node.port ? String(node.port) : "";
    }

    bootstrapSelectionElement.textContent = `Selected ${node.name || "bootstrap node"} at ${node.endpoint || `${node.host}:${node.port}`}.`;
    bootstrapSelectionElement.classList.remove("is-placeholder");
    statusElement.textContent = "Bootstrap node selected";
}

function setBootstrapError(message) {
    if (!message) {
        bootstrapErrorElement.textContent = "";
        bootstrapErrorElement.classList.add("hidden");
        return;
    }

    bootstrapErrorElement.textContent = message;
    bootstrapErrorElement.classList.remove("hidden");
}

function applyBootstrapConnectPrompt(result, label) {
    pendingBootstrapSessionID = result.sessionId || "";
    pendingBootstrapRecovery = Boolean(result.recoveryAvailable);
    awaitingBootstrapPassword = Boolean(result.awaitingPassword);

    if (bootstrapPasswordMetaElement) {
        bootstrapPasswordMetaElement.textContent = pendingBootstrapRecovery
            ? `Recovered account ${result.accountId || "unknown"} is available from ${label}. Enter its password to unlock the local signing key.`
            : `No account exists for this NodeID on ${label}. Enter a password to create and encrypt a new account blob.`;
    }
    if (bootstrapPasswordElement) {
        bootstrapPasswordElement.value = "";
        bootstrapPasswordElement.disabled = false;
        bootstrapPasswordElement.focus();
    }
    if (bootstrapPasswordSubmitButton) {
        bootstrapPasswordSubmitButton.disabled = false;
        bootstrapPasswordSubmitButton.textContent = pendingBootstrapRecovery ? "Recover Account" : "Create Account";
    }

    bootstrapSelectionElement.textContent = result.message || `Password required to continue with ${label}.`;
    bootstrapSelectionElement.classList.remove("is-placeholder");
    bootstrapMetaElement.textContent = `Held bootstrap session open with ${label}.`;
    statusElement.textContent = pendingBootstrapRecovery ? "Account recovery available" : "Bootstrap password required";
}

async function connectBootstrapEndpoint(host, port, nodeID, label) {
    if (connectingBootstrap) {
        return;
    }

    connectingBootstrap = true;
    setBootstrapError("");
    statusElement.textContent = `Connecting to ${label}...`;
    bootstrapMetaElement.textContent = `Attempting bootstrap handshake with ${label}...`;
    bootstrapRefreshButton.disabled = true;
    if (bootstrapCustomConnectButton) {
        bootstrapCustomConnectButton.disabled = true;
    }

    try {
        const result = await appBridge.ConnectBootstrap(host, port, nodeID || "");
        applyBootstrapConnectPrompt(result, label);
    } catch (error) {
        setBootstrapError(String(error));
        statusElement.textContent = "Bootstrap connection failed";
        bootstrapMetaElement.textContent = `Unable to connect to ${label}.`;
        console.error(error);
    } finally {
        connectingBootstrap = false;
        bootstrapRefreshButton.disabled = false;
        if (bootstrapCustomConnectButton) {
            bootstrapCustomConnectButton.disabled = false;
        }
    }
}

async function completeBootstrapSession() {
    if (!pendingBootstrapSessionID) {
        setBootstrapError("Start a bootstrap session before entering a password.");
        return;
    }

    const password = bootstrapPasswordElement?.value ?? "";
    if (password.trim() === "") {
        setBootstrapError("A bootstrap password is required.");
        return;
    }
    if (connectingBootstrap) {
        return;
    }

    connectingBootstrap = true;
    setBootstrapError("");
    statusElement.textContent = pendingBootstrapRecovery ? "Recovering account..." : "Creating account...";
    bootstrapMetaElement.textContent = pendingBootstrapRecovery
        ? "Decrypting recovered account blob and finalizing bootstrap..."
        : "Generating account material and finalizing bootstrap...";
    bootstrapRefreshButton.disabled = true;
    bootstrapCustomConnectButton.disabled = true;
    bootstrapPasswordSubmitButton.disabled = true;
    bootstrapPasswordElement.disabled = true;

    try {
        const result = await appBridge.CompleteBootstrap(pendingBootstrapSessionID, password);
        pendingBootstrapSessionID = "";
        pendingBootstrapRecovery = false;
        awaitingBootstrapPassword = false;
        bootstrapPasswordElement.value = "";
        bootstrapPasswordMetaElement.textContent = "Choose a bootstrap node first.";
        bootstrapPasswordSubmitButton.textContent = "Continue Bootstrap";
        bootstrapSelectionElement.textContent = result.message || "Bootstrap completed.";
        bootstrapSelectionElement.classList.remove("is-placeholder");
        statusElement.textContent = result.reachable ? "Bootstrap completed" : "Bootstrap completed (degraded)";
        await loadShellState();
    } catch (error) {
        setBootstrapError(String(error));
        statusElement.textContent = "Bootstrap completion failed";
        bootstrapMetaElement.textContent = pendingBootstrapRecovery
            ? "Unable to recover the account. Check the password and try again."
            : "Unable to create the account. Check the password and try again.";
        console.error(error);
    } finally {
        connectingBootstrap = false;
        bootstrapRefreshButton.disabled = false;
        bootstrapCustomConnectButton.disabled = false;
        bootstrapPasswordSubmitButton.disabled = false;
        bootstrapPasswordElement.disabled = false;
    }
}

function renderBootstrapNodes(nodes) {
    bootstrapNodeListElement.replaceChildren();

    if (!Array.isArray(nodes) || nodes.length === 0) {
        const emptyElement = document.createElement("p");
        emptyElement.className = "bootstrap-empty";
        emptyElement.textContent = "No bootstrap nodes are available right now.";
        bootstrapNodeListElement.append(emptyElement);
        return;
    }

    for (const node of nodes) {
        const nodeElement = document.createElement("button");
        nodeElement.className = "bootstrap-node";
        nodeElement.type = "button";
        nodeElement.classList.toggle("is-selected", bootstrapNodeKey(node) === selectedBootstrapKey);
        nodeElement.setAttribute("aria-pressed", bootstrapNodeKey(node) === selectedBootstrapKey ? "true" : "false");
        nodeElement.addEventListener("click", async () => {
            selectBootstrapNode(node);
            renderBootstrapNodes(nodes);
            await connectBootstrapEndpoint(node.host, node.port, node.nodeId, node.name || node.endpoint || "bootstrap node");
        });

        const headerElement = document.createElement("div");
        headerElement.className = "bootstrap-node-header";

        const nameWrapElement = document.createElement("div");
        const nameElement = document.createElement("h4");
        nameElement.className = "bootstrap-node-name";
        nameElement.textContent = node.name || "Unnamed node";

        const endpointElement = document.createElement("p");
        endpointElement.className = "bootstrap-node-endpoint";
        endpointElement.textContent = node.endpoint || `${node.host || "unknown"}:${node.port || "?"}`;

        const latencyElement = document.createElement("span");
        latencyElement.className = "bootstrap-latency";
        latencyElement.textContent = formatBootstrapLatency(node);
        latencyElement.classList.toggle("is-unreachable", !node.reachable);

        nameWrapElement.append(nameElement, endpointElement);
        headerElement.append(nameWrapElement, latencyElement);

        const nodeIDElement = document.createElement("p");
        nodeIDElement.className = "bootstrap-node-id";
        nodeIDElement.textContent = node.nodeId || "NodeID unavailable";

        nodeElement.append(headerElement, nodeIDElement);
        bootstrapNodeListElement.append(nodeElement);
    }
}

function applyBootstrapState(bootstrapState) {
    if (!bootstrapState || typeof bootstrapState !== "object" || Array.isArray(bootstrapState)) {
        hideBootstrapScreen();
        return;
    }

    if (!bootstrapState.needsBootstrap) {
        hideBootstrapScreen();
        return;
    }

    showBootstrapScreen();
    renderBootstrapNodes(bootstrapState.nodes);

    if (Number(bootstrapState.peerCount || 0) > 0) {
        bootstrapMetaElement.textContent = `${bootstrapState.peerCount} peer file(s) already exist locally.`;
    } else if (Array.isArray(bootstrapState.nodes) && bootstrapState.nodes.length > 0) {
        bootstrapMetaElement.textContent = `${bootstrapState.nodes.length} bootstrap node(s), sorted from lowest to highest latency.`;
    } else {
        bootstrapMetaElement.textContent = "No bootstrap nodes could be ranked yet.";
    }

    if (bootstrapState.error) {
        bootstrapErrorElement.textContent = bootstrapState.error;
        bootstrapErrorElement.classList.remove("hidden");
        statusElement.textContent = "Bootstrap required";
    } else {
        bootstrapErrorElement.textContent = "";
        bootstrapErrorElement.classList.add("hidden");
        statusElement.textContent = "Bootstrap required";
    }

    if (bootstrapCustomPortElement && bootstrapCustomPortElement.value === "") {
        bootstrapCustomPortElement.value = "58103";
    }
    if (!awaitingBootstrapPassword && bootstrapPasswordMetaElement) {
        bootstrapPasswordMetaElement.textContent = "Choose a bootstrap node first.";
    }
}

async function loadBootstrapState() {
    try {
        const bootstrapState = await appBridge.BootstrapState();
        applyBootstrapState(bootstrapState);
        return bootstrapState;
    } catch (error) {
        showBootstrapScreen();
        renderBootstrapNodes([]);
        bootstrapMetaElement.textContent = "Bootstrap discovery failed.";
        bootstrapErrorElement.textContent = String(error);
        bootstrapErrorElement.classList.remove("hidden");
        statusElement.textContent = "Bootstrap required";
        console.error(error);
        return null;
    }
}

function applyUpdateStatus(updateStatus) {
    versionElement.textContent = `${formatVersion(updateStatus.currentVersion)} (remote ${formatVersion(updateStatus.remoteVersion)})`;
    syncUpdateModal(updateStatus);
}

function applySectionUpdate(section, payload) {
    if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
        return;
    }

    for (const [key, value] of Object.entries(payload)) {
        setMetric(`${section}.${key}`, value);
    }
}

function applySnapshot(snapshot) {
    if (!snapshot || typeof snapshot !== "object" || Array.isArray(snapshot)) {
        return;
    }

    applySectionUpdate("node", snapshot.node);
    applySectionUpdate("account", snapshot.account);
    applySectionUpdate("network", snapshot.network);
}

function normalizeEventPayload(eventData) {
    if (eventData.length === 1 && eventData[0] && typeof eventData[0] === "object" && !Array.isArray(eventData[0])) {
        return eventData[0];
    }

    return {};
}

function bindDashboardEvents() {
    const dashboardApi = {
        setMetric,
        applySectionUpdate,
        applySnapshot,
        resetMetric(field) {
            setMetric(field, "");
        },
    };

    globalThis.continuumDashboard = dashboardApi;

    if (!runtimeBridge || typeof runtimeBridge.EventsOnMultiple !== "function") {
        return;
    }

    dashboardEventUnsubscribers = dashboardEventBindings.map(([eventName, section]) =>
        runtimeBridge.EventsOnMultiple(eventName, (...eventData) => {
            applySectionUpdate(section, normalizeEventPayload(eventData));
        }, -1),
    );

    dashboardEventUnsubscribers.push(
        runtimeBridge.EventsOnMultiple("dashboard:snapshot", (...eventData) => {
            applySnapshot(normalizeEventPayload(eventData));
        }, -1),
    );

    dashboardEventUnsubscribers.push(
        runtimeBridge.EventsOnMultiple("updater:status", (...eventData) => {
            const updateStatus = normalizeEventPayload(eventData);
            if (!("currentVersion" in updateStatus) || !("remoteVersion" in updateStatus)) {
                return;
            }

            applyUpdateStatus(updateStatus);
            if (updateStatus.updateRequired) {
                statusElement.textContent = updateStatus.updateError ? "Update failed" : "Update available";
                return;
            }

            statusElement.textContent = bootstrapRequired ? "Bootstrap required" : "Dashboard ready";
        }, -1),
    );
}

function releaseDashboardEvents() {
    for (const unsubscribe of dashboardEventUnsubscribers) {
        if (typeof unsubscribe === "function") {
            unsubscribe();
        }
    }

    dashboardEventUnsubscribers = [];
}

async function loadShellState() {
    statusElement.textContent = "Resolving dashboard...";

    try {
        const [nodeID, updateStatus] = await Promise.all([
            appBridge.NodeID(),
            appBridge.UpdateStatus(),
        ]);

        setMetric("node.nodeId", nodeID);
        await loadDiskUsage();
        await loadBandwidthUsage();
        applyUpdateStatus(updateStatus);
        const bootstrapState = await loadBootstrapState();
        if (!bootstrapState?.needsBootstrap) {
            statusElement.textContent = "Dashboard ready";
        }
    } catch (error) {
        setMetric("node.nodeId", "Unable to load NodeID");
        fieldElements.get("node.nodeId")?.classList.remove("is-placeholder");
        versionElement.textContent = "unknown (remote unavailable)";
        statusElement.textContent = "Failed to resolve dashboard";
        hideBootstrapScreen();
        hideUpdateModal();
        console.error(error);
    }
}

async function loadDiskUsage() {
    try {
        const snapshot = await appBridge.DiskUsage();
        setMetric("node.diskUsage", formatDiskUsage(snapshot));
    } catch (error) {
        setMetric("node.diskUsage", "Unavailable");
        fieldElements.get("node.diskUsage")?.classList.remove("is-placeholder");
        console.error(error);
    }
}

async function loadBandwidthUsage() {
    try {
        const snapshot = await appBridge.NetworkUsage();
        setMetric("node.bandwidth", formatBandwidthUsage(snapshot));
    } catch (error) {
        setMetric("node.bandwidth", "Unavailable");
        fieldElements.get("node.bandwidth")?.classList.remove("is-placeholder");
        console.error(error);
    }
}

function syncUpdateModal(updateStatus) {
    if (!updateStatus.updateRequired) {
        hideUpdateModal();
        return;
    }

    const currentVersion = formatVersion(updateStatus.currentVersion);
    const remoteVersion = formatVersion(updateStatus.remoteVersion);
    const updateError = updateStatus.updateError ?? "";
    const statusChanged =
        pendingUpdateStatus === null ||
        pendingUpdateStatus.currentVersion !== updateStatus.currentVersion ||
        pendingUpdateStatus.remoteVersion !== updateStatus.remoteVersion ||
        pendingUpdateStatus.updateError !== updateStatus.updateError;

    pendingUpdateStatus = updateStatus;
    if (statusChanged || updateCountdownTimer === null) {
        countdownRemaining = updateCountdownSeconds;
    }

    showUpdateModal();
    updateNowButton.disabled = false;
    exitAppButton.disabled = false;
    renderUpdateMessage(currentVersion, remoteVersion, updateError);

    if (updateError) {
        clearUpdateCountdown();
        statusElement.textContent = "Update failed";
        return;
    }

    startUpdateCountdown();
}

function renderUpdateMessage(currentVersion, remoteVersion, updateError = "") {
    if (updateError) {
        updateMessageElement.textContent = `Current ${currentVersion}. Remote ${remoteVersion}. Automatic retry is paused until you choose Update Now or Exit.`;
        updateErrorElement.textContent = `Last update attempt failed:\n${updateError}`;
        updateErrorElement.classList.remove("hidden");
        return;
    }

    updateMessageElement.textContent = `Current ${currentVersion}. Remote ${remoteVersion}. Continuum will auto update in ${countdownRemaining} seconds.`;
    updateErrorElement.textContent = "";
    updateErrorElement.classList.add("hidden");
}

function startUpdateCountdown() {
    if (updateCountdownTimer !== null) {
        return;
    }

    updateCountdownTimer = globalThis.setInterval(async () => {
        countdownRemaining -= 1;

        if (countdownRemaining <= 0) {
            clearUpdateCountdown();
            await triggerUpdateNow();
            return;
        }

        renderUpdateMessage(
            formatVersion(pendingUpdateStatus?.currentVersion),
            formatVersion(pendingUpdateStatus?.remoteVersion),
            pendingUpdateStatus?.updateError,
        );
    }, 1000);
}

function clearUpdateCountdown() {
    if (updateCountdownTimer === null) {
        return;
    }

    globalThis.clearInterval(updateCountdownTimer);
    updateCountdownTimer = null;
}

function showUpdateModal() {
    if (updateDialogElement.open) {
        return;
    }

    if (typeof updateDialogElement.showModal === "function") {
        updateDialogElement.showModal();
        return;
    }

    updateDialogElement.setAttribute("open", "");
}

function hideUpdateModal() {
    clearUpdateCountdown();
    pendingUpdateStatus = null;
    updateErrorElement.textContent = "";
    updateErrorElement.classList.add("hidden");

    if (!updateDialogElement.open) {
        updateDialogElement.removeAttribute("open");
        return;
    }

    if (typeof updateDialogElement.close === "function") {
        updateDialogElement.close();
        return;
    }

    updateDialogElement.removeAttribute("open");
}

async function triggerUpdateNow() {
    clearUpdateCountdown();
    updateNowButton.disabled = true;
    exitAppButton.disabled = true;
    updateMessageElement.textContent = "Applying update now...";
    updateErrorElement.textContent = "";
    updateErrorElement.classList.add("hidden");
    statusElement.textContent = "Applying update...";

    try {
        await appBridge.UpdateNow();
        statusElement.textContent = "Refreshing update status...";
        await loadShellState();
        if (pendingUpdateStatus === null) {
            statusElement.textContent = "Already up to date";
            return;
        }

        statusElement.textContent = "Update still required";
    } catch (error) {
        try {
            const updateStatus = await appBridge.UpdateStatus();
            pendingUpdateStatus = updateStatus;
            updateNowButton.disabled = false;
            exitAppButton.disabled = false;
            renderUpdateMessage(
                formatVersion(updateStatus.currentVersion),
                formatVersion(updateStatus.remoteVersion),
                updateStatus.updateError || String(error),
            );
            statusElement.textContent = "Update failed";
        } catch (statusError) {
            updateMessageElement.textContent = "Update failed. You can try again or exit.";
            updateErrorElement.textContent = String(error);
            updateErrorElement.classList.remove("hidden");
            updateNowButton.disabled = false;
            exitAppButton.disabled = false;
            statusElement.textContent = "Update failed";
            console.error(statusError);
        }
        console.error(error);
    }
}

async function exitApplication() {
    clearUpdateCountdown();
    await appBridge.Exit();
}

function startDiskUsageChecks() {
    if (diskUsageTimer !== null) {
        return;
    }

    diskUsageTimer = globalThis.setInterval(() => {
        void loadDiskUsage();
        void loadBandwidthUsage();
    }, diskUsageRefreshIntervalMs);
}

function startBootstrapChecks() {
    if (bootstrapRefreshTimer !== null) {
        return;
    }

    bootstrapRefreshTimer = globalThis.setInterval(() => {
        if (!bootstrapRequired) {
            return;
        }

        void loadBootstrapState();
    }, bootstrapRefreshIntervalMs);
}

updateNowButton.addEventListener("click", () => {
    void triggerUpdateNow();
});
exitAppButton.addEventListener("click", () => {
    void exitApplication();
});
bootstrapRefreshButton.addEventListener("click", () => {
    bootstrapMetaElement.textContent = "Refreshing bootstrap latency...";
    void loadBootstrapState();
});
bootstrapCustomConnectButton.addEventListener("click", () => {
    const host = bootstrapCustomHostElement.value.trim();
    const port = Number.parseInt(bootstrapCustomPortElement.value.trim(), 10);
    void connectBootstrapEndpoint(host, port, "", host || "custom bootstrap");
});

bootstrapPasswordSubmitButton.addEventListener("click", () => {
    void completeBootstrapSession();
});

bootstrapPasswordElement.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") {
        return;
    }

    event.preventDefault();
    void completeBootstrapSession();
});
updateDialogElement.addEventListener("cancel", (event) => {
    event.preventDefault();
});
globalThis.addEventListener("beforeunload", releaseDashboardEvents);

bindDashboardEvents();
await loadShellState();
startDiskUsageChecks();
startBootstrapChecks();
