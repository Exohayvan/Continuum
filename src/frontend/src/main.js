const nodeIDElement = document.getElementById("nodeid");
const versionElement = document.getElementById("version");
const refreshButton = document.getElementById("refresh");
const statusElement = document.getElementById("status");
const updateOverlayElement = document.getElementById("update-overlay");
const updateMessageElement = document.getElementById("update-message");
const updateNowButton = document.getElementById("update-now");
const exitAppButton = document.getElementById("exit-app");
const appBridge = globalThis.go.desktop.App;
const updateCountdownSeconds = 15;
const updateCheckIntervalMs = 30 * 60 * 1000;

let updateCountdownTimer = null;
let updateCheckTimer = null;
let countdownRemaining = updateCountdownSeconds;
let pendingUpdateStatus = null;

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

async function loadShellState() {
    statusElement.textContent = "Resolving NodeID...";
    refreshButton.disabled = true;

    try {
        const [nodeID, updateStatus] = await Promise.all([
            appBridge.NodeID(),
            appBridge.UpdateStatus(),
        ]);

        nodeIDElement.textContent = nodeID;
        versionElement.textContent = `${formatVersion(updateStatus.currentVersion)} (remote ${formatVersion(updateStatus.remoteVersion)})`;
        syncUpdateModal(updateStatus);
        statusElement.textContent = "NodeID loaded";
    } catch (error) {
        nodeIDElement.textContent = "Unable to load NodeID";
        versionElement.textContent = "unknown (remote unavailable)";
        statusElement.textContent = "Failed to resolve NodeID";
        hideUpdateModal();
        console.error(error);
    } finally {
        refreshButton.disabled = false;
    }
}

function syncUpdateModal(updateStatus) {
    if (!updateStatus.updateRequired) {
        hideUpdateModal();
        return;
    }

    const currentVersion = formatVersion(updateStatus.currentVersion);
    const remoteVersion = formatVersion(updateStatus.remoteVersion);
    const statusChanged =
        pendingUpdateStatus === null ||
        pendingUpdateStatus.currentVersion !== updateStatus.currentVersion ||
        pendingUpdateStatus.remoteVersion !== updateStatus.remoteVersion;

    pendingUpdateStatus = updateStatus;
    if (statusChanged || updateCountdownTimer === null) {
        countdownRemaining = updateCountdownSeconds;
    }

    updateOverlayElement.classList.remove("hidden");
    updateNowButton.disabled = false;
    exitAppButton.disabled = false;
    renderUpdateMessage(currentVersion, remoteVersion);
    startUpdateCountdown();
}

function renderUpdateMessage(currentVersion, remoteVersion) {
    updateMessageElement.textContent = `Current ${currentVersion}. Remote ${remoteVersion}. Continuum will auto update in ${countdownRemaining} seconds.`;
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

function hideUpdateModal() {
    clearUpdateCountdown();
    pendingUpdateStatus = null;
    updateOverlayElement.classList.add("hidden");
}

async function triggerUpdateNow() {
    clearUpdateCountdown();
    updateNowButton.disabled = true;
    exitAppButton.disabled = true;
    updateMessageElement.textContent = "Applying update now...";
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
        updateMessageElement.textContent = "Update failed. You can try again or exit.";
        updateNowButton.disabled = false;
        exitAppButton.disabled = false;
        statusElement.textContent = "Update failed";
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

refreshButton.addEventListener("click", loadShellState);
updateNowButton.addEventListener("click", () => {
    void triggerUpdateNow();
});
exitAppButton.addEventListener("click", () => {
    void exitApplication();
});
await loadShellState();
startUpdateChecks();
