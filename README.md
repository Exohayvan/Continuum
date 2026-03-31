# Continuum

> A modular, distributed infrastructure platform for managing servers, backups, and data through a private mesh network.

---

> [!WARNING]
> 🚧 **Status: Concept / Design Phase**
>
> Continuum is currently on the drawing board. Core architecture, networking models, and system design are actively being explored and refined.
>
> Nothing here is production-ready — expect major changes as the system evolves.

---

![Sonar Coverage](https://img.shields.io/sonar/coverage/Exohayvan_Continuum?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarqubecloud)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/ExoHayvan/Continuum/total?style=for-the-badge&logo=github)
![GitHub Release](https://img.shields.io/github/v/release/ExoHayvan/Continuum?include_prereleases&style=for-the-badge&logo=github)

## 🚀 Overview

**Continuum** is a distributed platform designed to:

* Manage and orchestrate server environments
* Perform automated, efficient backups
* Distribute data across a **trusted mesh network**
* Enable **fast, parallel restores** from multiple nodes
* Eliminate reliance on a single storage location

Continuum is not a single application — it is a **modular system** composed of multiple interoperating components.

---

## 🧠 Core Concept

Instead of treating backups as static files stored in one place, Continuum treats them as:

> **distributed, verifiable data sets that exist across a network of nodes**

Backups are:

* split into chunks
* hashed and tracked
* distributed across nodes
* reconstructed from multiple sources when needed

---

## 🧩 Modules

Continuum is composed of multiple modules, each responsible for a specific role.

### 🧠 Continuum (Core)

* Base runtime and shared logic
* Networking, identity, configuration
* Common libraries used across all modules

---

### 🖥️ Continuum Host

* Server orchestration layer
* Manages game servers (initial focus: Minecraft)
* Handles:

  * start / stop
  * file management
  * plugin management
  * scheduling

---

### 🌐 Continuum Mesh

* Distributed data layer
* Handles:

  * chunk distribution
  * peer-to-peer transfers
  * node discovery
  * replication tracking

---

### 🖥️ Continuum Node

* Runs on each participating machine
* Stores data and participates in the mesh
* Can act as:

  * full storage node
  * partial storage node
  * relay/cache node

---

### 🎛️ Continuum Panel (Planned)

* Web-based UI
* Displays:

  * server status
  * node health
  * backup coverage
  * chunk distribution

---

## 🌐 Mesh Network Overview

### Bootstrap Model

1. Node installs Continuum Node
2. Node connects to a **bootstrap authority**
3. Node receives:

   * identity registration
   * role assignment
   * peer list

After bootstrap:

* nodes communicate directly
* mesh continues operating independently

---

## 📦 Chunk-Based System

Each backup is:

1. Compressed
2. Split into chunks (configurable size)
3. Hashed (per chunk)
4. Stored with a manifest

---

### Chunk Map Concept

Each node maintains a **bitfield** representing chunk ownership:

```text
Chunk Index:  0 1 2 3 4 5
Node A:       1 1 1 1 1 1
Node B:       1 0 0 1 0 1
Node C:       0 1 0 0 1 0
```

This enables:

* fast comparison between nodes
* efficient chunk transfer decisions
* replication tracking

---

### Distribution Strategy

When a node joins:

1. Determines available storage
2. Fetches chunk map
3. Selects chunks with **lowest replication count**
4. Distributes selections across entire dataset
5. Downloads + verifies chunks
6. Immediately begins seeding

---

### Node Storage Flexibility

Nodes can define limits such as:

* max storage size
* max chunks per backup
* retention rules

This allows:

* full archive nodes
* partial storage nodes
* temporary cache nodes

---

## ⚡ Restore Model

Restores are performed using **parallel chunk retrieval**:

1. Request backup
2. Identify nodes holding required chunks
3. Download chunks from multiple peers
4. Verify each chunk
5. Reconstruct archive
6. Restore system

---

## ⚙️ Roadmap

### Phase 1 — Core Foundation

* [ ] Base Continuum runtime
* [ ] Node identity + bootstrap system
* [ ] Basic networking layer

---

### Phase 2 — Host Management

* [ ] Server lifecycle management
* [ ] File + plugin management
* [ ] Scheduling system

---

### Phase 3 — Backup System

* [ ] Compression pipeline
* [ ] Chunking system
* [ ] Manifest format
* [ ] Local backup handling

---

### Phase 4 — Mesh v1

* [ ] Node registration
* [ ] Peer discovery
* [ ] Chunk transfer (host → node)
* [ ] Bitfield tracking

---

### Phase 5 — Mesh v2 (P2P)

* [ ] Node-to-node chunk transfers
* [ ] Bitfield exchange
* [ ] Parallel downloads
* [ ] Partial node support

---

### Phase 6 — Intelligent Distribution

* [ ] Replication tracking
* [ ] Least-replicated chunk assignment
* [ ] Storage-aware scheduling
* [ ] Rebalancing logic

---

### Phase 7 — Restore System

* [ ] Multi-node restore
* [ ] Parallel chunk retrieval
* [ ] Integrity verification
* [ ] Recovery workflows

---

### Phase 8 — Security + Scaling

* [ ] Encryption at rest
* [ ] Role-based permissions
* [ ] Multi-authority nodes
* [ ] Secure mesh communication

---

### Phase 9 — Advanced Systems

* [ ] Erasure coding
* [ ] Temporary cache nodes
* [ ] Bandwidth-aware scheduling
* [ ] Node reliability scoring

---

## 📌 Design Goals

* High availability (no single point of failure)
* Fast restore times through parallelism
* Efficient use of distributed storage
* Modular architecture for expansion
* Controlled, secure mesh network

---

## 💡 Inspiration

* BitTorrent (chunk distribution model)
* Distributed storage systems
* Cluster orchestration platforms

---

## 🔮 Vision

> A distributed infrastructure platform where systems are not backed up to a location — they are **continuously distributed, verified, and recoverable from anywhere in the network**.

---
