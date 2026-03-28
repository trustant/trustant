# ![](./web/trustable-logo.png){ height=50px } Trustable - Istruzioni per l'installazione

> **Nota:** La release è al momento in versione alpha ed è possibile che contenga errori. Potete riportarli su `github.com/trustable-ai`

## Prerequisiti

### Docker Desktop

Trustable richiede ed utilizza Docker. Occorre scaricare e installare **Docker Desktop** prima di cominciare e averlo in esecuzione.

> **Attenzione:** Docker Desktop richiede una login per scaricare le immagini. Effettuare il login prima di procedere.

### Memoria

Richiede almeno **16 GB di memoria RAM**. È molto improbabile che funzioni accettabilmente con meno.

### Ollama Cloud

Per i modelli AI, Trustable utilizza **Ollama Cloud**. È disponibile anche la versione free, che fornisce crediti gratuiti (ma limitati) per provare. Occorre registrare un account gratuito.

Se si vuole lavorare più intensamente occorre:

- Un **Ollama locale con GPU** (come le nostre BestIA), oppure
- Un **account Pro** su Ollama Cloud

### Sicurezza

La AI agisce solo all'interno di un container Docker e non ha accesso al filesystem, eccetto nella cartella `~/.ops-workspace` dove viene salvato il lavoro.

I rischi di danni sono quindi limitati, ma è sempre bene essere cauti e non usare un computer di produzione o con dati sensibili.

### Sistemi operativi supportati

Testato su:

- Windows 11
- macOS Ventura
- Ubuntu 22.04

È possibile che ci siano problemi su altri sistemi operativi o configurazioni.

## Installazione

### 1. Scaricare Trustable

**Windows** (PowerShell):

```powershell
powershell -e "irm n7s.co/get-trustable | iex"
```

**Linux / macOS** (Terminale):

```bash
curl -sL n7s.co/get-trustable | bash
```

### 2. Setup

Chiudere il terminale, riaprirlo e poi eseguire:

```bash
ops trustable setup
```

> **Nota:** L'installazione dura un certo tempo e scarica molti componenti. Se avete una rete lenta e si verifica un timeout, si può riprovare: l'installazione è incrementale e non riparte da zero.

## Utilizzo

Una volta installato, Trustable parte in automatico. Altrimenti usare:

```bash
ops trustable signin
```

## Logging e Debugging

Per accedere ai log e facilitare il debugging potete vedere cosa succede con il comando

```bash
ops trustable logs
```

I log sono continui. Premere Control-C per interropere la visualizzazione.

## Disinstallazione

Per disinstallare tutto:

```bash
ops trustable uninstall
```
