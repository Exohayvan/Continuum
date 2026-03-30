const nodeIDElement = document.getElementById("nodeid");
const refreshButton = document.getElementById("refresh");
const statusElement = document.getElementById("status");

async function loadNodeID() {
    statusElement.textContent = "Resolving NodeID...";
    refreshButton.disabled = true;

    try {
        const nodeID = await window.go.desktop.App.NodeID();
        nodeIDElement.textContent = nodeID;
        statusElement.textContent = "NodeID loaded";
    } catch (error) {
        nodeIDElement.textContent = "Unable to load NodeID";
        statusElement.textContent = "Failed to resolve NodeID";
        console.error(error);
    } finally {
        refreshButton.disabled = false;
    }
}

refreshButton.addEventListener("click", loadNodeID);
loadNodeID();
