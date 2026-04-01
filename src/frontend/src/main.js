const versionElement = document.getElementById("version");
const statusElement = document.getElementById("status");
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
const updateCheckIntervalMs = 30 * 60 * 1000;
const diskUsageRefreshIntervalMs = 2000;
const defaultPlaceholder = "Pending";
const dashboardEventBindings = [
    ["dashboard:this-node", "node"],
    ["dashboard:your-account", "account"],
    ["dashboard:network", "network"],
];

let updateCountdownTimer = null;
let updateCheckTimer = null;
let diskUsageTimer = null;
let countdownRemaining = updateCountdownSeconds;
let pendingUpdateStatus = null;
let dashboardEventUnsubscribers = [];

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

    return `${formatBytes(snapshot.dataBytes)} / ${formatBytes(snapshot.volumeBytes)} (${formatPercent(snapshot.usagePercent)}) • R ${Number(snapshot.readMbps || 0).toFixed(2)} Mb/s • W ${Number(snapshot.writeMbps || 0).toFixed(2)} Mb/s`;
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
        versionElement.textContent = `${formatVersion(updateStatus.currentVersion)} (remote ${formatVersion(updateStatus.remoteVersion)})`;
        syncUpdateModal(updateStatus);
        statusElement.textContent = "Dashboard ready";
    } catch (error) {
        setMetric("node.nodeId", "Unable to load NodeID");
        fieldElements.get("node.nodeId")?.classList.remove("is-placeholder");
        versionElement.textContent = "unknown (remote unavailable)";
        statusElement.textContent = "Failed to resolve dashboard";
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

function startUpdateChecks() {
    if (updateCheckTimer !== null) {
        return;
    }

    updateCheckTimer = globalThis.setInterval(() => {
        void loadShellState();
    }, updateCheckIntervalMs);
}

function startDiskUsageChecks() {
    if (diskUsageTimer !== null) {
        return;
    }

    diskUsageTimer = globalThis.setInterval(() => {
        void loadDiskUsage();
    }, diskUsageRefreshIntervalMs);
}

updateNowButton.addEventListener("click", () => {
    void triggerUpdateNow();
});
exitAppButton.addEventListener("click", () => {
    void exitApplication();
});
updateDialogElement.addEventListener("cancel", (event) => {
    event.preventDefault();
});
globalThis.addEventListener("beforeunload", releaseDashboardEvents);

bindDashboardEvents();
await loadShellState();
startUpdateChecks();
startDiskUsageChecks();
