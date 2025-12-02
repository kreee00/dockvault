# 💾 DockVault — Multi-Volume Docker Backup to Google Workspace Shared Drives

> **Automated multi-volume Docker backup system for Google Workspace Shared Drives using rclone and Service Accounts. Reliable, cron-ready, and tokenless.**

![License](https://img.shields.io/github/license/kreee00/dockvault?style=flat-square)
![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20WSL%20%7C%20Docker-blue?style=flat-square)
![Status](https://img.shields.io/badge/status-Active-green?style=flat-square)
![Built With](https://img.shields.io/badge/built_with-rclone%20%7C%20bash%20%7C%20docker-yellow?style=flat-square)

---

## 🧠 Project Overview

**DockVault** is a reliable, self-contained backup system for Docker volumes running under **Windows + WSL**, designed to upload directly to a **Google Workspace Shared Drive**.

Unlike typical `rclone` scripts that fail every 7 days due to expiring OAuth tokens or personal quota limits, DockVault uses a **Google Service Account** to achieve:

✅ Non-expiring authentication  
✅ Organizational storage quota usage (Shared Drive)  
✅ Seamless automation under Docker + WSL  

> Originally inspired by [Abhay Kulkarni’s single-volume n8n backup script](https://hiabhaykulkarni.gumroad.com/l/n8n-backup-restore), DockVault extends functionality to **multi-volume environments** with a roadmap toward a **CLI-based configuration system**.

---

## 🏗️ Architecture Overview

### Environment
| Component | Description |
|------------|-------------|
| **Host OS** | Windows 11 Pro |
| **Virtualization** | Docker Desktop |
| **Linux Subsystem** | Ubuntu via WSL |
| **Backup Tool** | `rclone` (inside WSL) |
| **Destination** | Google Workspace Shared Drive |
| **Authentication** | Google Service Account JSON Key |

### Process Flow
```mermaid
graph TD
    subgraph "Host Machine (Windows 11 / Docker Desktop)"
        A[Docker Volume 1: n8n_data]
        B[Docker Volume 2: postgres_data]
    end

    subgraph "WSL (Ubuntu)"
        C(Cron Scheduler) -->|1 AM Trigger| D(Run Docker Command)
        F(backup.sh) --> D
    end
    
    subgraph "Ephemeral Docker Container"
        D -->|Mount| G[/n8n_data, /postgres_data/]
        D -->|Exec| H(backup.sh)
    end

    subgraph "Google Cloud"
        H -->|rclone copy| I(Drive API via Service Account)
        I --> J(Google Workspace Shared Drive)
    end
````

---

## ⚙️ Core Features

* 🔒 **Tokenless Authentication** — Service Account JSON key (never expires)
* 🗂️ **Multi-Volume Backup** — Back up any number of Docker volumes in one run
* 🪣 **Shared Drive Integration** — Uses Workspace quota, not personal quota
* 🧾 **Automatic Cleanup** — Keeps the latest two local backups per service
* ⏰ **Cron-Ready** — Works seamlessly with WSL’s cron for scheduling

---

## 🚀 Quick Start

### 1️⃣ Configure Google Cloud (Service Account)

Follow the [detailed setup guide](#31-google-cloud--drive-setup-service-account) to:

* Create a Service Account
* Enable Google Drive API
* Download and place your JSON key inside `~/.config/rclone/`

### 2️⃣ Setup rclone

```bash
rclone config
# Name your remote (e.g. gdrive_svc_shared)
# Choose "drive"
# Skip client_id and client_secret
# Enter path to your JSON key
# Skip team_drive to allow auto-detect
```

### 3️⃣ Prepare Local Folders

```bash
sudo mkdir -p /opt/dockvault/{n8n,postgres}
sudo chmod -R 755 /opt/dockvault
```

### 4️⃣ Create Backup Script

File: `/opt/dockvault/backup.sh`

```bash
sudo nano /opt/dockvault/backup.sh
# Paste the full script from this repository
sudo chmod +x /opt/dockvault/backup.sh
```

---

## 🧰 Execution

### Manual Run

```bash
docker run --rm \
  -v server-monitoring_grafana_data:/server-monitoring-grafana-1 \
  -v server-monitoring_prometheus_data:/server-monitoring-prometheus-1 \
  -v traefik-certs:/traefik \
  -v n8n_n8n_data:/n8n-n8n-1 \
  -v n8n_postgres_data:/n8n-n8n_postgres-1 \
  -v /opt/dockvault/backup.sh:/backup/backup.sh \
  -v /opt/dockvault:/backup \
  -v /home/ersdb/.config/rclone:/root/.config/rclone \
  -v /home/ersdb/.config/rclone:/home/ersdb/.config/rclone \
  alpine:latest /bin/sh -c "apk add --no-cache tar rclone && /bin/sh /backup/backup.sh"
```

### Scheduled Run

Edit your WSL cron:

```bash
crontab -e
```

Add:

```bash
0 1 * * * <same docker run command as above>
```

Runs daily at **1 AM**.

---

## 🧩 Restoration Guide

Example: Restore a Postgres volume

```bash
# Stop service
docker compose stop db

# Clean old data
docker run --rm -v db-backup_db_data:/data alpine sh -c "rm -rf /data/*"

# Restore
docker run --rm \
  -v db-backup_db_data:/var/lib/postgresql/data \
  -v /home/<USER>/.config/rclone:/root/.config/rclone \
  -v /home/<USER>/.config/rclone:/home/ubuntu/.config/rclone \
  postgres:14 sh -c "apt-get update && apt-get install -y tar rclone ca-certificates && \
  rclone copy 'gdrive_svc_shared:.../postgres' /tmp/restore --include '<BACKUP_FILENAME>' && \
  tar xzf /tmp/restore/<BACKUP_FILENAME> -C /var/lib/postgresql/data && \
  chown -R postgres:postgres /var/lib/postgresql/data"
```

---

## ⚠️ Common Pitfalls

| Error                               | Cause                                    | Fix                                            |
| ----------------------------------- | ---------------------------------------- | ---------------------------------------------- |
| `storageQuotaExceeded`              | Using “My Drive” instead of Shared Drive | Use Shared Drive destination                   |
| `no such file or directory`         | rclone can’t locate JSON key             | Double-mount config dir                        |
| `permission denied`                 | Key file permissions too strict          | `chmod 644 ~/.config/rclone/key.json`          |
| `tls: failed to verify certificate` | Missing CA certs                         | Install `ca-certificates`                      |
| `Container fails after restore`     | Wrong ownership                          | Run `chown -R postgres:postgres` after restore |

---

## 🧭 Roadmap

| Phase | Goal              | Description                                                 |
| ----- | ----------------- | ----------------------------------------------------------- |
| **1** | 🧮 CLI Wrapper    | Build CLI (Python / Go) to manage volumes & config via YAML |
| **2** | 🤖 Auto-Discovery | Detect running Docker volumes dynamically                   |
| **3** | 🖥️ GUI App       | Electron/Tauri desktop app for managing backups & restores  |

---

## 👏 Credits

* **Original Single-Volume Concept:** [Abhay Kulkarni](https://github.com/abhayxyz)
* **Multi-Volume Expansion & CLI Concept:** [Your Name](https://github.com/yourusername)
* **Built with:** `Docker`, `rclone`, `Bash`, `Google Cloud Service Accounts`

---

## 🪪 License

This project is released under the **MIT License**.
See [`LICENSE`](LICENSE) for details.

---

### ⭐ If DockVault saved your data — consider giving it a star!
