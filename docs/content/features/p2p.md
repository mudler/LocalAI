+++
disableToc = false
title = "P2P API"
weight = 22
url = "/features/p2p/"
+++

LocalAI supports peer-to-peer (P2P) networking for distributed inference. The P2P API endpoints allow you to monitor connected worker and federated nodes, retrieve the P2P network token, and get cluster statistics.

For an overview of distributed inference setup, see [Distributed Inference](/features/distributed_inferencing/).

## Endpoints

### List all P2P nodes

- **Method:** `GET`
- **Endpoint:** `/api/p2p`

Returns all worker and federated nodes in the P2P network.

#### Response

| Field              | Type    | Description                          |
|--------------------|---------|--------------------------------------|
| `nodes`            | `array` | List of worker nodes                 |
| `federated_nodes`  | `array` | List of federated nodes              |

Each node object:

| Field            | Type     | Description                              |
|------------------|----------|------------------------------------------|
| `Name`           | `string` | Node name                                |
| `ID`             | `string` | Unique node identifier                   |
| `TunnelAddress`  | `string` | Network tunnel address                   |
| `ServiceID`      | `string` | Service identifier                       |
| `LastSeen`       | `string` | ISO 8601 timestamp of last heartbeat     |

#### Usage

```bash
curl http://localhost:8080/api/p2p
```

#### Example response

```json
{
  "nodes": [
    {
      "Name": "worker-1",
      "ID": "abc123",
      "TunnelAddress": "192.168.1.10:9090",
      "ServiceID": "worker",
      "LastSeen": "2025-01-15T10:30:00Z"
    }
  ],
  "federated_nodes": [
    {
      "Name": "federation-1",
      "ID": "def456",
      "TunnelAddress": "192.168.1.20:9090",
      "ServiceID": "federated",
      "LastSeen": "2025-01-15T10:30:05Z"
    }
  ]
}
```

---

### Get P2P token

- **Method:** `GET`
- **Endpoint:** `/api/p2p/token`

Returns the P2P network token used for node authentication.

#### Usage

```bash
curl http://localhost:8080/api/p2p/token
```

#### Response

Returns the token as a plain text string.

---

### List worker nodes

- **Method:** `GET`
- **Endpoint:** `/api/p2p/workers`

Returns worker nodes with online status.

#### Response

| Field                    | Type     | Description                          |
|--------------------------|----------|--------------------------------------|
| `nodes`                  | `array`  | List of worker nodes                 |
| `nodes[].name`           | `string` | Node name                            |
| `nodes[].id`             | `string` | Unique node identifier               |
| `nodes[].tunnelAddress`  | `string` | Network tunnel address               |
| `nodes[].serviceID`      | `string` | Service identifier                   |
| `nodes[].lastSeen`       | `string` | Last heartbeat timestamp             |
| `nodes[].isOnline`       | `bool`   | Whether the node is currently online |

A node is considered online if it was last seen within the past 40 seconds.

#### Usage

```bash
curl http://localhost:8080/api/p2p/workers
```

---

### List federated nodes

- **Method:** `GET`
- **Endpoint:** `/api/p2p/federation`

Returns federated nodes with online status. Same response format as `/api/p2p/workers`.

#### Usage

```bash
curl http://localhost:8080/api/p2p/federation
```

---

### Get P2P statistics

- **Method:** `GET`
- **Endpoint:** `/api/p2p/stats`

Returns aggregate statistics about the P2P cluster.

#### Response

| Field              | Type     | Description                       |
|--------------------|----------|-----------------------------------|
| `workers.online`   | `int`    | Number of online worker nodes     |
| `workers.total`    | `int`    | Total worker nodes                |
| `federated.online` | `int`    | Number of online federated nodes  |
| `federated.total`  | `int`    | Total federated nodes             |

#### Usage

```bash
curl http://localhost:8080/api/p2p/stats
```

#### Example response

```json
{
  "workers": {
    "online": 3,
    "total": 5
  },
  "federated": {
    "online": 2,
    "total": 2
  }
}
```

## Error Responses

| Status Code | Description                                 |
|-------------|---------------------------------------------|
| 500         | P2P subsystem not available or internal error |
