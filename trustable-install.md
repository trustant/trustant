# ![](./web/trustable-logo.png){ height=50px } Trustable - Installation Instructions

> **Note:** The release is currently in alpha version and may contain errors.

## Support

- You can report issues and read the FAQ

```
https://github.com/trustable-ai
```

- You can join the support group on WhatsApp

 ![](./web/wa-trustable.jpeg){ height=50px }

```
https://n7s.co/wa-trustable
```

## Security

Trustable is a vibe coding system that generates applications, therefore it requires the ability to execute code entrusted to the AI.

To reduce risks, it operates only within Docker containers and writes only to specific folders in the home directory:

- `.ops`
- `.ops-workspace`
- `.local/bin`

Trustable requires the download of binary executables for installation and execution. All binaries are downloaded from known and documented sources like github.

> **IMPORTANT** The AI operates only within a Docker container and **never** has full access to the user's local filesystem except for the documented folders. Commands are executed inside a Docker container. However, it is recommended to install Trustable on a machine **THAT DOES NOT CONTAIN SENSITIVE DATA** and for which you have a **complete backup**.

The system is not authenticated and follows the Nuvolaris cluster apihost. For
local installs this can use whatever wildcard host the cluster exposes; for
custom domains it should use the same protocol and base domain as the cluster,
such as `http://*.<node-ip>.nip.io` or `https://*.<base-domain>`.

If you install on a virtual machine, you need to set up a tunnel to access it:

```
ssh -L 80:127.0.0.1:80 <user>@<server>
```

## Prerequisites

### Memory, Disk and CPU

Trustable requires a system with at least **16 GB of RAM**. It is **very unlikely** to work acceptably with less memory.

It will use around 60 GB of disk space. If you use it in a virtual machine, you need at least 4 virtual CPUs.

### Supported Operating Systems

Trustable has been tested on:

- Windows 11
- macOS Sequoia
- Ubuntu 24.04

Different or older systems may present compatibility issues. The installer will set up a firewall rule for local access to `127.0.0.1`.

### Docker Desktop

Trustable requires and uses Docker. You need to download and install **Docker Desktop** before starting and have it running.

> **Warning:** Docker Desktop requires a login to download images. Log in before proceeding.

### Ollama Cloud

For AI models, Trustable uses **Ollama Cloud**. A free version is also available, which provides free (but limited) credits to try. You need to register a free account.

If you want to work more intensively, you need:

- A **local Ollama with GPU** (such as our BestIA), or
- A **Pro account** on Ollama Cloud

## Installation

### 1. Download Trustable

**Windows**:

Open PowerShell (press Windows+R and type 'powershell') and run:

```powershell
irm n7s.co/get-trustable | iex
```

If there are issues with `ensure prerequisites`, you need to temporarily disable real-time protection.

Then **close and reopen** the terminal before continuing.

**Linux / Mac**:

Open the terminal and run:

```bash
curl -sL n7s.co/get-trustable | bash
```

Then **close and reopen** the terminal before continuing.

### 2. Setup

To install, run:

```bash
ops trustable setup
```

If the system cannot find ops, you need to close and reopen the terminal.

> **Note:** The installation takes some time and downloads many components. If you have a slow network and a timeout occurs, you can retry: the installation is incremental and does not restart from scratch.

## Usage

Once installed, Trustable starts automatically.

To access it on subsequent occasions, use:

```bash
ops trustable signin
```

## Troubleshooting

To check the health status of the installation, use:

```bash
ops trustable doctor
```

If the doctor detects issues, try a simple resolution with:

```bash
ops trustable restart
```

To access logs and facilitate debugging, you can inspect what is happening with the command:

```bash
ops trustable logs
```

Logs are continuous. Press Control-C to stop the display.


## Uninstallation

To remove everything use:

```bash
ops trustable uninstall
```
