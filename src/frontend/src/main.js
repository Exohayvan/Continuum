const nodeIDElement = document.getElementById("nodeid");
const versionElement = document.getElementById("version");
const refreshButton = document.getElementById("refresh");
const statusElement = document.getElementById("status");
const appBridge = globalThis.go.desktop.App;

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
        const [nodeID, appVersion, remoteVersion] = await Promise.all([
            appBridge.NodeID(),
            appBridge.Version(),
            appBridge.RemoteVersion(),
        ]);

        nodeIDElement.textContent = nodeID;
        versionElement.textContent = `${formatVersion(appVersion)} (remote ${formatVersion(remoteVersion)})`;
        statusElement.textContent = "NodeID loaded";
    } catch (error) {
        nodeIDElement.textContent = "Unable to load NodeID";
        versionElement.textContent = "unknown (remote unavailable)";
        statusElement.textContent = "Failed to resolve NodeID";
        console.error(error);
    } finally {
        refreshButton.disabled = false;
    }
}

refreshButton.addEventListener("click", loadShellState);
await loadShellState();
